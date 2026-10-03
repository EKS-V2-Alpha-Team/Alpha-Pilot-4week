# Platform

Cluster tools the apps depend on. ArgoCD is installed once by hand; ArgoCD installs everything else from `platform/apps/` (App of Apps). Every platform change is a pull request, and ArgoCD reverts changes made by hand (`selfHeal`).

| Path | What |
|---|---|
| `helmfile.yaml` + `helm-values/argocd-values.yaml` | ArgoCD, installed by hand once |
| `platform/root-app.yaml` | Root app, applied by hand once; points at `platform/apps/` |
| `platform/apps/` | One ArgoCD Application per tool: upstream chart at a pinned version + our values file |
| `platform/manifests/` | Our own cluster resources (ClusterIssuers, ClusterSecretStore, Karpenter EC2NodeClass + NodePool) |
| `helm-values/` | Values for every chart. Non-obvious settings are explained inline |

## Tools

| Tool | Job in this cluster | Wave |
|---|---|---|
| AWS Load Balancer Controller | Creates the public NLB in front of Traefik | -3 |
| External Secrets | Copies Secrets Manager entries (RDS password, JWT secret) into Kubernetes Secrets | -3 |
| cert-manager | Gets Let's Encrypt certificates | -3 |
| Karpenter | Adds EC2 nodes when pods don't fit | -3 |
| metrics-server | CPU/memory numbers for the HPA | -3 |
| `platform/manifests/` | ClusterIssuers, ClusterSecretStore, Karpenter EC2NodeClass + NodePool (need the CRDs from wave -3) | -2 |
| Traefik | Routes requests to services | -1 |
| ExternalDNS | Points the domain at the NLB in Route 53 | -1 |
| Reloader | Restarts pods when a secret rotates | -1 |
| kube-prometheus-stack | Prometheus, Grafana, Alertmanager | -1 |
| The 9 services (ALPHA-91) | | 0 |

A wave starts only when the previous one is healthy (health check in `argocd-values.yaml`).

## AWS access (Pod Identity)

Tools that call AWS get a role through an EKS Pod Identity association in `infra/terraform/modules/pod-identity/`. The namespace and service account below must match Terraform exactly, or every AWS call fails with AccessDenied.

| Workload | Namespace / service account |
|---|---|
| Karpenter | `kube-system` / `karpenter` |
| AWS Load Balancer Controller | `kube-system` / `aws-load-balancer-controller` |
| ExternalDNS | `external-dns` / `external-dns` |
| cert-manager | `cert-manager` / `cert-manager` |
| External Secrets | `external-secrets` / `external-secrets` |
| order-service, worker (SQS) | `order-platform` / `order-service`, `worker` |

Traefik, Reloader, metrics-server, kube-prometheus-stack and ArgoCD don't call AWS.

## Rules

1. Service account names in `helm-values/` match the Pod Identity associations in Terraform.
2. Every ArgoCD Application uses `git@github.com:EKS-V2-Alpha-Team/Alpha-Pilot-4week.git`, so the deploy key matches.
3. A tool others depend on goes in an earlier wave.
4. Every chart version is pinned. Never `latest`.

## Before the first deploy

Replace these placeholders on `main`:

| Placeholder | File | Value |
|---|---|---|
| `REPLACE_WITH_VPC_ID` | `helm-values/aws-load-balancer-controller-values.yaml` | `terraform -chdir=infra/terraform output -raw vpc_id` |
| `REPLACE_WITH_TEAM_DOMAIN` | `helm-values/external-dns-values.yaml` (`domainFilters`) | The Route 53 hosted zone, e.g. `orders.<domain>` |
| `REPLACE_WITH_TEAM_DOMAIN` | `helm-values/traefik-values.yaml` (hostname annotation) | e.g. `dashboard.dev.orders.<domain>`; must equal api-gateway's `ingressRoute.host` |
| `route53_hosted_zone_arn = "n/a"` | `infra/terraform/modules/pod-identity` | The hosted zone's ARN (ExternalDNS and cert-manager policies use it) |

## Bootstrap a new cluster

```bash
aws eks update-kubeconfig --name eks-v2-dev-cluster --region eu-west-2

# 1. ArgoCD
helmfile -f kubernetes/helmfile.yaml apply

# 2. Let ArgoCD read this private repo (read-only GitHub deploy key)
kubectl -n argocd create secret generic alpha-repo \
  --from-literal=type=git \
  --from-literal=url=git@github.com:EKS-V2-Alpha-Team/Alpha-Pilot-4week.git \
  --from-file=sshPrivateKey=<path-to-deploy-key>
kubectl -n argocd label secret alpha-repo argocd.argoproj.io/secret-type=repository

# 3. Hand over to ArgoCD
kubectl apply -f kubernetes/platform/root-app.yaml
kubectl -n argocd get applications -w
```

Then check:

```bash
kubectl -n argocd get applications                    # every app Synced and Healthy
kubectl -n traefik get svc traefik                    # EXTERNAL-IP shows the NLB address
kubectl get clustersecretstore aws-secrets-manager    # READY True
kubectl get clusterissuer                             # both READY True
```

## Known gaps

- Not installed yet: gp3 default StorageClass, VolumeSnapshotClass + snapshot-controller, Redis.
- Grafana generates its own admin password; the Secrets Manager one isn't wired in yet.

## Tear down

Delete the Traefik Service (or the `traefik` Application) before `terraform destroy`. The NLB belongs to the Load Balancer Controller, not Terraform, and destroy hangs while it exists.
