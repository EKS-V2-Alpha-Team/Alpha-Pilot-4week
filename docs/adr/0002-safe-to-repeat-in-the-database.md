# 0002: The database makes every worker step safe to repeat

**Status:** Accepted.
**Decision in one line:** stock holds, charges and status changes are protected by database rules (unique indexes and guarded updates), so a repeated or simultaneous call cannot do damage, whoever sends it.

## Context

SQS delivers every message at least once, and sometimes twice ([0001](0001-order-events-outbox.md)). The worker also retries on purpose after a timeout. So every call the worker makes will sometimes arrive twice, or twice at the same moment.

We reproduced what that did to the original code before fixing anything:

| Call | What happened |
|---|---|
| Charge the same order 6 times | 6 payment rows, 5 of them completed |
| Release the same hold 20 times at once | `reserved` went from 3 to minus 57 |
| Confirm and cancel the same order at once | Both succeeded in 25 of 30 runs |

## Options

| Option | What it is | Result |
|---|---|---|
| **A. Database rules (chosen)** | Unique indexes and guarded updates, so a second attempt finds the first one's result | Holds for any caller and any timing, with no caller changes. |
| B. Idempotency-Key header on `/charge` | The caller sends a key, and payment saves and replays the first result for that key | Exact replay, including failed attempts. **Also built** ([0007](0007-idempotency-keys.md)): the database rule still stops a second completed charge, and the key stops a declined charge being approved on a retry. |
| C. Worker skips events it has already seen, by `id` | Keep a list of processed event IDs | Not needed while every step is safe to repeat. **Not built.** |

## Decision

**Stock holds (inventory):**

- At most one active hold per order and product, enforced by a unique partial index.
- A repeated reserve returns the existing hold with 200, where a new one returns 201. A reserve for stock that is already committed also returns 200 and holds nothing.
- Release claims its rows with `UPDATE ... SET status = 'released' WHERE status = 'active' RETURNING ...`. Only the call that actually changed a row lowers `reserved`.

**Charges (payment):**

- At most one completed charge per order, enforced by a unique partial index. Refund rows are left out, because they are stored as completed with method `refund`.
- A repeat with the same customer, amount and currency returns 200 with the existing payment ID and `already_paid: true`.
- A repeat with different details returns 409 with the existing payment ID. That means the caller has a bug.
- `/charge` refuses `method: refund`.

**Status changes (order):**

- Update only if the status is still what we read (`WHERE status = <current>`), so only one of two simultaneous changes wins. The loser gets 409.
- Asking for the status the order already has returns 200 with `changed: false` and writes nothing.

**Worker:** before any step it reads the order's status. `pending` runs the full flow, `cancelled` only releases, `confirmed` or later only commits. A message that comes back late cannot redo work the order has moved past.

## Consequences

- Retries and DLQ redrives are safe.
- The unique indexes cannot be created if duplicates already exist. Before deploying to a database with old data, check that no order has two completed charges or two active holds for the same product.
- The database rule alone does not replay a failed attempt; the charge key does ([0007](0007-idempotency-keys.md)).
- A message that comes back after an order is confirmed no longer ends in the DLQ: the worker sees `confirmed` and only commits the stock.
- The worker makes one extra call per message (the status read). If order-service is down, the message waits and is retried like any other technical failure.

## How we know it works

- inventory: reserve and release tests, with 20 concurrent releases on a warmed connection pool. The original code ended at minus 57; the new code ends at 0.
- payment: with the unique index removed, 20 concurrent charges all complete, so the index is what stops it.
- order status: confirm and cancel race 30 times. Removing the `WHERE status` guard or the same-status check makes a test fail.
- worker: a message coming back after a confirm or a cancel, and order-service being down during the check.

Run them: [tests](../../tests/README.md).

## Revisit when

A step is added that is not safe to repeat. Then add option C for that step, or a key like [0007](0007-idempotency-keys.md).
