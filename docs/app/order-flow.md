# How an order flows

Everything that happens after an order is placed, until it is confirmed or cancelled.

- A retried request returns the first order instead of making a second one (`Idempotency-Key`). Prices come from inventory, never from the request.
- order-service saves the order and its event in one database transaction (the outbox). A loop then sends the event to SQS, usually within a second. If SQS is down, the order is still accepted and the event waits.
- The worker reads the queue and runs four steps: hold the stock, charge, confirm the order, commit the stock.
- Out of stock or card declined is a final answer. The worker undoes what it did, cancels the order and deletes the message.
- A 5xx or a timeout is not a final answer. The worker leaves the message on the queue, SQS hands it out again, and after 3 tries it moves to the dead letter queue (DLQ).
- SQS can deliver a message twice, so every step is safe to repeat. The database enforces this, so it holds no matter who calls.

Stop here if you only need the shape. Below: the full picture, the failure cases and the rules you must not break.

---

## 1. The happy path

Sara orders 2 mugs. Her order becomes order 42.

```
Sara --> gateway --> order-service
                       | one transaction:
                       |   orders          (order 42, pending)
                       |   order_events    (history row)
                       |   order_outbox    (order.created, not sent yet)
                       v
                  201 back to Sara

relay (every second, inside order-service)
   reads unsent outbox rows, sends them to SQS, marks them sent

worker (waits on the queue, up to 20 seconds per call)
   0. GET  order     /42                             still pending? then carry on
   1. POST inventory /reserve   {order 42, 2 mugs}   hold the stock
   2. POST payment   /charge    {order 42, 20 GBP}   take the money
   3. PUT  order     /status    {order 42, confirmed}
   4. POST inventory /commit    {order 42}           stock 5 becomes 3
   5. delete the message from the queue
```

About 5 seconds after Sara clicks, order 42 shows as `confirmed` on the dashboard and the mug stock has dropped by 2.

**Before the order is saved**, order-service checks:

| Check | If it fails |
|---|---|
| An `Idempotency-Key` header is there (255 characters at most) | 400 |
| This customer already used the key | Same request: 200 with the first order and `Idempotent-Replayed: true`. Different request: 422. Nothing new is saved either way. |
| Each product exists and is in stock | Unknown product 422, not enough stock 409, inventory down 503 |

The key is claimed in the same transaction as the order, so two identical requests at the same moment still make one order. Keys are kept 24 hours. See [decision 0007](../adr/0007-idempotency-keys.md).

**After it is confirmed**, the dashboard moves the order on. Each status change is an event the worker acts on:

| Status | What the worker does |
|---|---|
| `confirmed` | Emails the customer (`order_confirmed`) |
| `processing` | Creates the shipment (with a placeholder address, see [Known gaps](../known-app-gaps.md)) |
| `shipped` | Moves the shipment to `in_transit`, emails the tracking number |
| `delivered` | Moves the shipment to `delivered`, emails the customer |
| `cancelled` | Releases the hold or returns sold stock, refunds, cancels the shipment, then emails `order_cancelled` |

Every email has a dedupe key such as `order_cancelled:42`, so a repeated event sends nothing new.

---

## 2. When things go wrong

The worker sorts every reply into one of two kinds.

- **A business "no".** Final. Retrying will not change it. The worker undoes what it did, cancels the order and deletes the message.
- **A technical failure.** Something was down or slow. The worker keeps the message so SQS retries it.

| Step | Reply | What the worker does |
|---|---|---|
| Reserve | 201 or 200 | Carries on. 200 means the hold already existed from an earlier try. |
| Reserve | 409 (not enough stock) or 404 (unknown product) | Cancels the order. Never charges. Deletes the message. |
| Charge | 201 or 200 | Carries on. 200 means this order was already paid. |
| Charge | 402 (card declined) | Releases the hold, cancels the order, deletes the message. |
| Confirm or commit | 200 | Carries on. |
| Any step | 5xx, timeout, or service unreachable | Stops and keeps the message. SQS retries after 60 seconds. |

**Payment-service is down:**

1. The worker holds the stock, then the charge fails. The message stays on the queue.
2. SQS hands it out again 60 seconds later, up to 3 times in total.
3. After the 3rd failure the message moves to `order-events-dlq`. Order 42 stays `pending` and its stock stays held.
4. Once payment is back, someone redrives the message ([DLQ runbook](../sqs/dlq-runbook.md)). The worker finishes the order.

A redrive is safe because every step is safe to repeat: no second hold, no second charge.

**A message that comes back after the order is finished** (the commit failed once after the confirm, or the delete failed after a cancel). The worker checks the order's status first. A confirmed order only gets its stock committed, a cancelled order only gets its hold released. It never charges again.

**The hold runs out mid-payment.** A hold lasts 15 minutes. If it expires between the charge and the commit, inventory answers the commit with 409. The worker holds the stock again and commits it; if the stock is gone it cancels the order, which refunds it. The scheduler and the commit cannot both win: whichever claims the hold first in the database wins, and stock is given back once.

**Abandoned orders.** The scheduler cancels orders left `pending` for 30 minutes by asking order-service with `expected_status: pending`. If the order moved on, order-service refuses and nothing happens. Because the cancel goes through order-service, the worker gets the event and refunds and restocks like any other cancel.

---

## 3. Rules that must stay true

If you change any of these services, keep every rule. Each one is what makes a retry harmless.

1. An order and its event are saved together, or not at all.
2. Every event has a unique `id`, for example `order-service-42`.
3. The worker deletes a message only after it has handled it.
4. An order holds stock at most once per product.
5. Releasing a hold lowers `reserved` once, however many times release is called.
6. An order is charged successfully at most once.
7. A committed hold never expires and never goes back on sale.
8. The queue's visibility timeout stays longer than the slowest order (60 s against about 55 s). See [Order events queue](../sqs/order-events-queue.md).
9. When two status changes arrive at the same time, only one wins. Asking for the status the order already has returns success and changes nothing.
10. Before doing anything, the worker checks the order's status. `pending` runs the full flow, `cancelled` only releases the hold, `confirmed` or later only commits the stock.
11. `AWS_ENDPOINT_URL` is set only on laptops. On EKS it must not be set, or events go to Floci instead of AWS.
12. Every `POST /orders` needs an `Idempotency-Key`. The worker charges with the key `charge-order-<id>`, the same on every retry, so a retried charge gets the first answer back, a decline included.
13. Prices come from inventory. Never trust a price in the request.
14. Anything that compares times does it in Postgres (`NOW()`), never with a Go time. Several columns have no time zone, and a Go time compares wall clocks.
15. Only order-service changes an order's status. Other services, the scheduler included, ask it.

---

## 4. Why it is built this way

**Outbox.** Saving the order and sending the event are two separate systems. If the save works and the send fails, the order exists but nobody hears about it, and the scheduler cancels it 30 minutes later with no error anywhere. Saving the event in the same transaction means it cannot be lost, and SQS being down no longer blocks orders. [Decision 0001](../adr/0001-order-events-outbox.md).

**Standard queue, not FIFO.** FIFO still delivers duplicates after a crash, and one stuck message blocks its whole group. [Decision 0001](../adr/0001-order-events-outbox.md).

**The database enforces "safe to repeat".** SQS delivers at least once, so repeats are normal. Before the fix, 6 charges for one order made 5 completed payments, and 20 overlapping releases took `reserved` to minus 57. A unique index or a guarded UPDATE holds no matter who calls, how often, or at the same moment. [Decision 0002](../adr/0002-safe-to-repeat-in-the-database.md).

**Declines are not retried.** A declined card is still declined on the next try. Retrying only delays the cancel and keeps stock held for nothing.

**Long polling (20 seconds per call).** SQS bills every request, empty ones too. One idle worker polling every 5 seconds makes 17,280 requests a day; waiting 20 seconds makes 4,320, and a message still arrives the moment it is sent. The AWS free tier is 1 million requests a month.

**A commit step for stock.** Without it a hold could only be active, released or expired, so even a paid order's stock went back on sale after 15 minutes. Commit is a fourth state the scheduler never expires.

---

## 5. What we accepted

- Messages can arrive twice. Fine, because every step is safe to repeat.
- An event reaches the queue up to about a second after the order is saved.
- A message in the DLQ needs a person to redrive it.
- The worker's status check is one extra call to order-service per message. If order-service is down, the message waits like any other technical failure.
- Old rows are cleaned up by the scheduler: order history after 90 days, sent outbox rows after 7 days (unsent rows never), order keys after 24 hours, charge keys after 14 days (as long as a message can wait in the DLQ), notifications after 90 days. Orders, payments and shipments are kept ([Known gaps](../known-app-gaps.md)).

---

## 6. Check it yourself

- Run the unit tests ([tests](../../tests/README.md)). Remove a unique index or a status guard and a test fails; that is how we know they test the rule.
- Run the smoke test against a running stack. It places a real order and checks every step above.
- Try the failure drills (SQS down, a service down, DLQ redrive) in [Run, build, test](run-build-test.md).

---

## 7. Reference

### Event message

```json
{
  "id": "order-service-42",
  "type": "order.created",
  "timestamp": "2026-01-15T10:15:00Z",
  "payload": {
    "order_id": 42,
    "customer_id": "sara@example.com",
    "items": [{ "product_id": "mug-1", "quantity": 2, "price": 10 }],
    "total": 20,
    "currency": "GBP"
  }
}
```

| Type | Sent when | Payload |
|---|---|---|
| `order.created` | An order is placed | order_id, customer_id, items, total, currency |
| `order.status_changed` | A status changes | order_id, old_status, new_status |

### Replies the worker relies on

| Call | Reply | Meaning |
|---|---|---|
| inventory `POST /reserve` | 201 | New hold made |
| | 200 | This order already had its hold (or its stock is already committed) |
| | 409 / 404 | Not enough stock / unknown product |
| inventory `POST /release` | 200 | Active holds released. Calling again changes nothing. |
| inventory `POST /commit` | 200 | Active holds turned into a sale. Calling again commits nothing more. |
| payment `POST /charge` | 201 | Charged |
| | 200 with `already_paid: true` | Already charged with the same customer, amount and currency |
| | 409 | Already charged with different details (a bug in the caller) |
| | 402 | Card declined |
| order `GET /{id}` | 200 | The order, with its current status. The worker reads this first. |
| order `PUT /status` | 200 | Changed, or already at that status (`changed: false`) |
| | 409 | Not allowed from the current status, or another change got there first |

### States

Order: `pending` becomes `confirmed` or `cancelled`. `confirmed` becomes `processing` or `cancelled`. `processing` becomes `shipped` or `cancelled`. `shipped` becomes `delivered`.

Stock hold: `active` becomes `released`, `expired` (after 15 minutes, by the scheduler) or `committed`. Only `active` holds expire.

### Where the code lives

| What | Where |
|---|---|
| Outbox and relay | `services/order-service/outbox.go`, `publisher.go` |
| Idempotency keys on orders | `services/order-service/idempotency.go` |
| Status changes | `services/order-service/main.go`, `handleUpdateStatus` |
| Reserve, release, commit | `services/inventory-service/main.go` |
| Charge rule and charge keys | `services/payment-service/main.go`, `idempotency.go` |
| Worker | `services/worker/main.go`, `processNewOrder` |
| Cleanup rules | `services/scheduler/retention.go` |
| Local queue and DLQ | `scripts/floci-init.sh` |
