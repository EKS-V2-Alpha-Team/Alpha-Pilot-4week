# Platform

Cluster tools the apps depend on. Each environment (dev, staging, prod) has its own cluster and its own ArgoCD. `infra/argocd-bootstrap` installs ArgoCD and that environment's root app; ArgoCD installs everything else from the `platform/apps/` chart (App of Apps). Every platform change is a pull request, and ArgoCD reverts changes made by hand (`selfHeal`).

| Path                                               | What                                                                                              |
| -------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| `helmfile.yaml` + `helm-values/argocd-values.yaml` | ArgoCD, installed by hand once (or by `infra/argocd-bootstrap`)                                    |
| `platform/root-app-<env>.yaml`                     | Root app per environment. Installs the `platform/apps/` chart with `environment: <env>`           |
| `platform/apps/`                                   | Helm chart: one ArgoCD Application per tool (pinned chart + our values) and the services ApplicationSet. `environment` picks the per-env files below; Postgres is dev only, prod services have no auto-sync |
| `platform/manifests/`                              | Our own cluster resources, the same in every environment (ClusterIssuers, ClusterSecretStore, Karpenter NodePool, namespaces, default gp3 StorageClass, default `ebs-csi` VolumeSnapshotClass) |
| `platform/manifests/envs/<env>/`                   | The ones that name the environment: Karpenter EC2NodeClass, Grafana secret, Postgres secret (dev) |
| `helm-values/`                                     | Values for every chart. Non-obvious settings are explained inline                                 |
| `helm-values/envs/<env>/`                          | Per-environment overrides: cluster name, VPC tags, ExternalDNS owner ID, public hostname           |
| `charts/microservice/`                             | Shared chart for the 9 services (see `deploy/README.md`)                                          |
| `deploy/`                                          | Per-service and per-environment values for that chart                                             |

To see what a cluster gets: `helm template kubernetes/platform/apps --set environment=staging`.

## Tools

| Tool                         | Job in this cluster                                                                                | Wave |
| ---------------------------- | -------------------------------------------------------------------------------------------------- | ---- |
| AWS Load Balancer Controller | Creates the public NLB in front of Traefik                                                         | -3   |
| External Secrets             | Copies Secrets Manager entries (RDS password, JWT secret) into Kubernetes Secrets                  | -3   |
| cert-manager                 | Gets Let's Encrypt certificates                                                                    | -3   |
| Karpenter                    | Adds EC2 nodes when pods don't fit                                                                 | -3   |
| SnapScheduler                | Takes scheduled VolumeSnapshots and deletes old ones (Postgres: daily, keep 7)                     | -3   |
| metrics-server               | CPU/memory numbers for the HPA                                                                     | -3   |
| `platform/manifests/`        | ClusterIssuers, ClusterSecretStore, Karpenter EC2NodeClass + NodePool, VolumeSnapshotClass (need the CRDs from wave -3 and the snapshot-controller add-on) | -2   |
| Traefik                      | Routes requests to services                                                                        | -1   |
| ExternalDNS                  | Points the domain at the NLB in Route 53                                                           | -1   |
| Reloader                     | Restarts pods when a secret rotates                                                                | -1   |
| Redis (StatefulSet, 1Gi gp3)  | Rate-limit counters for api-gateway, in `order-platform`                                           | -1   |
| kube-prometheus-stack        | Prometheus, Grafana, Alertmanager                                                                  | -1   |
| The 9 services               | `order-platform.yaml` ApplicationSet: one Application per service                                  | 0    |

A wave starts only when the previous one is healthy (health check in `argocd-values.yaml`).

## AWS access (Pod Identity)

Tools that call AWS get a role through an EKS Pod Identity association in `infra/terraform/modules/pod-identity/`. The namespace and service account below must match Terraform exactly, or every AWS call fails with AccessDenied.

| Workload                     | Namespace / service account                    |
| ---------------------------- | ---------------------------------------------- |
| Karpenter                    | `kube-system` / `karpenter`                    |
| AWS Load Balancer Controller | `kube-system` / `aws-load-balancer-controller` |
| ExternalDNS                  | `external-dns` / `external-dns`                |
| cert-manager                 | `cert-manager` / `cert-manager`                |
| External Secrets             | `external-secrets` / `external-secrets`        |
| order-service, worker (SQS)  | `order-platform` / `order-service`, `worker`   |

Traefik, Reloader, SnapScheduler, metrics-server, kube-prometheus-stack and ArgoCD don't call AWS. EBS snapshots are taken by the EBS CSI driver, whose role is in `infra/terraform/modules/eks/`.

## Rules

1. Service account names in `helm-values/` match the Pod Identity associations in Terraform.
2. Every ArgoCD Application uses `git@github.com:EKS-V2-Alpha-Team/Alpha-Pilot-4week.git`, so the deploy key matches.
3. A tool others depend on goes in an earlier wave.
4. Every chart version is pinned. Never `latest`.

## DNS (one-off)

The hosted zone `eksv2.naimaosmann.com` is created in `bootstrap/` (it survives `terraform destroy`). `infra/terraform` looks it up by name, so apply `bootstrap/` first.

1. `terraform -chdir=bootstrap apply`
2. In Cloudflare (`naimaosmann.com`), add 4 `NS` records named `eksv2`, one per value of `terraform -chdir=bootstrap output route53_hosted_zone_name_servers`. DNS only, not proxied.
3. Check: `dig NS eksv2.naimaosmann.com +short` returns the Route 53 name servers.

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
kubectl apply -f kubernetes/platform/root-app-dev.yaml
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

- Redis has no password. Only the gateway uses it, for rate-limit counters, and it fails open.
- Grafana generates its own admin password; the Secrets Manager one isn't wired in yet.

## Tear down

Delete the Traefik Service (or the `traefik` Application) before `terraform destroy`. The NLB belongs to the Load Balancer Controller, not Terraform, and destroy hangs while it exists.
