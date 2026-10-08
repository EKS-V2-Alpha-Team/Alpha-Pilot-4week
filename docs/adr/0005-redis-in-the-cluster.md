# 0005: Run Redis in the cluster, with a small disk

**Status:** Accepted.
**Decision in one line:** the gateway's rate-limit counters live in one Redis pod inside the cluster, run as a StatefulSet with a 1Gi encrypted gp3 disk and its write log (AOF) on.

## Context

**What Redis does here.** Only api-gateway uses it. Each request adds 1 to a counter per user or IP, and the counter expires after 60 seconds. If Redis does not answer within 100 ms, the request goes through ([0003](0003-gateway-rate-limiting.md)). Losing everything in Redis costs one minute of fresh allowances and nothing else.

**What the client asked for.** The project brief: "PostgreSQL and Redis running in the cluster with persistent storage". The graded checklist: Redis as a StatefulSet with AOF on an encrypted gp3 volume that survives pod restarts.

**Constraints.** AWS spend is capped at $200 and cost is graded first. Zero loss of order and payment data. The CI role has no `elasticache:*` permission.

**Existing pattern.** Postgres runs on RDS because losing orders and payments costs money. Platform tools install through ArgoCD, one Application each. docker-compose runs Redis locally.

## Options

| Option | What it is | Cost | For | Against |
|---|---|---|---|---|
| **A. StatefulSet with a small disk and AOF (chosen)** | One Redis pod with its own encrypted disk and write log | About $0.09 a month for 1Gi of gp3 (London, $0.0928 per GB a month) | Meets the client's wording. No new AWS permissions: the EBS CSI driver is already installed. | The app does not need the disk. The disk ties the pod to one AZ. One more thing to clean up before `terraform destroy`. |
| B. Deployment with no disk | The same pod, keeping nothing across restarts | Nothing | Exactly what the app needs. Least to build. Can run in any AZ. | Departs from "persistent storage" in the brief. |
| C. ElastiCache | AWS runs Redis outside the cluster | About $13 a month for one `cache.t4g.micro` in London | Nothing to patch. Terraform owns it, so destroy removes it. | New module, subnet group, TLS, auth token and CI permissions. Outside the client's wording. The app uses nothing it adds. |

The cost gap is about $13 a month against a $200 cap. It did not decide this on its own.

## Decision

**A.** In order of weight:

1. **The client wrote it down.** The brief and the checklist both ask for Redis in the cluster with persistent storage. A is the only option that meets the wording without going back to the client.
2. **Managed Redis protects nothing here.** Postgres went to RDS because its data is worth money. Redis holds a tally that expires in 60 seconds, so replicas, backups and patching guard nothing we care about.
3. **It is cheap to meet.** A StorageClass, a volume template and two Redis flags. The disk costs cents a month.
4. **AOF and the disk come as a pair.** A log on a disk that dies with the pod restores nothing, so both are on or both are off. The app would be fine with both off (B). The written requirement tips it to both on.

The smaller choices (Redis not Valkey, the exact image tag, 1Gi, the namespace manifest, plain YAML instead of a chart) are in [Redis in the cluster](../redis/redis-in-the-cluster.md).

## Consequences

- **A restart is a short gap with no rate limiting.** Orders carry on.
- **The disk ties Redis to one AZ.** If no node is free there, the pod waits until Karpenter adds one.
- **Terraform does not know about the disk.** `whenDeleted: Delete` removes it only when the StatefulSet is deleted, so teardown must delete Redis before `terraform destroy`.
- **1Gi, not 10Gi.** We meet the intent (persistent, encrypted, gp3), not the number. Matching it would cost about $0.84 a month more.
- **No snapshot or restore.** The checklist line "snapshot taken and restored" stays unmet until the EBS snapshot controller is added.
- **No password, TLS or network policy** ([Known gaps](../known-app-gaps.md)).
- **This depends on the gateway failing open.** If a route ever starts blocking requests when Redis is down, a single pod becomes a weak point.

## How we know it works

On a laptop with Docker: the container runs as uid 999 with a read-only root filesystem and all capabilities dropped, a key set with a 300 second expiry survives a restart and comes back with 299 seconds left, and `kubeconform -strict` accepts all the manifests. In the code, any Redis error or a reply slower than 100 ms lets the request through. After a deploy: the gateway logs "Redis connected for rate limiting", `gateway_rate_limit_decisions_total` shows both `allowed` and `limited`, and a counter is still there after `kubectl delete pod redis-0`.

## Revisit when

- A route changes from failing open to failing closed. Look at C.
- Per-IP limits move to Traefik.
- Traffic grows towards the 10k-user scenario. A replica or a managed cache comes next.
- The client confirms Redis needs no persistent storage. Drop to B.

## Sources

[Redis persistence](https://redis.io/docs/latest/operate/oss_and_stack/management/persistence/), [Kubernetes StatefulSets](https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/), [AWS EBS pricing](https://aws.amazon.com/ebs/pricing/), [ElastiCache pricing](https://aws.amazon.com/elasticache/pricing/), [EKS snapshot controller](https://docs.aws.amazon.com/eks/latest/userguide/csi-snapshot-controller.html).
