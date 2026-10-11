# Services

Nine Go services behind `api-gateway`. Each folder has its code, its tests (`*_test.go`) and a Dockerfile.

## Run locally

```bash
docker compose up -d --build
docker compose ps          # everything should say Up
```

Open http://localhost:8080 (not 8086). The page signs itself in as `admin@platform.local`. Add parts to a kit and place the order: it should show `confirmed` within about 5 seconds. How the page is designed and built: [dashboard-api/DESIGN.md](dashboard-api/DESIGN.md).

Local AWS is Floci on port 4566. It creates the `order-events` queue and its DLQ at start. If an order sits at `pending`, run `docker compose restart worker order-service`.

From the terminal:

```bash
TOKEN=$(curl -s -X POST localhost:8080/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"anything"}' | sed -E 's/.*"token":"([^"]+)".*/\1/')

curl -s -X POST localhost:8080/api/orders/ -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -H "Idempotency-Key: $(date +%s%N)" \
  -d '{"items":[{"product_id":"mon-001","quantity":1}]}'
```

The simulated card provider declines about 1 in 10 charges, so an occasional cancelled order is expected.

## Unit tests

```bash
make test                      # every service
make test SERVICE=worker       # one service
```

Tests that need Postgres, Redis or SQS print `SKIP` when their variable isn't set. Give the database tests a throwaway database, never `orders`:

```bash
docker compose up -d postgres redis floci
docker compose exec postgres createdb -U app orders_test       # once
```

| Service | Variables |
|---|---|
| api-gateway | `TEST_REDIS_URL` |
| order-service | `TEST_DATABASE_URL`, `AWS_ENDPOINT_URL` |
| inventory, payment, shipping, notification, scheduler | `TEST_DATABASE_URL` |
| worker | `AWS_ENDPOINT_URL` (real-queue test only) |
| dashboard-api | none |

```bash
cd services/order-service
TEST_DATABASE_URL='postgres://app:localdev@localhost:5432/orders_test?sslmode=disable' \
AWS_ENDPOINT_URL=http://localhost:4566 AWS_REGION=us-east-1 \
AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test \
go test ./... -race -count=1
```

## Smoke test

Runs a real order through the public URL and prints PASS or FAIL per step: health, login, idempotent order, worker confirm, charge, shipping, cancel and refund, internal routes blocked.

```bash
go run tests/smoke/main.go -base http://localhost:8080
go run tests/smoke/main.go -base https://<dashboard-host> -insecure    # EKS dev
```

It creates real orders. Local or dev only, never prod.

## Build an image

```bash
docker build -t <service> ./services/<service>
```

Build in `golang:1.27-alpine` (from ECR Public), run on distroless `static-debian13` as `nonroot`. There's no shell in the final image, so use logs and the health endpoint.

## When something looks wrong

| You see | Fix |
|---|---|
| Dashboard empty or orders fail on 8086 | Use 8080 |
| Dashboard empty on 8080 after a code change | `docker compose up -d --build api-gateway dashboard-api` |
| Order stays `pending` | `docker compose logs worker`, then restart floci and the worker |
| 429 responses | Rate limit hit, wait for `Retry-After` |
| A test says `SKIP` | Set its variable from the table above |