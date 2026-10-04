# deploy/

Values for the shared [`charts/microservice`](../charts/microservice) chart. One Helm
release per service; the release name **is** the service name.

```
kubernetes/deploy/
  services/<svc>/values.yaml   # what the service is: ports, env, DB access, resources
  envs/<env>/common.yaml       # env-wide: ECR repo, AWS region, RDS endpoint + secret
  envs/<env>/<svc>.yaml        # env + service: image tag (CI writes it), replicas, env-only config
```

Files are layered in this order (later wins). The ArgoCD ApplicationSet (ALPHA-91) must use
the same order, with paths relative to the chart and the SSH repo URL
(`git@github.com:EKS-V2-Alpha-Team/Alpha-Pilot-4week.git`) so ArgoCD's deploy key matches:

```yaml
helm:
  releaseName: order-service
  valueFiles:
    - ../../deploy/services/order-service/values.yaml
    - ../../deploy/envs/dev/common.yaml
    - ../../deploy/envs/dev/order-service.yaml
```

## Render and lint locally

Lint the chart on its own (uses the test fixture):

```bash
helm lint kubernetes/charts/microservice -f kubernetes/charts/microservice/ci/test-values.yaml
```

Lint and render one service, with its value files in the same order ArgoCD uses:

```bash
helm lint kubernetes/charts/microservice \
  -f kubernetes/deploy/services/order-service/values.yaml \
  -f kubernetes/deploy/envs/dev/common.yaml \
  -f kubernetes/deploy/envs/dev/order-service.yaml

helm template order-service kubernetes/charts/microservice -n order-platform \
  -f kubernetes/deploy/services/order-service/values.yaml \
  -f kubernetes/deploy/envs/dev/common.yaml \
  -f kubernetes/deploy/envs/dev/order-service.yaml
```

Swap `order-service` for any other service folder name.

## What the cluster must provide

| Dependency | Why | Name the chart expects |
|---|---|---|
| External Secrets + `ClusterSecretStore` | `DATABASE_URL`, `JWT_SECRET` | `aws-secrets-manager` |
| RDS (Postgres) | `DATABASE_URL` | Endpoint and RDS-managed secret ARN in `envs/<env>/common.yaml`; SSL required |
| Pod Identity association (order-service, worker) | SQS access | SA = service name, namespace `order-platform` |
| Redis Service | api-gateway rate limiting | `redis` (not installed yet) |
| Traefik + cert-manager `ClusterIssuer` | only when `ingressRoute.enabled` | entrypoint `websecure`, `letsencrypt-staging` / `letsencrypt-prod` |
| Reloader | restart pods when secrets rotate | - |
| Prometheus Operator | ServiceMonitors (skipped if the CRD is missing) | - |

## Adding a service

1. `kubernetes/deploy/services/<svc>/values.yaml`: `component`, `containerPort`, `resources`, and
   `database.enabled: true` if it talks to Postgres.
2. `kubernetes/deploy/envs/dev/<svc>.yaml`: `image.tag` and anything env-specific.
3. `helm lint` and `helm template` (above) must pass for the new service. The schema rejects unknown keys, `latest`
   tags and missing resources.

## Chart opinions (change them in the chart, not per service)

- Non-root (UID 65532), read-only root FS, all capabilities dropped, writable `/tmp`.
- Probes: liveness `/livez`, readiness `/healthz`, startup `/livez` (150s budget).
- `maxUnavailable: 0` rollouts, 10s `preStop` sleep, 50s grace period.
- PDB only when a service runs 2+ pods; HPA owns `replicas` when enabled.
- `singleton: true` (scheduler) forces 1 replica + `Recreate` and fails on HPA.
- ConfigMap changes roll pods via a checksum annotation; secret changes via Reloader.
- `GOMEMLIMIT` set from the memory limit; pod labels exclude `helm.sh/chart`, so a chart version bump doesn't restart every service.
