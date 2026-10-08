# Order events queue (SQS)

The SQS queue that carries order events in AWS: what the app needs from it, how it is set up, and why.

- One queue, `eks-v2-<env>-order-events`, plus a dead letter queue (DLQ), `eks-v2-<env>-order-events-dlq`. Terraform builds both in `infra/terraform/modules/sqs`.
- order-service sends events to the queue. The worker reads them and deletes each one once handled.
- If the worker fails the same message 3 times, SQS moves it to the DLQ. It waits there until someone redrives it.
- Every setting comes from the app's code or the cost cap. The settings match Floci's (`scripts/floci-init.sh`), so a laptop behaves like AWS.

Stop here if you only need the shape. Below: what drove the design, what the app needs, each setting and the rules.

---

## 1. What shaped this design

| Driver | What it means for the queue |
|---|---|
| No lost orders | Failed messages go to a DLQ instead of disappearing, and the DLQ keeps them 14 days, long enough to fix the cause and redrive. |
| Black Friday bursts | The queue absorbs a spike and the worker catches up. The limit is upstream: the outbox relay sends about 10 events a second per order-service replica, so peak traffic needs 2 or more replicas. |
| PCI DSS and security | Messages hold no card data. The body is encrypted at rest ([decision 0004](../adr/0004-sqs-encryption.md)). Each service gets only the SQS actions it uses, and pods carry no AWS keys. |
| AWS spend ($200 cap, the first priority) | Long polling keeps one idle worker at about 130,000 requests a month, inside the 1 million free. Encryption is free. No VPC endpoint (about $22 a month across 3 AZs). |
| Free local testing | Tries and visibility timeout match `scripts/floci-init.sh`, so what passes locally behaves the same in AWS. |

---

## 2. What the app needs from AWS

| Service | Env vars | What it does with SQS | AWS permission |
|---|---|---|---|
| order-service | `SQS_QUEUE_URL`, `AWS_REGION` | Sends events (the relay only) | `sqs:SendMessage` on the queue |
| worker | `SQS_QUEUE_URL`, `AWS_REGION` | Receives and deletes | `sqs:ReceiveMessage`, `sqs:DeleteMessage` on the queue |
| All other services | none | Nothing | none |

- **Where permissions come from.** EKS Pod Identity gives each pod its own role. There are no AWS keys in the pods.
- **Never set these on EKS.** `AWS_ENDPOINT_URL` would send events to Floci instead of AWS. `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` would win over Pod Identity, because the Go SDK uses keys it finds in env vars before any other source.
- **If `SQS_QUEUE_URL` is missing.** The worker refuses to start. order-service starts anyway and only logs a warning, and events pile up in `order_outbox` until the URL is set. After a deploy, check this first.
- **Where the URL comes from.** The Terraform output `sqs_queue_url`, copied by hand into the Helm values `kubernetes/deploy/envs/<env>/order-service.yaml` and `worker.yaml` after the first apply.

---

## 3. The settings

| Setting | Main queue | DLQ | In one line |
|---|---|---|---|
| Visibility timeout | 60 s | 30 s (default) | Longer than the worker's worst case for one message, about 55 s. Nothing reads the DLQ directly. |
| Long polling wait | 20 s | 0 (default) | Matches the worker's `WaitTimeSeconds`. Fewer empty, billed requests. |
| Tries before the DLQ | 3 | n/a | A stuck message reaches the DLQ in about 3 minutes. Same in Terraform and Floci. |
| Retention | 4 days (AWS default) | 14 days (the maximum) | The DLQ must keep messages longer than the main queue. |
| Encryption | SSE-SQS | SSE-SQS | Free, AWS-owned key. |
| Who may use the DLQ | n/a | Only the main queue | No other queue can send its failures here. |
| Alarm | n/a | `...-order-events-dlq-not-empty` | Fires when any message sits in the DLQ. See the [runbook](dlq-runbook.md). |

The redrive policy and the redrive allow policy are separate Terraform resources, as the provider docs recommend. Each rule points at the other queue, so keeping them out of the queue blocks avoids a dependency loop.

---

## 4. Choices we made

| Decision | Options | Picked, and why |
|---|---|---|
| Queue type | Standard; FIFO | **Standard.** FIFO still delivers duplicates after a crash, and one stuck message blocks its whole group. [Decision 0001](../adr/0001-order-events-outbox.md). |
| Visibility timeout | 30 s (default); 60 s; 10 minutes | **60 s.** AWS says to match the longest time a message takes. Ours is 5 calls with a 10 s timeout plus a 5 s delete, about 55 s. Not 10 minutes: a stuck message would take about 30 minutes to reach the DLQ, after its stock hold expires (15 min) and the scheduler cancels the order (30 min). |
| Batch size and wait | Smaller batches; a short poll | **10 messages, 20 s wait.** One idle worker makes 4,320 requests a day instead of 17,280 with a 5 s poll. A batch can overrun 60 s, which we accept (section 5). |
| Retention | 4 days both; 14 days both | **4 days main, 14 days DLQ.** A message keeps its original arrival time when it moves, so equal retention can delete it soon after it lands. Capped because messages hold the customer's email. |
| Encryption | SSE-SQS; AWS managed KMS key; our own KMS key | **SSE-SQS.** Free, no KMS permissions. [Decision 0004](../adr/0004-sqs-encryption.md). |
| Permissions | One shared role; one role per service | **One role per service.** order-service only sends. The worker receives and deletes. Nothing calls change-visibility. |
| Private network path | An SQS interface VPC endpoint | **No endpoint.** At $0.01 an hour per AZ it costs about $22 a month across 3 AZs, far more than our SQS traffic. |
| Getting the URL to the pods | Root output plus Helm values; the app looks it up by name (`GetQueueUrl`); Terraform writes onto ArgoCD's cluster secret; External Secrets | **Root output plus Helm values.** `GetQueueUrl` needs a code change in two services and a new permission. Writing onto ArgoCD's secret needs Terraform to talk to the cluster. External Secrets would store a value that is not secret, for $0.40 a month. |

---

## 5. What we accepted

- **A batch can overrun the timeout.** The worker takes up to 10 messages per call and handles them one after another, but 60 s is sized for one message. A slow batch can run past it and SQS then hands a message out twice. That is safe because every step is safe to repeat ([decision 0002](../adr/0002-safe-to-repeat-in-the-database.md)). If load tests show it, the fix AWS recommends is a heartbeat: the worker extends the timeout while it works (`ChangeMessageVisibility`).
- **DLQ redrive is manual.** Follow the [DLQ runbook](dlq-runbook.md).
- **No policy forcing TLS on the queue.** The SDK uses HTTPS anyway.
- **Message metadata is not encrypted.** SSE-SQS leaves message IDs, timestamps and attributes readable. Do not put customer data in message attributes.

---

## 6. Rules that must stay true

1. The visibility timeout stays longer than the slowest message: 60 s against about 55 s. If you add a worker call or raise a client timeout, raise this too.
2. The number of tries before the DLQ is the same in Terraform and in `scripts/floci-init.sh`: 3.
3. The DLQ keeps messages longer than the main queue does.
4. order-service and the worker have `SQS_QUEUE_URL` set. No pod on EKS has `AWS_ENDPOINT_URL` or AWS keys.
5. Only order-service sends, and only the worker receives and deletes.

---

## 7. Check it

After an apply, read the settings back from the real queue:

```bash
aws sqs get-queue-attributes --attribute-names All \
  --queue-url "$(terraform -chdir=infra/terraform output -raw sqs_queue_url)"
```

Expect on the main queue: `VisibilityTimeout` 60, `ReceiveMessageWaitTimeSeconds` 20, `MessageRetentionPeriod` 345600, `maxReceiveCount` 3 in the redrive policy, `SqsManagedSseEnabled` true. On the DLQ: `MessageRetentionPeriod` 1209600 and a `RedriveAllowPolicy` that names only the main queue. A second `terraform plan` should show no changes.

Checkov flags `CKV2_AWS_73` (no customer-managed key) on both queues. That is [decision 0004](../adr/0004-sqs-encryption.md), and CI runs checkov in soft-fail mode.

---

## 8. Reference

| Terraform output | Value in dev | Used by |
|---|---|---|
| `sqs_queue_url` | `https://sqs.eu-west-2.amazonaws.com/<account>/eks-v2-dev-order-events` | Helm values, as `SQS_QUEUE_URL` |
| `module.sqs.queue_arn` | `arn:aws:sqs:eu-west-2:<account>:eks-v2-dev-order-events` | Pod identity permissions |
| `module.sqs.dlq_arn` | `arn:aws:sqs:eu-west-2:<account>:eks-v2-dev-order-events-dlq` | DLQ redrive (`--source-arn`) |

| What | Where |
|---|---|
| Queues, redrive rules, alarm | `infra/terraform/modules/sqs/main.tf` |
| Module wiring and the URL output | `infra/terraform/main.tf`, `infra/terraform/output.tf` |
| Local queue and DLQ | `scripts/floci-init.sh` |
| Sending | `services/order-service/publisher.go` |
| Receiving and deleting | `services/worker/main.go` |

AWS and Terraform docs this follows: [visibility timeout](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-visibility-timeout.html), [dead letter queues](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-dead-letter-queues.html), [server-side encryption](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-server-side-encryption.html), [pricing](https://aws.amazon.com/sqs/pricing/), [Go SDK credentials](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-gosdk.html), [PrivateLink pricing](https://aws.amazon.com/privatelink/pricing/), [`aws_sqs_queue`](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/sqs_queue), [`aws_sqs_queue_redrive_policy`](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/sqs_queue_redrive_policy), [`aws_sqs_queue_redrive_allow_policy`](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/sqs_queue_redrive_allow_policy), [`GetQueueUrl`](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_GetQueueUrl.html).
