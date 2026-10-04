# deploy/

Values for the shared [`charts/microservice`](../charts/microservice) chart. One Helm
release per service; the release name **is** the service name.

```
deploy/
  services/<svc>/values.yaml   # what the service is: ports, env, DB access, resources
  envs/<env>/common.yaml       # env-wide: ECR repo, AWS region, DB secret name
  envs/<env>/<svc>.yaml        # env + service: image tag (CI writes it), replicas, env-only config
```

Files are layered in this order (later wins). ArgoCD Applications must use the same order:

```yaml
helm:
  valueFiles:
    - ../../deploy/services/order-service/values.yaml
    - ../../deploy/envs/dev/common.yaml
    - ../../deploy/envs/dev/order-service.yaml
```

## Render and lint locally

```bash
scripts/helm-render.sh dev              # lint + render every service
scripts/helm-render.sh dev /tmp/out     # also write manifests to /tmp/out/<svc>.yaml
```

One service:

```bash
helm template order-service charts/microservice -n order-platform \
  -f deploy/services/order-service/values.yaml \
  -f deploy/envs/dev/common.yaml \
  -f deploy/envs/dev/order-service.yaml
```

## What the cluster must provide

| Dependency | Why | Name the chart expects |
|---|---|---|
| External Secrets Operator + `ClusterSecretStore` | `DATABASE_URL`, `JWT_SECRET` | `aws-secrets-manager` |
| Pod Identity association per AWS-using service | SQS access | SA = service name, namespace = release namespace |
| Postgres Service | `DATABASE_URL` host | `postgres` (override `database.host`) |
| Redis Service | api-gateway rate limiting | `redis` |
| Traefik + cert-manager `ClusterIssuer` | only when `ingressRoute.enabled` | `letsencrypt-staging` / `letsencrypt-prod` |
| Stakater Reloader (optional) | restart pods when secrets rotate | - |
| Prometheus Operator (optional) | ServiceMonitors (skipped if CRD missing) | - |

## Adding a service

1. `deploy/services/<svc>/values.yaml`: `component`, `containerPort`, `resources`, and
   `database.enabled: true` if it talks to Postgres.
2. `deploy/envs/dev/<svc>.yaml`: `image.tag` and anything env-specific.
3. `scripts/helm-render.sh dev` must pass. The schema rejects unknown keys, `latest`
   tags and missing resources.

## Chart opinions (change them in the chart, not per service)

- Non-root (UID 65532), read-only root FS, all capabilities dropped, writable `/tmp`.
- Probes: liveness `/livez`, readiness `/healthz`, startup `/livez` (150s budget).
- `maxUnavailable: 0` rollouts, 10s `preStop` sleep, 45s grace period.
- PDB only when a service runs 2+ pods; HPA owns `replicas` when enabled.
- `singleton: true` (scheduler) forces 1 replica + `Recreate` and fails on HPA.
- ConfigMap changes roll pods via a checksum annotation.
