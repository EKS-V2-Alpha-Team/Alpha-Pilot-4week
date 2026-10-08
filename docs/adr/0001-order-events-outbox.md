# 0001: Order events go through an outbox and a Standard SQS queue

**Status:** Accepted.
**Decision in one line:** order-service saves each event in the same database transaction as the order, and a background loop sends it to a Standard SQS queue.

## Context

When an order is created or changes status, order-service tells the rest of the platform with an event, a small JSON message on the SQS queue `order-events`. The worker reads that queue and calls the other services.

Two questions came up:

1. **What if sending fails?** The order goes to Postgres and the event goes to SQS, two different systems. If the save works and the send fails, the order exists but nobody hears about it. The scheduler cancels it 30 minutes later, and no error shows up anywhere.
2. **Standard or FIFO queue?**

What decided it: zero tolerance for data loss, cost as the top priority, and audit readiness (PCI DSS).

## Options

**When a send fails:**

| Option | What it does | Result |
|---|---|---|
| A1. Send directly, log failures | Simplest | A failed send loses the event for good. Breaks "zero data loss". |
| A2. Send directly, return 500 on failure | The caller sees the error | The order is already saved, so a client retry creates a duplicate order. |
| **A3. Outbox** | Save the event with the order; a loop delivers it | **Chosen.** Nothing is lost, and taking an order no longer depends on SQS being up. Costs one table, one loop and about 200 lines of code plus tests. |

**Queue type:**

| Option | For | Against |
|---|---|---|
| **Standard (chosen)** | Already set up and tested, including redelivery and the DLQ. The order status rules already reject out-of-order changes. | Messages can arrive twice or out of order. |
| FIFO | Strict order per group; the sender's repeats are removed for 5 minutes | The worker still gets duplicates after a crash or a timeout. One stuck message blocks its whole group. AWS warns a DLQ breaks FIFO order. Needs a queue rename, a Terraform change and a group ID on every send. |

## Decision

- The event is written to `order_outbox` in the same transaction as the order change. Both are saved, or neither is.
- A loop inside order-service (the relay) reads unsent rows every second, sends them to SQS and marks them sent. If SQS is down, rows stay unsent with `attempts` and `last_error` filled in, and the relay tries again on the next pass.
- Keep the Standard queue with its DLQ (`maxReceiveCount` 3).

## Consequences

What we gain:

- An order cannot exist without its event.
- Orders are still accepted while SQS is down, and the events are delivered once it is back.
- The order, its history row and its event are saved together.
- Safe with several order-service copies on EKS: `FOR UPDATE SKIP LOCKED` stops two copies sending the same row.
- An idle relay makes no SQS requests, because it reads Postgres, not SQS.

What we accept:

- **At least once.** If the pod dies after SQS accepted a message but before the row was marked sent, the event goes again. Every event has a unique `id`, and every step the worker calls is safe to repeat ([0002](0002-safe-to-repeat-in-the-database.md)).
- **Up to about 1 second of delay** before an event reaches the queue.
- **Log noise during an outage:** one error per second until SQS is back.
- **Sent rows hold the customer email.** The scheduler deletes them after 7 days. Unsent rows are never deleted.

## How we know it works

order-service tests cover the atomic save, the rollback when the outbox write fails, the relay sending and marking rows, the relay keeping events while the queue is down, two copies not double-sending, and a real SQS send. Break the code on purpose (send before commit, drop the lock) and they fail. Run them: [tests](../../tests/README.md). Drill: stop SQS, place an order (still 201), start SQS, and the event is delivered ([Run, build, test](../app/run-build-test.md)).

## Revisit when

payment-service and shipping-service need to send events that something consumes. Use the same pattern.

## Sources

AWS: [at-least-once delivery](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/standard-queues-at-least-once-delivery.html), [visibility timeout](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-visibility-timeout.html), [dead letter queues](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-dead-letter-queues.html), [FIFO delivery](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/FIFO-queues-understanding-logic.html).
