# Gateway and dashboard

How requests get into the app, who is let in, and how they are limited.

- Everything goes through api-gateway on port 8080, the dashboard page included. The page calls `/api` and `/auth` on whatever address served it, so opened on 8086 its calls get a 404.
- The gateway forwards only the routes meant for the outside world. Everything else (charge, refund, reserve, restock, send email, create shipment) gets a 404 and never reaches the service.
- Limits per 60 seconds: 120 per logged-in user, 60 per IP when not logged in, 20 per IP on login, register, the shipping webhook and bad tokens. Over the limit you get a 429 and a `Retry-After` header.
- If Redis is down, requests are let through. The gateway logs it and counts it.
- Login is open today: any email and password gets a token. See [Known gaps](../known-app-gaps.md).

---

## 1. Where requests go

| Request path | Goes to | Notes |
|---|---|---|
| `/` | dashboard-api | The dashboard page. No token needed for the page itself. |
| `/dashboard/...` | dashboard-api | Path kept as is. |
| `/api/orders/...` | order-service | The `/api/orders` part is removed before forwarding. Same for every `/api/...` route. |
| `/api/inventory/...` | inventory-service | |
| `/api/payments/...` | payment-service | |
| `/api/shipping/...` | shipping-service | `/api/shipping/webhook` needs no token. |
| `/api/notifications/...` | notification-service | |
| `/api/dashboard/...` | dashboard-api | |
| `/auth/login`, `/auth/register` | the gateway itself | Return a token valid for 24 hours. Limited to 20 a minute per IP. |
| `/healthz`, `/api/<service>/healthz` | gateway / each service | No token needed. |

Every other request needs `Authorization: Bearer <token>`.

---

## 2. Who gets in

**Routes the outside world can use.** Everything else is a 404 at the gateway (`services/api-gateway/allowlist.go`):

| Service | Allowed |
|---|---|
| orders | `GET /`, `POST /`, `GET /<id>`, `PUT /status` (the dashboard moves and cancels orders) |
| inventory | `GET /products`, `POST /products`, `GET /products/<id>`, `GET /low-stock` |
| payments | `GET /ledger`, `GET /balance/<id>` |
| notifications | `GET /history`, `GET /templates` |
| shipping | `GET /shipments`, `GET /shipments/<id>`, `GET /track/<n>`, `POST /webhook` |
| all | `GET /healthz` |

Paths with `.`, `..` or empty segments are never forwarded. The worker and scheduler call services directly inside the cluster, so they are not affected.

**Who the services think is calling.** The gateway sets `X-User-Email` and `X-User-Role` from the checked token, replacing anything the client sent.

**Bad tokens are counted.** A missing or invalid token on a protected route counts against the client's IP, and after 20 a minute that IP gets 429. Good tokens are never counted, so an attacker cannot lock out real users who share the IP.

---

## 3. How the limiter decides who you are

| Who | Counted by | Limit per 60 s | Applies to |
|---|---|---|---|
| Logged-in user | User ID in the token (`sub`) | 120 | All normal API calls |
| Not logged in | Client IP | 60 | Public paths such as health checks |
| Sensitive paths | Client IP | 20 | `/auth/login`, `/auth/register`, `/api/shipping/webhook`, and requests with a missing or bad token |

- The client IP is the address that connected, without the port. `X-Forwarded-For` and `X-Real-IP` are believed only when the request comes from a proxy in `TRUSTED_PROXY_CIDRS`. Locally that list is empty, so nobody can fake their IP.
- From a trusted proxy, the gateway keeps the incoming `X-Forwarded-For` chain and adds its own hop. From anyone else it drops the header. `X-Real-IP` is always set by the gateway to the real client IP, so a service that wants one trustworthy value reads that.
- Counters live in Redis under keys like `rl:user:<hash>`. IDs are hashed, so Redis holds no emails or IPs.
- Counting and setting the expiry happen in one Redis step, so a crash cannot leave a counter that never resets.
- If Redis does not answer within 100 ms, the request is allowed (fail open). The gateway logs this at most once every 10 seconds.

Watch it with `gateway_rate_limit_decisions_total{scope, result}` on port 9090 inside the container (9091 on your laptop). That port is never routed through the gateway.

---

## 4. Traefik and the client IP

On EKS, Traefik runs in front of the gateway, behind an NLB.

- The NLB keeps the client's IP (`preserve_client_ip.enabled=true` in `kubernetes/helm-values/traefik-values.yaml`; AWS turns this off by default for IP targets). Traefik adds it to `X-Forwarded-For`.
- `TRUSTED_PROXY_CIDRS` is the VPC range, so the gateway believes that header only from Traefik's pods. A client outside the VPC cannot fake its IP.
- Traefik does not rate-limit. Limits live in the gateway only, in Redis, shared across its pods.

Every per-IP limit depends on the client IP really reaching the gateway. If it did not, all clients would share one bucket and normal traffic would be blocked. Check it after each new NLB:

```bash
# 1. the target group keeps the client IP: expect true
TG=$(aws elbv2 describe-target-groups --query "TargetGroups[?contains(TargetGroupName, 'traefik')].TargetGroupArn" --output text)
for t in $TG; do aws elbv2 describe-target-group-attributes --target-group-arn "$t" \
  --query "Attributes[?Key=='preserve_client_ip.enabled'].Value" --output text; done

# 2. the gateway counts you under your own public IP: one key means your IP was seen
curl -sk -X POST https://<dashboard-host>/auth/login -d '{}' >/dev/null
H=$(printf '%s' "$(curl -s https://checkip.amazonaws.com)" | sha256sum | cut -c1-32)
kubectl -n order-platform exec redis-0 -- redis-cli --scan --pattern "rl:sensitive:$H"
```

---

## 5. How the dashboard behaves

1. The page loads from `/` on 8080 and shows only the login box.
2. The user logs in. The token is kept in the page's memory only, never in browser storage, so it is gone when the tab closes.
3. The page loads its tables, then refreshes every 30 seconds.
4. When the token expires (24 hours) the next refresh gets a 401 and the login box comes back.
5. Placing an order sends an `Idempotency-Key`.
6. Everything that comes back from the API is escaped before it is shown, so HTML in a product name or note appears as text instead of running.

Why 30 seconds: one refresh makes 8 data calls. At 10 seconds that is 48 calls a minute per tab, and three tabs logged in as the same user (144) go over the 120 limit they share. At 30 seconds one tab makes 16 a minute.

---

## 6. Rules that must stay true

1. The gateway is the only way in. On EKS nothing else is exposed.
2. Forwarding headers are trusted only from known proxies.
3. Login, register, the webhook and bad tokens keep the stricter per-IP limit.
4. The metrics port is never routed through the gateway.
5. The dashboard loads data only after it has a token.
6. A route is reachable from outside only if it is in `allowlist.go`. Adding a public endpoint means adding it there.
7. Only the gateway sets `X-User-Email` and `X-User-Role`.
8. No password, secret or key is written in the code or the Helm values. They come from Secrets Manager through External Secrets.

---

## 7. Why it is built this way

**What was wrong before.** The old limiter counted by IP and port. Every new connection has a new port, so every connection got a fresh allowance and nothing was ever limited: 150 separate requests all returned 200.

| Question | Options | Picked | Why |
|---|---|---|---|
| Where do limits live? | Gateway only, or also Traefik | Gateway only | Per-user limits need the token, which only the gateway reads. A Traefik limit counts per pod in memory and adds a second set of numbers. |
| How to count | Fixed window, or token bucket | Fixed window | Simple, and enough for this traffic |
| Redis down | Let requests through, or block | Let through | A Redis blip should not take the whole shop down |

Full reasoning: [decision 0003](../adr/0003-gateway-rate-limiting.md).

**Dashboard.** It must be served from the same address as the API because its calls use paths like `/api/orders` with no host. Serving it from the gateway fixes that with one route.

---

## 8. Settings

| Variable | Default | What it does |
|---|---|---|
| `RATE_LIMIT_WINDOW_SECONDS` | 60 | Window length |
| `RATE_LIMIT_USER_PER_WINDOW` | 120 | Per logged-in user |
| `RATE_LIMIT_IP_PER_WINDOW` | 60 | Per IP, not logged in |
| `RATE_LIMIT_SENSITIVE_PER_WINDOW` | 20 | Per IP on login, register, webhook, bad tokens |
| `TRUSTED_PROXY_CIDRS` | empty | Proxies whose forwarding headers are believed |
| `METRICS_PORT` | 9090 | Prometheus metrics |
| `REDIS_URL` | none | Redis for the counters. Unset: rate limiting off, one warning at startup. Malformed: the gateway refuses to start. |
| `JWT_SECRET` | none | Signs tokens. On EKS it comes from Secrets Manager. |
| `DASHBOARD_SERVICE_URL` | `http://dashboard-api:8086` | Where the page and `/dashboard/` go |

---

## 9. Check it yourself

- Run the gateway tests ([tests](../../tests/README.md)). They cover the limiter against real Redis, routing, the allow-list, client IP handling and bad-token counting.
- Send 22 logins in a row from one client: 20 succeed and the rest get 429.
- Call a closed route, for example `POST /api/payments/charge` with a valid token: expect 404.
