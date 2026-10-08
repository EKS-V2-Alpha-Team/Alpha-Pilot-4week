# Tests

Two kinds, in two places.

| Kind | Where | What it proves |
|---|---|---|
| Unit and integration tests | Beside the code: `services/<service>/*_test.go` | One service does its job, including races and retries, against a real Postgres, Redis or SQS API |
| Smoke test | [`tests/smoke`](smoke/main.go) | The whole platform works end to end through the public URL |

Unit tests stay in each service folder because the tests are written in the same Go package as the code (they call its private functions). Go only allows that from the same directory, so moving them here would mean rewriting them to use the public API only, and they would stop testing the internals.

## Run everything

```bash
make test                      # every service, plain `go test ./...`
make test SERVICE=worker       # one service
```

Tests that need a database, Redis or SQS print `SKIP` when it is not set up. A run with skips is not a full run: read the output. CI runs `go test -race` for each changed service without those, so the database and Redis tests protect `main` only when someone runs them as below.

## What each service needs

The database tests wipe their tables. Give them a throwaway database, never `orders`:

```bash
docker compose up -d postgres redis floci
docker compose exec postgres createdb -U app orders_test       # once
```

| Service | Needs | Variables |
|---|---|---|
| api-gateway | Redis, for the limiter tests. Routing, allow-list and client IP tests need nothing. | `TEST_REDIS_URL` |
| order-service | Postgres, plus SQS for the publisher tests | `TEST_DATABASE_URL`, `AWS_ENDPOINT_URL` |
| inventory-service, payment-service, shipping-service, notification-service, scheduler | Postgres | `TEST_DATABASE_URL` |
| worker | Nothing for the handler tests. SQS for the real-queue test. | `AWS_ENDPOINT_URL` |
| dashboard-api | Nothing | none |

Example, for one service with everything it can use:

```bash
cd services/order-service
TEST_DATABASE_URL='postgres://app:localdev@localhost:5432/orders_test?sslmode=disable' \
AWS_ENDPOINT_URL=http://localhost:4566 AWS_REGION=us-east-1 \
AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test \
go test ./... -race -count=1 -v
```

api-gateway:

```bash
cd services/api-gateway
TEST_REDIS_URL='redis://localhost:6379/15' go test ./... -race -count=1
```

No Go on your machine? Run the tests in the same image the Dockerfile builds with (`golang:1.27-alpine`) on the compose network.

## Smoke test

Runs a real order through the public URL and prints PASS or FAIL per step:

1. health of every service;
2. login;
3. place an order with an `Idempotency-Key`, then send the same key again and expect the same order back;
4. the worker confirms it, and stock is taken;
5. charge and confirmation email;
6. ship it to delivered, with its emails;
7. place a second order and cancel it: refund and stock back;
8. internal routes (charge, refund, reserve) answer 404 from outside.

```bash
go run tests/smoke/main.go -base http://localhost:8080

# on EKS dev (the certificate is a staging one, hence -insecure)
go run tests/smoke/main.go -base https://<dashboard-host> -insecure
```

It creates two real orders, so run it against local or dev only, never prod. Someone ordering the same product during the run can make a stock step fail; run it again. The exit code is 1 if any step failed. Login accepts any credentials today, so `-email` and `-password` only matter once real login exists.

## Two things learned the hard way

- A concurrency test proves nothing on a cold connection pool. With one connection open, the first request finished before the others started, and the buggy code passed. The inventory tests open 20 connections first.
- Each test must close its pool (`t.Cleanup(db.Close)`). Otherwise Postgres runs out of connection slots after about 10 runs.

## A new test

Put it next to the code it tests. Make it fail first: break the code (remove the unique index, the status guard, the header check) and see the test go red before you trust it green.
