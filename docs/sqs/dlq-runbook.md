# Runbook: messages in the DLQ

Use this when the CloudWatch alarm `eks-v2-<env>-order-events-dlq-not-empty` fires, or when the dashboard shows orders stuck at `pending` or `confirmed` for more than a few minutes.

- A message lands in the DLQ after the worker failed it 3 times. Its order is stuck until someone looks.
- Nothing is lost: the DLQ keeps messages for 14 days.
- Redrive only after the cause is fixed, and within 4 days of the order. A redriven message keeps its original send time, and the main queue deletes anything older than its 4 day retention.
- Redriving is safe even if part of the order already went through. Every worker step is safe to repeat ([decision 0002](../adr/0002-safe-to-repeat-in-the-database.md)).

Time to resolve when the cause is known: about 10 minutes.

---

## 1. See what is in the DLQ

```bash
ENV=dev   # your environment
DLQ_URL=$(aws sqs get-queue-url --queue-name "eks-v2-$ENV-order-events-dlq" --query QueueUrl --output text)
aws sqs get-queue-attributes --queue-url "$DLQ_URL" --attribute-names ApproximateNumberOfMessages
aws sqs receive-message --queue-url "$DLQ_URL" --max-number-of-messages 10 \
  --visibility-timeout 0 --attribute-names All
```

`--visibility-timeout 0` leaves the messages visible, so reading them doesn't hide them from the redrive. Each body is an order event, for example `{"id":"<event id>","type":"order.created","payload":{"order_id":42,...}}`. Write down the event ids and order ids.

## 2. Find out why the worker failed

```bash
kubectl -n order-platform logs deploy/worker --since=2h | grep -E "<event id>|order 42"
```

The failure line reads `Failed to handle event order.created (<event id>): <reason>`.

| The reason starts with | Usual cause | Fix |
|---|---|---|
| `reserve: ...`, `charge: ...`, `commit stock: ...` with a connection error | inventory or payment was down | Check those pods (`kubectl -n order-platform get pods`). Once they are healthy, redrive. |
| `unexpected status 500` | a bug or a database problem in the called service | Read that service's logs for the same order id. Fix and deploy, then redrive. |
| `get order: ... 404` | the order does not exist (test data, or a database reset) | Nothing to redrive. Purge the message (step 4). |

## 3. Redrive

After the cause is fixed, move the messages back to the main queue:

```bash
DLQ_ARN=$(aws sqs get-queue-attributes --queue-url "$DLQ_URL" --attribute-names QueueArn --query Attributes.QueueArn --output text)
aws sqs start-message-move-task --source-arn "$DLQ_ARN"
aws sqs list-message-move-tasks --source-arn "$DLQ_ARN"
```

With no destination given, SQS sends each message back to the queue it came from. Only one move can run per queue at a time.

Check it worked: the DLQ count from step 1 is 0, the orders have moved on in the dashboard, and the alarm goes back to OK within 5 minutes.

## 4. If a message must not be retried

Delete just that message, using the `ReceiptHandle` from step 1:

```bash
aws sqs delete-message --queue-url "$DLQ_URL" --receipt-handle "<ReceiptHandle>"
```

Then fix the order by hand in the dashboard (cancel it, which refunds and puts the stock back), and note what happened in the thread.

---

## The alarm

| Setting | Value | Why |
|---|---|---|
| Metric | `ApproximateNumberOfMessagesVisible` on the DLQ | AWS recommends it for DLQs. `NumberOfMessagesSent` does not count messages moved there by the redrive policy. |
| Fires when | at least 1 message, over 5 minutes | Any message in the DLQ is an order that needs a person. |
| No data | counts as OK | An idle queue sends no data points. Without this the alarm would sit in "insufficient data". |
| Who is told | `dlq_alarm_actions` in the sqs module (empty by default) | Until a target is set (for example an SNS topic), the alarm shows only in the CloudWatch console. Set it per environment in the Terraform variables. |

Code: `infra/terraform/modules/sqs/main.tf`, resource `aws_cloudwatch_metric_alarm.order_events_dlq_not_empty`.

## Sources

- AWS: [CloudWatch metrics for SQS](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-available-cloudwatch-metrics.html) (why `ApproximateNumberOfMessagesVisible` for a DLQ), [dead letter queues](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-dead-letter-queues.html) (retention keeps the original send time), [`StartMessageMoveTask`](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_StartMessageMoveTask.html)
- Terraform AWS provider: [`aws_cloudwatch_metric_alarm`](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/cloudwatch_metric_alarm)
