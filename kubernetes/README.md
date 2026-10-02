# Platform

Cluster tools the apps depend on. ArgoCD is installed once by hand; ArgoCD installs everything else from `platform/apps/`.

| Wave | Installed |
|---|---|
| -3 | AWS Load Balancer Controller, External Secrets, cert-manager, Karpenter, metrics-server |
| -2 | `platform/manifests/`: ClusterIssuers, ClusterSecretStore |
| -1 | Traefik, ExternalDNS, Reloader, kube-prometheus-stack |
| 0 | the 9 services (ALPHA-91) |

Values for every chart live in `helm-values/`.

## Bootstrap a new cluster

Prerequisites: Terraform applied (cluster, pod identity roles), and these placeholders replaced on `main`:
`vpcId` in `helm-values/aws-load-balancer-controller-values.yaml` (`terraform -chdir=infra/terraform output -raw vpc_id`) and the domain in `helm-values/external-dns-values.yaml`.

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

## Tear down

Delete the Traefik Service (or the `traefik` Application) before `terraform destroy`. The NLB belongs to the Load Balancer Controller, not Terraform, and destroy hangs while it exists.
