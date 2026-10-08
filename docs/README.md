# Documentation: start here

An order platform: 9 Go services behind one gateway, one Postgres database, one SQS queue. A customer places an order, the platform holds the stock, takes the payment, confirms the order, ships it and emails the customer.

**Fastest way in (5 minutes).** Run it and place an order: [Run, build, test](app/run-build-test.md).

## Read in this order

| Time | Read | You will know |
|---|---|---|
| 5 min | This page | What each service does and how an order moves |
| 15 min | [How an order flows](app/order-flow.md) | The full journey, what happens on failure, what not to break |
| 10 min | [Gateway and dashboard](app/gateway-and-dashboard.md) | How requests get in, who is let in, why you get a 429 |
| 5 min | [Known gaps](known-app-gaps.md) | What is not built, why, and what it would take |
| When needed | [Order events queue](sqs/order-events-queue.md), [DLQ runbook](sqs/dlq-runbook.md), [Redis](redis/redis-in-the-cluster.md), [tests](../tests/README.md) | One topic each |
| When you ask "why?" | [Decisions](adr/README.md) | The options we weighed and what we picked |

## The services

| Service | Port | What it does |
|---|---|---|
| api-gateway | 8080 | The only way in. Login, rate limits, forwards the public `/api/...` routes, serves the dashboard. |
| order-service | 8081 | Orders and their status. Sends order events to SQS. |
| inventory-service | 8082 | Products and stock holds (reserve, release, commit). |
| payment-service | 8083 | Charges, refunds, ledger. The card provider is simulated and declines about 1 in 10. |
| notification-service | 8084 | Records each email once. No email provider, so nothing leaves the system. |
| shipping-service | 8085 | Shipments and the carrier webhook. |
| worker | none | Reads the queue and drives each order through its steps. |
| scheduler | none | Timed jobs: expire stock holds, cancel abandoned orders, retry payments, clean up old rows. Run one copy only. |
| dashboard-api | 8086 | The admin page and its reports. |

Also running: Postgres (5432), Redis (6379, rate-limit counters) and, on a laptop, Floci (4566, a local stand-in for AWS). On a laptop the other ports are open for debugging; on EKS only the gateway is reachable from outside.

```
 browser or curl
       |
       v
 api-gateway :8080 ---- redis (rate limits)
       |
       +--> order-service --- order + event saved together ---> postgres
       |         |
       |         | relay sends the event (about 1 second)
       |         v
       |    SQS "order-events" ---> worker ---> inventory, payment, order-service
       |         |
       |         | after 3 failed tries
       |         v
       |    "order-events-dlq"
       |
       +--> inventory, payment, shipping, notification, dashboard-api
```

## One order in 6 steps

1. The customer places an order through the gateway. order-service saves it as `pending` together with its event, in one database transaction.
2. order-service sends the event to SQS, usually within a second.
3. The worker asks inventory to hold the stock.
4. The worker asks payment to charge.
5. The worker marks the order `confirmed`.
6. The worker tells inventory to commit the hold. The items are sold.

Out of stock or card declined: the worker undoes what it did and cancels the order. After `confirmed`, moving the order to processing, shipped and delivered creates the shipment and sends the emails.

## Words you will see

| Word | Meaning here |
|---|---|
| Event | A small JSON message saying something happened, like `order.created`. |
| Outbox | The `order_outbox` table. Events are saved there with the order, then sent. |
| Relay | The loop in order-service that sends outbox rows to SQS. |
| At least once | SQS always delivers, but sometimes delivers the same message twice. |
| Safe to repeat | Doing a call twice has the same result as doing it once (idempotent). |
| Hold | Stock set aside for an order. Expires after 15 minutes unless committed. |
| Commit | Turning a hold into a sale. Stock goes down for good. |
| DLQ | Dead letter queue. Where a message goes after 3 failed tries. |
| Visibility timeout | How long SQS hides a message from other workers while one handles it. |
| Floci | A local program that pretends to be AWS, so SQS runs on a laptop for free. |
