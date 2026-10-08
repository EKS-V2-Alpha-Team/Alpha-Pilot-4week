# Known gaps

What is not built, what that means today, and what fixing it takes. Each gap was looked at and left on purpose. The reason is next to it.

Highest risk first: the open login and the unsigned webhook, the top two rows under Security.

## Security

| Gap and what it means today | Why it is left | What fixing takes |
|---|---|---|
| **Login accepts any email and password, and sign-up is open.** Anyone who reaches the gateway can get a token and use the dashboard. | A real fix needs either a user table (hashed passwords, sign-up, ownership checks) or one admin account with a secret. Neither fit alongside the order flow work. | One admin account: password from a GitHub secret through Terraform into Secrets Manager and an ExternalSecret, checked in constant time, sign-up closed. Customer accounts: a user table with Argon2id hashes, then filter orders and payments by the caller. |
| **The carrier webhook is not signed.** Anyone can call `POST /api/shipping/webhook`. They can only set `in_transit` or `delivered` on a real tracking number, a shipment never moves backwards, and the call is limited to 20 a minute per IP. | A signature needs a shared secret in Secrets Manager and an ExternalSecret first. | The carrier signs the body with HMAC-SHA256 and sends it in a header; the service rejects a missing or wrong signature. |
| **No customer accounts, so no "see only your orders".** Every logged-in user sees every order. | There is only the admin dashboard, and login is open (above). | Comes with customer accounts. |
| **Every service connects to Postgres as the same user.** A bug or leak in one service can read or change every table. | The tables split cleanly by service, but a role and secret per service must be created on every database. | One Postgres role per service with grants for its own tables. Start with dashboard-api read-only. |
| **Redis has no password and no network policy.** Any pod in the cluster can connect. It holds hashed IDs and counters, no card data. | Counters expire in a minute. A password needs a secret and a restart path; a policy needs the VPC CNI network policy agent turned on. | Password from Secrets Manager through an ExternalSecret, plus a NetworkPolicy allowing only api-gateway on port 6379. |
| **No browser security headers.** The page escapes everything it shows from the API, which stops injected HTML, but a Content-Security-Policy would add a second layer. | The page still has inline scripts, so a strict policy needs its code moved to a separate file first. | Move the script out, then add `Content-Security-Policy`, `X-Frame-Options`, `X-Content-Type-Options` and `Referrer-Policy` in the gateway. |
| **No `Strict-Transport-Security`, and Redis traffic is not encrypted.** | Dev uses a staging certificate, and HSTS would lock browsers out until a real one is in place. Redis is reachable inside the cluster only. | HSTS at Traefik with production certificates. Redis TLS plus client config in the gateway. |
| **Rate limits live in the gateway only, not at Traefik.** | A Traefik limit counts per pod in memory (about double the number with 2 pods) and adds a second set of numbers. The gateway already limits on the real client IP. | A Traefik middleware, if the gateway ever struggles under load ([decision](adr/0003-gateway-rate-limiting.md)). |

## Data

| Gap and what it means today | Why it is left | What fixing takes |
|---|---|---|
| **Every shipment has a placeholder address.** Orders do not collect a shipping address. | The platform works end to end without it. | `shipping_address` on `POST /orders` (validated, saved, included in the idempotency hash), passed to shipping-service. |
| **Shipments keep a customer's name, address and tracking steps forever.** The scheduler cleans old events, outbox rows, keys and notifications, but not shipments or orders. | Needs a rule for what a "finished" shipment is. | A retention rule that clears personal fields on finished shipments after a set age. |
| **Money is added up as floats in Go.** Postgres stores exact `DECIMAL`, and each total is rounded to the penny before saving. | It touches every service and the ledger. | Integer pence in Go. |
| **Customer email on orders is whoever is logged in.** | Comes with customer accounts. | Customer accounts. |

## Operations

| Gap and what it means today | Why it is left | What fixing takes |
|---|---|---|
| **The DLQ alarm has nobody to notify.** It shows in the CloudWatch console only. | The notification target (an SNS topic) is an environment decision. | Set `dlq_alarm_actions` per environment in the Terraform variables. |
| **DLQ redrive is manual.** | Redriving blindly could repeat a failure. A person should fix the cause first. | Follow the [DLQ runbook](sqs/dlq-runbook.md). |
| **Payment and shipment events are only logged, not sent to SQS.** | Nothing consumes them, so nothing can be lost. | Use the outbox ([decision](adr/0001-order-events-outbox.md)) the day something consumes them. |
| **Events are not de-duplicated by id.** | Every step is safe to repeat, so a duplicate changes nothing. | A processed-events table, if a step ever stops being safe to repeat. |
| **A batch of 10 messages can overrun the 60 s visibility timeout.** SQS may then hand a message out twice. | Safe, because every step is safe to repeat. | The worker extends the timeout while it works (`ChangeMessageVisibility`), if load tests show overruns. |
| **A fixed rate-limit window allows up to double the limit across the window edge.** | Simple, and enough for this traffic. | A sliding window or token bucket. |
