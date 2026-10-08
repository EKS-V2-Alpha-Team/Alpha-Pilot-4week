# Decisions

One page per choice that is hard to undo or that someone will ask about. Each page lists the options, what we picked, what it costs us and when to look again.

| # | Decision | In one line |
|---|---|---|
| [0001](0001-order-events-outbox.md) | Outbox and a Standard queue | An order and its event are saved together; a loop sends the event to SQS. |
| [0002](0002-safe-to-repeat-in-the-database.md) | The database makes steps safe to repeat | Unique indexes and guarded updates, so a repeat or a race does no damage. |
| [0003](0003-gateway-rate-limiting.md) | Rate limiting in the gateway | Fixed 60 s window in Redis, per user and per real client IP. Fails open. |
| [0004](0004-sqs-encryption.md) | SQS encryption | SSE-SQS, no KMS key of our own. |
| [0005](0005-redis-in-the-cluster.md) | Redis in the cluster | One pod with a small encrypted disk, not ElastiCache. |
| [0006](0006-prices-come-from-inventory.md) | Prices come from inventory | The price in an order request is ignored. |
| [0007](0007-idempotency-keys.md) | Idempotency keys | New orders and charges carry a key; a retry returns the first answer. |

What we chose not to build, and why, is in [Known gaps](../known-app-gaps.md).
