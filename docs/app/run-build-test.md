# Run, build, test

The hands-on page: run the app on your laptop, watch an order move, break things on purpose, build the images.

The 2-minute version:

```bash
docker compose up -d --build
# open http://localhost:8080 (not 8086), log in with any email and password,
# place an order, check it shows "confirmed" within about 5 seconds
go run tests/smoke/main.go -base http://localhost:8080    # every step should say PASS
```

---

## 1. Run the stack

```bash
docker compose up -d --build
docker compose ps          # everything should say Up
```

Open **http://localhost:8080**. Login is open today, so any email and password works. The dashboard on 8086 loads, but all its data calls fail there (see [Gateway and dashboard](gateway-and-dashboard.md)).

If an order sits at `pending` after `docker compose up --build`, restart the two services that talk to the queue: `docker compose restart worker order-service`.

Local AWS is Floci on port 4566. At start it runs `scripts/floci-init.sh`, which creates the `order-events` queue and its dead letter queue `order-events-dlq`. A message moves to the DLQ after 3 failed tries. The visibility timeout is 60 seconds.

On a fresh start the worker sometimes polls before the queue exists. It logs one `NonExistentQueue` error, then recovers 5 seconds later. Ignore it.

---

## 2. Place an order and watch it move

From the dashboard: Orders tab, pick a product and quantity, Place Order. The price always comes from inventory.

From the terminal:

```bash
TOKEN=$(curl -s -X POST localhost:8080/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"anything"}' | sed -E 's/.*"token":"([^"]+)".*/\1/')

curl -s -X POST localhost:8080/api/orders/ -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -H "Idempotency-Key: $(date +%s%N)" \
  -d '{"items":[{"product_id":"mon-001","quantity":1}]}'
# note the "id" in the reply, for example 42. Send the same command again with the same key
# and you get order 42 back, not a new order.

sleep 5
curl -s localhost:8080/api/orders/42 -H "Authorization: Bearer $TOKEN"
# expect "status":"confirmed"
```

Check each step in the database:

```bash
docker compose exec postgres psql -U app -d orders

-- was the event sent?
SELECT id, event_type, sent_at IS NOT NULL AS sent, attempts, last_error
FROM order_outbox ORDER BY id DESC LIMIT 5;

-- was stock held, then committed?
SELECT order_id, product_id, quantity, status FROM reservations ORDER BY created_at DESC LIMIT 5;

-- was it charged once?
SELECT order_id, amount, status, method FROM payments ORDER BY created_at DESC LIMIT 5;
```

Watch the worker: `docker compose logs -f worker`.

The simulated card provider declines about 1 in 10 charges. A cancelled order with a released hold after a decline is correct.

---

## 3. Failure drills

Worth running once so the failure paths are not a surprise.

**SQS down: the order is still accepted and the event waits.**

```bash
docker compose stop floci
# place an order: expect 201
# order_outbox: newest row sent = f, attempts going up, last_error filled
docker compose start floci
# a few seconds later: sent = t
```

Floci keeps queues in memory and recreates them from `floci-init.sh` after a restart. If `last_error` shows `NonExistentQueue`, run `docker compose restart floci`.

**A service down: retries, then the DLQ.**

```bash
docker compose stop payment-service
# place an order: after about 3 minutes (3 tries, 60 s apart) the message is in the DLQ
docker compose exec floci aws sqs get-queue-attributes \
  --queue-url http://localhost:4566/000000000000/order-events-dlq \
  --attribute-names ApproximateNumberOfMessages
```

**DLQ redrive: send stuck messages back once the service is healthy.**

```bash
docker compose start payment-service
docker compose exec floci aws sqs start-message-move-task \
  --source-arn arn:aws:sqs:us-east-1:000000000000:order-events-dlq
# the worker picks the message up within seconds and finishes the order
```

On AWS it is the same API, or "Start DLQ redrive" in the SQS console. See the [DLQ runbook](../sqs/dlq-runbook.md).

---

## 4. Smoke test and unit tests

Both are in [tests](../../tests/README.md): one command that runs a real order through the public URL and prints PASS or FAIL per step, and how to run each service's unit tests.

---

## 5. Build images

Every service uses the same two-stage Dockerfile:

1. **Build** in `golang:1.27-alpine`. `CGO_ENABLED=0` gives a static binary, and `-trimpath -ldflags "-s -w"` keeps it small and reproducible.
2. **Run** on `gcr.io/distroless/static-debian13`, pinned by digest, as user `nonroot`. There is no shell in the final image.

To add a service, copy an existing Dockerfile and `.dockerignore`, then change the labels and the `EXPOSE` port. Do not exclude `static/` in `.dockerignore`: dashboard-api embeds it.

To update the base image digest, in all 9 Dockerfiles:

```bash
docker pull gcr.io/distroless/static-debian13
docker inspect gcr.io/distroless/static-debian13 --format='{{index .RepoDigests 0}}'
# replace the @sha256:... in every Dockerfile, then
docker compose build --no-cache
```

With no shell you cannot `docker exec` into a running service. Check logs and the health endpoint, or run the binary in the builder image.

---

## 6. Who is holding database connections

Each service names its connections (`PGAPPNAME`), so Postgres can show who holds what:

```sql
SELECT application_name, state, count(*) FROM pg_stat_activity
GROUP BY 1, 2 ORDER BY 1;
```

inventory-service also exposes pool metrics (`go_sql_*`) at http://localhost:9092/metrics.

Finding to remember: 600 stock holds on one product, 60 at a time, made about 570 requests wait for a connection. The same load spread over 60 products made none wait. The waits come from requests queueing on one database row, not from too few connections.

---

## 7. When something looks wrong

| You see | Likely cause | Fix |
|---|---|---|
| Dashboard tables empty, or orders fail, on 8086 | The page needs the gateway | Use 8080 |
| Dashboard empty on 8080 after a code change | Old image | `docker compose up -d --build api-gateway dashboard-api` |
| Order stays `pending` | Worker not running, or the queue missing | `docker compose logs worker`, then restart floci and the worker |
| `order_outbox` rows not sent | Floci down, or wrong `SQS_QUEUE_URL` | Check `last_error` |
| 429 responses | Rate limit hit | Wait for `Retry-After`. Raise the limit in compose only for load tests. |
| Order cancelled after about a second | Simulated decline, or not enough stock | Check the worker log line for the order |
| A test says `SKIP` | Its env var is not set | See [tests](../../tests/README.md) |
