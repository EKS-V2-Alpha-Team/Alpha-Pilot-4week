# Redis

Rate-limit counters for api-gateway. One pod in `order-platform`, a 1Gi encrypted gp3 disk, AOF on, no password. Not deployed yet.

| File | What |
|---|---|
| `statefulset.yaml` | The pod and its disk |
| `service.yaml` | `redis` (what the gateway uses) and `redis-headless` |
| `../apps/redis.yaml` | The ArgoCD app, sync wave -1 |
| `../manifests/storageclass-gp3.yaml` | The gp3 StorageClass, cluster default |
| `../manifests/namespace-order-platform.yaml` | The namespace |

Keep these true:

- The Service is called `redis` and lives in `order-platform`. The gateway's `REDIS_URL` depends on it.
- `--appendonly yes`, `--dir /data` and the disk at `/data` stay together. The root filesystem is read-only.
- `whenDeleted: Delete` stays, or teardown leaves a billed disk behind.
- The image tag stays exact (`redis:7.4.11-alpine`). docker-compose still runs `redis:7-alpine`, not aligned yet.

Why it is built this way and the options we weighed: ADR 0006 in Plane (ALPHA-89).
