# Redis in the cluster

What api-gateway's Redis holds, how it runs on EKS, what happens when it fails, and why it looks like this.

- One Redis pod, `redis-0`, in namespace `order-platform` next to the 9 services. ArgoCD installs it from plain manifests in `kubernetes/platform/redis`.
- Only api-gateway uses it. Each request adds 1 to a counter, and the counter deletes itself after 60 seconds.
- Redis writes every change to a log on a 1Gi encrypted disk, so a restarted pod comes back with its counters.
- If Redis is down or slower than 100 ms, the gateway lets the request through. Orders never wait on Redis.
- There is no password and no network policy yet. See [Known gaps](../known-app-gaps.md).

Stop here if you only need the shape. Below: one request, the failure cases, the rules and the reasons.

---

## 1. One request, end to end

Sara is logged in and sends her 121st request inside one minute.

```
Sara --> Traefik --> api-gateway ------------------------> Service "redis" (port 6379)
                        |  key rl:user:<hash of Sara's ID>         |
                        |  one Lua script: add 1, and start a      v
                        |  60 s expiry if the key is new        redis-0
                        |<------------------ count = 121 ------   appends the write to its log
                        v                                         on /data (1Gi gp3 disk)
                 limit is 120 a minute, so Sara gets
                 429 Too Many Requests, Retry-After = seconds left in the window
```

Her first 120 requests go through. The 121st gets 429. Sixty seconds after her first request the key expires and she starts again from 0.

The gateway finds Redis through `REDIS_URL: redis://redis:6379` in `kubernetes/deploy/services/api-gateway/values.yaml`. The short name `redis` works only inside the same namespace, which is why Redis lives in `order-platform`.

---

## 2. When things go wrong

The gateway never blocks a request because of Redis. Every Redis problem ends the same way for the customer: the request goes through, without a rate limit.

| Situation | What the gateway does | What happens to Redis |
|---|---|---|
| The pod restarts (crash, node drain, Karpenter moving it) | Lets requests through until Redis answers | The new pod mounts the same disk, replays its log, and the counters come back with their time left |
| No node is free in the disk's AZ | Lets requests through | The pod stays Pending. An EBS disk lives in one AZ, so the pod can only run there. Karpenter can add a node in that AZ. |
| Redis takes longer than 100 ms | Lets the request through and counts it as `result="redis_error"` | Nothing |
| `REDIS_URL` is not set | Rate limiting is off, one warning at startup | Not involved |
| `REDIS_URL` is malformed | The gateway refuses to start | Not involved |
| `terraform destroy` while Redis is still running | Not involved | The cluster created the disk, not Terraform, so it is left behind and keeps billing. Delete Redis first. |

---

## 3. Rules that must stay true

1. The Service is called `redis` and lives in `order-platform`. The gateway's URL depends on both.
2. `--appendonly yes`, `--dir /data` and the disk mounted at `/data` stay together. The root filesystem is read-only, so `/data` is the only place Redis can write, and a log on a disk that dies with the pod restores nothing.
3. `persistentVolumeClaimRetentionPolicy.whenDeleted: Delete` stays. Without it, deleting Redis leaves a billed disk behind.
4. One replica. Two separate Redis pods would each hold half the counts, so every limit would double.
5. The image tag stays exact (`redis:7.4.11-alpine`). Never `latest` or a floating `7`.
6. The `gp3` StorageClass and the `order-platform` namespace come from `kubernetes/platform/manifests` (sync wave -2), before Redis (wave -1). Redis's ArgoCD app must not create the namespace.

---

## 4. Why it is built this way

**In the cluster, not ElastiCache.** The brief asks for Redis running in the cluster with persistent storage. ElastiCache would add a Terraform module, TLS, an auth token and CI permissions, and cost about $13 a month for one `cache.t4g.micro` in London. Postgres moved to RDS because losing orders costs money. Redis holds counters that expire in a minute, so a managed service protects nothing we need. [Decision 0005](../adr/0005-redis-in-the-cluster.md).

**A disk the app does not strictly need.** A StatefulSet gives a pod a fixed name (`redis-0`) and its own disk, which follows the pod when it moves. The app would survive losing every counter: the worst case is one minute of fresh allowances. We keep the disk because the brief asks for persistence and it costs cents. AOF (append-only file) is Redis's write log, replayed after a restart; it helps only if the disk survives, so the two come as a pair.

**1Gi, not 10Gi.** The counters expire after 60 seconds, so the data is tiny. Redis compacts its log by writing the new file before deleting the old one, so it needs room for two copies, and 1Gi is far more than that. At the London gp3 rate of $0.0928 per GB a month ([EBS pricing](https://aws.amazon.com/ebs/pricing/)), 1Gi costs about $0.09 a month.

**Redis, not Valkey.** Valkey is a fork that speaks the same protocol. docker-compose runs Redis, and the gateway's Lua script and tests have only run against Redis. Redis 7.4 is licensed RSALv2 or SSPLv1, not BSD ([Redis licences](https://redis.io/legal/licenses/)); we run it inside our own platform, not as a service for others. If that is ever challenged, Valkey is the swap, after running the gateway tests against it.

**Plain manifests, not a chart.** The other platform tools use an upstream chart plus our values. For Redis we rendered three charts and none fit: Bitnami's default image no longer gets tags on Docker Hub, groundhog2k defaults to a Deployment and adds a 150-line init script, and CloudPirates is pre-1.0 and renders almost the same as our files. Ours is about 60 lines in two files with no third-party dependency.

**gp3 is the cluster default.** On EKS 1.30 and later AWS no longer marks gp2 as the default StorageClass on new clusters, so a claim that names no class would wait forever. gp3 is also cheaper ($0.0928 per GB a month in London, against $0.116 for gp2).

**Probes run `redis-cli ping`.** A port check passes while Redis is hung but still listening. Ping passes only if Redis answers a command.

**The namespace has its own manifest.** If Redis's ArgoCD app created `order-platform`, Redis would become the accidental owner of the namespace all 9 services share.

**Karpenter may move it.** A node that can never be removed would cost more than the short gap while Redis restarts on the same disk.

---

## 5. What we accepted

- A restart is a short window with no rate limiting. Reschedule time is not measured.
- The disk lives in one AZ. If that AZ has no free node, or is down, Redis waits and the gateway lets requests through.
- No snapshot or restore. It needs the EBS snapshot controller and a VolumeSnapshotClass ([EKS docs](https://docs.aws.amazon.com/eks/latest/userguide/csi-snapshot-controller.html)).
- No password, no TLS, no network policy. Any pod in the cluster can reach Redis, which holds hashed IDs and counters, no card data.
- docker-compose runs a floating `redis:7-alpine`, so laptop and cluster versions can differ.

---

## 6. Check it after a deploy

```bash
kubectl get sc                                                           # gp3 (default), nothing else marked default
kubectl -n order-platform get pods,pvc -l app.kubernetes.io/name=redis   # redis-0 Running, data-redis-0 Bound
kubectl -n order-platform exec redis-0 -- redis-cli config get appendonly   # yes
kubectl -n order-platform logs deploy/api-gateway | grep "Redis connected"
```

To test the disk: set a key with a long expiry, `kubectl -n order-platform delete pod redis-0`, wait for it to come back, and read the key again.

### What is in Redis

| Key | Counts | Limit per 60 s |
|---|---|---|
| `rl:user:<hash>` | requests from one logged-in user | 120 |
| `rl:ip:<hash>` | requests from one IP when not logged in | 60 |
| `rl:sensitive:<hash>` | login, register and webhook calls from one IP | 20 |
| `rl:badtoken:<hash>` | missing or bad tokens from one IP | 20 |

### Where the code lives

| What | Where |
|---|---|
| The pod and its disk | `kubernetes/platform/redis/statefulset.yaml` |
| The two Services (`redis`, `redis-headless`) | `kubernetes/platform/redis/service.yaml` |
| The ArgoCD app (wave -1) | `kubernetes/platform/apps/redis.yaml` |
| gp3 StorageClass and the namespace (wave -2) | `kubernetes/platform/manifests/storageclass-gp3.yaml`, `namespace-order-platform.yaml` |
| The limiter | `services/api-gateway/main.go` |
