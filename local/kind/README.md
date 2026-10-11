# Local Kubernetes with kind

Run the Order Fulfillment Platform's 9 services on a real Kubernetes cluster on
your laptop, with in-cluster Postgres, Redis, Traefik and floci (local SQS). No AWS account, no cost.

It uses the same Helm chart and values as Argo CD (plus a small kind-only
overlay), and runs the real startup, liveness and readiness probes.

## Quick start

Needs Docker (give it at least 4 CPUs and 8 GB RAM), kind, kubectl and helm. Run from the repo root.

```bash
make kind-all        # up → platform → build → load → deploy
make kind-help       # list targets and current settings
make kind-down       # delete the cluster (keeps images); kind-clean also removes images
```

Open the app at **http://localhost:8080**. Traffic goes through Traefik and
api-gateway's IngressRoute, the same path as on EKS (but plain HTTP).

## Make targets

Also runs without the root Makefile: `make -f local/kind/kind.mk <target>`.

| Target | What it does |
|---|---|
| `kind-help` / `kind-check` | List targets and current settings / check docker, kind, kubectl, helm |
| `kind-up` / `kind-down` / `kind-clean` | Create the cluster / delete it (keeps images) / delete it and the built images |
| `kind-platform` | Postgres, Redis, Traefik (same version/values as Argo CD), floci, metrics-server |
| `kind-build` / `kind-load` | Build images from `services/<svc>` / copy them into the cluster |
| `kind-deploy` | Install `SERVICES` with `ENV` values into `order-platform` |
| `kind-render` | Render every service for every env; fail if anything AWS-only leaks in. No cluster needed |
| `kind-status` | Show nodes, releases, pods |
| `kind-all` | up → platform → build → load → deploy |

Variables: `SERVICES="order-service worker"` (default: all 9), `ENV=staging`
(default `dev`; must exist in `kubernetes/deploy/envs/`).

## How it matches EKS

`kind-deploy` installs the chart exactly like the Argo CD ApplicationSet: one
release per folder in `deploy/services/`, namespace `order-platform`, same value
files in the same order, plus one kind-only file last:

```text
kubernetes/deploy/services/<svc>/values.yaml
kubernetes/deploy/envs/<env>/common.yaml
kubernetes/deploy/envs/<env>/<svc>.yaml
local/kind/values-kind.yaml        ← kind only; Argo CD never reads local/
```

> If the ApplicationSet's `valueFiles` change, update `kind.mk` to match.

Only what can't exist on a laptop is replaced:

| On EKS | In kind |
|---|---|
| ECR images | Built locally (`order-platform-local:<svc>-local`) |
| DATABASE_URL and secrets via External Secrets | Set directly in `values-kind.yaml` |
| AWS SQS | floci in the cluster, queues in eu-west-2 |
| EBS `gp3` | A `gp3` class on local disk |
| Traefik behind an AWS NLB, 2 replicas, HTTPS | 1 replica on node port 80 → `localhost:8080`, plain HTTP |
| IngressRoute on `dashboard.dev...` with Let's Encrypt TLS | Host `localhost`, `web` entry point, no TLS |
| Secret `postgres-credentials` | Created by `kind-platform` with throwaway local credentials |
| Postgres daily snapshots (SnapScheduler + EBS) | Skipped (`KIND_PLATFORM_SKIP` in `kind.mk`) |
| ServiceMonitor, Certificate | Switched off |

**Not tested in kind:** RDS, Pod Identity/IAM, Secrets Manager, ECR pulls, real
SQS, TLS / cert-manager, Reloader (it only acts on External Secrets), Prometheus
scraping, Karpenter, NLB, external-dns, Argo CD sync. Local credentials are
throwaway; never reuse them.

## Check an order end to end

Submit an order through the application and follow its processing through the relevant services to shipping.

### Inspect recent logs

```bash
for d in order-service worker payment-service shipping-service notification-service; do
  echo "== $d"
  kubectl logs "deploy/$d" -n order-platform --since=10m --tail=10
done
```

### Check local SQS queue depths

```bash
for q in order-events order-events-dlq; do
  echo "== $q"
  kubectl exec deploy/floci -n order-platform -- \
    aws sqs get-queue-attributes \
      --queue-url "http://floci:4566/000000000000/$q" \
      --attribute-names ApproximateNumberOfMessages
done
```

### Verify the result

Confirm that the order reaches its expected final state and that the relevant service logs show successful processing.

Queue depths may return to zero after messages have been consumed. A non-zero dead-letter queue depth warrants investigation; messages are configured to move to the dead-letter queue after repeated unsuccessful receives, according to the local queue policy.
