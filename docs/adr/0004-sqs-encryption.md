# 0004: Encrypt the order events queue with SSE-SQS

**Status:** Accepted.
**Decision in one line:** both queues use SQS-managed encryption (SSE-SQS), with no KMS key of our own.

## Context

**What is in the messages.** Order event messages carry the order details and the customer's email. They carry no card data, because payment-service's `/charge` takes no card details.

**What was asked for.** PCI DSS: do not store card details. Security, reliability and scale in balance, without optimising one and ignoring the others. AWS spend is capped at $200 and is the first priority.

**What SQS offers.** Three ways to encrypt at rest ([AWS docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-server-side-encryption.html)). All three encrypt only the message body. Queue names, message IDs, timestamps and message attributes are never encrypted.

**The existing pattern.** AWS-owned keys (AES256) for the Terraform state bucket and ECR. Our own KMS keys, with rotation, one per purpose, for EKS secrets and Secrets Manager.

Checkov flags `CKV2_AWS_73` ("use a customer-managed key") on an SSE-SQS queue, which is why the choice was worth writing down.

## Options

| Option | What it is | Cost | What it adds |
|---|---|---|---|
| **A. SSE-SQS (chosen)** | SQS encrypts with a key AWS owns and manages | Free ([AWS](https://aws.amazon.com/about-aws/whats-new/2021/11/amazon-sqs-server-side-encryption-keys-sse/)) | Nothing. No KMS permissions, no key to manage. We cannot see or control the key. |
| B. SSE-KMS with the AWS managed key (`alias/aws/sqs`) | A KMS key AWS creates in our account for SQS | No monthly fee, but every KMS call is billed ([KMS pricing](https://aws.amazon.com/kms/pricing/)) | Both pod roles need KMS permissions. Key use shows in CloudTrail. We cannot change the key's policy. |
| C. SSE-KMS with our own key (new, or the Secrets Manager one) | A key we control | $1 a month per new key, plus calls. About 27,000 calls a month for one sender and one reader, with SQS reusing a data key for 5 minutes: just over the 20,000 free, so a few cents | Full control: key policy, rotation, audit, revoke. Both pod roles need KMS permissions. If KMS refuses a call, sends and receives fail. Reusing the Secrets Manager key would tie the two modules together. |

## Decision

**A. SSE-SQS on both queues.**

- The messages hold no card data.
- Encryption at rest is covered for free.
- B and C add KMS permissions to two roles and a new way for sending and receiving to fail. That buys control over the key, and nothing we run today uses that control.
- It follows the rule used for data that does not need key control, as with the state bucket and ECR.

## Consequences

- No cost, no KMS permissions, no key to rotate.
- Message metadata stays readable, so customer data must never go in message attributes.
- Checkov flags `CKV2_AWS_73` on both queues. That is this decision. CI runs checkov in soft-fail mode, so it does not block.
- We cannot show who used the key, or revoke it. If an audit asks for that, we need B or C.
- Switching later means setting a KMS key on the existing queues. They are not recreated, but both pod roles need KMS permissions first.

## How we know it works

`aws sqs get-queue-attributes` reports `SqsManagedSseEnabled: true` on both queues, and checkov's encryption check (`CKV_AWS_27`) passes.

## Revisit when

- An audit or a stakeholder asks for customer-managed keys, or for a record of who used the key.
- Messages start carrying more sensitive data.

## Sources

[SQS server-side encryption](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-server-side-encryption.html), [SSE-SQS announcement](https://aws.amazon.com/about-aws/whats-new/2021/11/amazon-sqs-server-side-encryption-keys-sse/), [KMS pricing](https://aws.amazon.com/kms/pricing/).
