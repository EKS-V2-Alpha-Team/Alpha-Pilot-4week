# 0003: Rate limiting lives in the gateway

**Status:** Accepted.
**Decision in one line:** api-gateway limits requests per logged-in user and per real client IP, with a fixed 60 second window in Redis, and lets requests through if Redis is down.

## Context

The platform has to survive bursts (evenings, weekends, Black Friday). The limiter the gateway started with did not work:

- It counted by IP and port. Every new connection gets a new port, so every connection got a fresh allowance. 150 separate requests all returned 200.
- It believed `X-Forwarded-For` from anyone, so a client could claim any IP.
- Login and register were not limited at all.
- Counting and expiry were two separate Redis calls. A crash between them left a counter that never reset.
- If Redis was down when the gateway started, limiting stayed off until a restart.

Rate limiting was a "nice to have" as long as the app works, so the fix had to be small.

## Options

| Question | Options | Chosen | Why |
|---|---|---|---|
| Where | Gateway only; also Traefik at the edge | Gateway only | Per-user limits need the token, and only the gateway reads it. A Traefik limit counts per pod in memory (2 pods, so about double the number), adds a second set of numbers, and a Redis-backed one would make a Redis problem hit the edge too. |
| How to count | Fixed window; token bucket | Fixed window | Simple, and enough for this traffic. |
| Redis down | Let requests through; block them | Let them through, with logs and a metric | A Redis blip should not stop all orders. |

## Decision

- **Who you are:** logged in, counted by the user ID in the token (120 per window). Not logged in, counted by IP (60). Login, register, the shipping webhook, and any request with a missing or bad token, counted by IP (20).
- **Real client IP:** the connecting address without the port. Forwarding headers are believed only from `TRUSTED_PROXY_CIDRS`. On EKS the NLB keeps the client IP and Traefik passes it on; the trusted range is the VPC.
- **Bad tokens:** only failures are counted, so real users who share an IP are never blocked by someone else's bad tokens.
- **One Redis step:** a small Lua script counts and sets the expiry together. IDs are hashed, so Redis holds no emails or IPs.
- **Over the limit:** 429 with `Retry-After`.
- **Redis slow or down:** after 100 ms the request goes through. Logs are throttled to one line every 10 seconds, and `gateway_rate_limit_decisions_total` counts every decision.
- **Settings:** every limit and the trusted proxy list come from environment variables.

## Consequences

- A fixed window allows up to double the limit in a burst that straddles the window edge.
- Many shoppers behind one office or mobile carrier IP share the login limit of 20. Watch the "limited" count before changing it.
- Dashboard tabs logged in as the same user share one bucket, so the dashboard refreshes every 30 seconds, not every 10.
- Load tests from one machine need a higher login limit in the test environment. Do not turn the limiter off.
- The gateway is the only place limits live. If a second public entry point appears that does not go through it, that entry point is unlimited.

## How we know it works

Limiter tests run against a real Redis with the race detector, one or more per requirement, and the client IP tests cover a forged `X-Forwarded-For` and bad-token counting. Live check: 22 logins in a row from one client give 20 successes and 2 blocks (429); before the fix all 22 succeeded. Run them: [tests](../../tests/README.md).

## Revisit when

- The gateway itself struggles under load. Then add an edge limit at Traefik for the crude cases.
- A second public entry point appears.
- Staff accounts exist, so each person gets their own limit.

## Sources

[NLB client IP preservation](https://docs.aws.amazon.com/elasticloadbalancing/latest/network/edit-target-group-attributes.html), [AWS Well-Architected REL05-BP02, throttle requests](https://docs.aws.amazon.com/wellarchitected/latest/reliability-pillar/rel_mitigate_interaction_failure_throttle_requests.html).
