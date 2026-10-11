# Load tests (dev)

One script (`load.js`), run with k6. Each test is picked with `-e TEST=<name>`. They create real orders in **dev only**; the script refuses any other address.

## What you need (once)

1. Install k6 for Linux (https://grafana.com/docs/k6/latest/set-up/install-k6/), then run `k6 version`.
2. Be logged in to AWS (the same profile you use for Terraform) and connected to the dev cluster (`kubectl get pods -n order-platform` works).

## Words you need

- **Requests per second (RPS):** how many requests k6 sends each second. k6 keeps sending at that pace even if the app slows down, which is what real shoppers do.
- **p95 / p99:** 95 (or 99) out of 100 requests were faster than this. A slow tail hides in the average, so we judge on p95/p99.
- **Pass / fail:** at the end k6 prints a green tick or a red cross next to each threshold. Any red cross means the test failed. The exit code is also non-zero.

## Every run

Run everything from the repo root.

```bash
# 1. Get the dev secret (stays in this shell only, never commit it)
export JWT_SECRET=$(aws secretsmanager get-secret-value --secret-id eks-v2-dev-jwt-secret --query SecretString --output text --region eu-west-2 --no-cli-pager)

# 2. Run one test. RUN_ID: letters and digits only, new for every run (e.g. l1a, l1b, l2a)
k6 run -e TEST=preflight -e RUN_ID=pre1 --insecure-skip-tls-verify tests/load/load.js

# 3. After it finishes, wait 2 minutes for the queue to drain, then check the data
kubectl exec -i -n order-platform postgres-0 -- sh -c 'psql -U $POSTGRES_USER -d orders -v run=pre1 -v ON_ERROR_STOP=1' < tests/load/checks.sql
```

`--insecure-skip-tls-verify` is needed because dev uses a staging certificate.

## The tests, in the order to run them

| Order | `TEST=` | What it does | Time | Pass means |
|---|---|---|---|---|
| 1 | `preflight` | One order end to end, plus a retry with the same key. Run this first, and any time something looks odd. | 1 min | All ticks green |
| 2 | `l1` | 15 RPS, a normal day (1,000 users). Run it twice. | 8 min | Reads p95 < 300 ms, checkout p95 < 500 ms, under 0.1% errors |
| 3 | `l2` | 150 RPS, a peak hour (Black Friday). Run it three times. | 8 min | Reads p95 < 500 ms, checkout p95 < 1 s, under 0.5% errors |
| 4 | `hot` | Same as `l2`, but 30% of shoppers go for one product (the doorbuster). | 8 min | Same as `l2`, and no oversold stock |
| 5 | `retry` | 10 customers each send the same order 50 times at once. | 1 min | Exactly one order each |
| 6 | `ratelimit` | One user and one IP hammer the gateway. | 1 min | 429 appears, Retry-After is set, other users unaffected |
| 7 | `spike` | Jump from 15 to 150 RPS in 30 s, hold, drop, then check recovery. | 6 min | Normal speed returns within 2 min of the spike ending |
| 8 | `dashboard` | `l1` plus 3 staff dashboard tabs open (`-e TABS=4` for four, the maximum). | 8 min | Shoppers still inside L1 limits, dashboard summary p95 < 1 s |
| 9 | `soak` | 15 RPS for 2 hours (`-e HOLD=30m` for a shorter one). | 2 h | Same as `l1`, no memory growth, no restarts |

Change length with `-e HOLD=10m`. Change speed with `-e RATE=50`. Do not run two tests at once.

## After every load run (this is the real pass or fail)

k6 green is not enough. Orders finish later, in the background.

1. **`checks.sql`** (command above). Sections 5, 6, 7 and 8 must print **no rows**. Section 3 (pending) must be **0**. Section 1 must equal `orders_created` from the k6 summary (a lower number means orders were lost). Section 4 shows how long orders took to confirm: write it down.
2. **Dead-letter queue empty** (messages the worker gave up on):
   `aws sqs get-queue-attributes --queue-url https://sqs.eu-west-2.amazonaws.com/751501311900/eks-v2-dev-order-events-dlq --attribute-names ApproximateNumberOfMessages --region eu-west-2 --no-cli-pager`
3. **Queue drained** (should return to 0 within a few minutes):
   `aws sqs get-queue-attributes --queue-url https://sqs.eu-west-2.amazonaws.com/751501311900/eks-v2-dev-order-events --attribute-names ApproximateNumberOfMessages --region eu-west-2 --no-cli-pager`
4. **Nothing crashed:** `kubectl get pods -n order-platform`. Restarts above 0, or `OOMKilled` under `kubectl describe pod`, is a finding.

During the hold of a run, in a second window, run `db.sql` the same way as `checks.sql` (drop `-v run=...`). It shows database connections and anything waiting on a lock.

## 1 replica vs 2 replicas

ArgoCD undoes manual scaling, so do it through Git: set `replicaCount: 2` in `kubernetes/deploy/envs/dev/` for `api-gateway`, `order-service`, `inventory-service` **and `worker`**, merge, wait for ArgoCD to sync, then repeat `l2` three times. Leave `scheduler` at 1. Revert with a second PR when done. Tell the team before you start.

## Break something on purpose (optional)

Start `l1` with `-e HOLD=10m`, then in another window: `kubectl delete pod -n order-platform -l app.kubernetes.io/name=worker`. Repeat with `order-service`. Expect a short blip, then recovery, and `checks.sql` still clean. Do not delete the Postgres pod without telling the team first.

## Notes

- Test data is prefixed `lt-` (products named "[load-test] ..."). It stays in dev; it does not affect real data. Each run only reads its own orders, so use a new `RUN_ID` every time.
- Run from a stable wired connection. If k6 reports `dropped_iterations`, the machine or network could not keep up, so the result is not valid. Lower `-e RATE` and say so.
- Costs: no new AWS resources. Short runs only add a little transfer and CPU on the existing nodes.
- Not covered (documented, needs more budget): multi-AZ failover, 10k to 100k users, separate load generator in the cluster.
