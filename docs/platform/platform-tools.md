# Platform tools

Describes: branch `alpha-62-install-helm-charts` (ALPHA-62). Not deployed yet. Update this line when it merges and after the first deploy.

This page covers the tools every app in the cluster depends on: what each one does for us, how they get installed, which AWS role each one uses, and how we got to this design.

- ArgoCD keeps the cluster matching what's in Git. We install it once by hand. After that, ArgoCD installs everything else.
- Everything else lives in `kubernetes/platform/apps/`, one small file per tool, installed in a fixed order ("waves").
- Traefik is the front door. It sits behind an AWS Network Load Balancer (NLB) that the AWS Load Balancer Controller creates.
- cert-manager gets HTTPS certificates, ExternalDNS points our domain at the NLB, and External Secrets copies passwords from AWS Secrets Manager into the cluster.
- Tools that call AWS get their permissions through EKS Pod Identity. No pod carries AWS keys.

Stop here if you only need the shape. Below: what drove the design, what each tool does, the install order, the AWS roles, and the decisions we made along the way.

---

## 1. What shaped this design

| Driver | Where it comes from | What it means for the platform |
|---|---|---|
| GitOps | Project brief ("GitOps"); eks-v2 README ("ArgoCD App of Apps, auto-sync on dev") | One root ArgoCD app installs every tool. Changes go in by pull request, and ArgoCD puts back anything changed by hand. |
| Prescribed stack | eks-v2 README: Traefik + NLB, cert-manager + Let's Encrypt, ExternalDNS + Route 53, External Secrets, Karpenter | We install those tools, not substitutes. ingress-nginx is banned by the README. |
| No long-lived AWS keys, least privilege | Project brief; team decision to use Pod Identity (18 Sep) | Each tool that calls AWS has its own role, linked to its service account. |
| Observability in the base product | Stakeholder requirement | kube-prometheus-stack (Prometheus, Grafana, Alertmanager) installed with the platform, and it collects the apps' metrics too. |
| Safe deployments and rollback | Project brief | Rolling updates from Naima's chart, plus rollback by reverting in Git. No Argo Rollouts yet (section 6). |
| AWS spend, the #1 graded priority | $200 cap for the whole simulation | One NLB, billed per hour while it exists, so it's deleted between test cycles. Prometheus keeps 24 hours of data on a 20 GiB disk (about $2 a month). |

---

## 2. What each tool does for us

| Tool | In plain words | In our cluster | If it's missing or wrong |
|---|---|---|---|
| ArgoCD | A robot that keeps the cluster matching Git | Installs every other tool and, later, the 9 services | Nothing gets installed or kept up to date |
| AWS Load Balancer Controller | Builds AWS load balancers on request | Creates the public NLB in front of Traefik | Traefik's Service stays "pending", so the site can't be reached |
| Traefik | The traffic router | Sends each request to the right service | No way in from outside |
| cert-manager | Gets and renews HTTPS certificates | Asks Let's Encrypt for our domain's certificate | No HTTPS |
| ExternalDNS | Writes DNS records for us | Points our domain at the NLB in Route 53 | The domain doesn't reach the cluster |
| External Secrets | Copies secrets from AWS into the cluster | Turns Secrets Manager entries into Kubernetes Secrets, for example `DATABASE_URL` | 7 services start without their database password and crash |
| Reloader | Restarts pods when a secret changes | Picks up rotated RDS passwords | Pods keep the old password until someone restarts them |
| Karpenter | Adds machines when the cluster is full | Starts EC2 nodes for pods that don't fit | Pods wait for a free node |
| metrics-server | Reports CPU and memory per pod | Gives the autoscaler (HPA) its numbers | HPA can't scale anything |
| kube-prometheus-stack | Collects metrics, draws dashboards, sends alerts | Prometheus, Grafana and Alertmanager | No visibility when something breaks |

---

## 3. How a cluster gets set up

1. **Terraform** builds the cluster, the starter nodes, the database, the queue, and one AWS role per tool.
2. **ArgoCD, by hand, once:** `helmfile -f kubernetes/helmfile.yaml apply`. ArgoCD is the thing that does the installing, so it can't install itself the first time.
3. **A read-only deploy key** lets ArgoCD read our private repo.
4. **The root app, by hand, once:** `kubectl apply -f kubernetes/platform/root-app.yaml`.
5. **ArgoCD installs the rest in waves.** A wave only starts when the one before it is healthy:

| Wave | What | Why this order |
|---|---|---|
| -3 | Load Balancer Controller, External Secrets, cert-manager, Karpenter, metrics-server | They bring the building blocks (CRDs and webhooks) the later waves use |
| -2 | Our own manifests: ClusterIssuers, ClusterSecretStore | These are kinds that only exist once wave -3 is up |
| -1 | Traefik, ExternalDNS, Reloader, kube-prometheus-stack | They use the controllers from wave -3 |
| 0 | The 9 services (ALPHA-91) | They need the secrets, routing and certificates above |

The exact commands are in `kubernetes/README.md`.

---

## 4. Which AWS role each tool uses

A pod proves who it is through its **service account**, a named identity inside Kubernetes. Terraform links a namespace plus a service account to an AWS role. This link is called a **Pod Identity association**. The names on both sides must match exactly, or every AWS call from that tool fails with AccessDenied.

| Tool | Namespace / service account (values file) | Terraform (pod-identity module) |
|---|---|---|
| Karpenter | `kube-system` / `karpenter` | Matches |
| ExternalDNS | `external-dns` / `external-dns` | Matches |
| cert-manager | `cert-manager` / `cert-manager` | Matches |
| External Secrets | `external-secrets` / `external-secrets` | Matches. Fixed in ALPHA-62 (it was the placeholder `secrets_manager`). |
| Load Balancer Controller | `kube-system` / `aws-load-balancer-controller` | Matches. Added in ALPHA-62 with AWS's official v3.5.0 policy, kept unchanged in `modules/pod-identity/policies/`. |
| EBS CSI driver | Set by the EKS add-on | In the EKS module |

Traefik, Reloader, metrics-server, kube-prometheus-stack and ArgoCD don't call AWS, so they have no role.

---

## 5. The settings that matter

| Setting | Where | In one line |
|---|---|---|
| `loadBalancerClass: service.k8s.aws/nlb`, `ip` targets, `internet-facing` | `helm-values/traefik-values.yaml` | The Load Balancer Controller builds the NLB, sends traffic straight to the Traefik pods, and makes it public. The controller defaults to internal. |
| `clusterName`, `region`, `vpcId` | `helm-values/aws-load-balancer-controller-values.yaml` | Set explicitly, so the controller doesn't depend on the node's metadata service. `vpcId` is filled in per deploy from `terraform output -raw vpc_id`. |
| Application health check | `helm-values/argocd-values.yaml` | Makes the waves actually wait. ArgoCD stopped checking child app health in v1.8. |
| `ServerSideApply=true` | `platform/apps/kube-prometheus-stack.yaml` | Its CRDs are too large for a normal apply. |
| `serviceMonitorSelectorNilUsesHelmValues: false` | `helm-values/prometheus-values.yaml` | Prometheus collects every ServiceMonitor, including the apps'. |
| No extra Grafana datasource | `helm-values/prometheus-values.yaml` | The chart already adds the default one. A second default stops Grafana from starting. |
| `upsert-only`, sources `ingress` + `traefik-proxy` | `helm-values/external-dns-values.yaml` | It never deletes records, and it reads Traefik's routing objects, which Naima's chart uses. |
| `letsencrypt-staging` and `letsencrypt-prod` | `platform/manifests/cluster-issuers.yaml` | Staging for testing (untrusted certificates, generous limits), prod for real certificates (strict rate limits). |
| `aws-secrets-manager` ClusterSecretStore | `platform/manifests/cluster-secret-store.yaml` | Named to match what Naima's chart asks for. |
| Pinned versions | `platform/apps/*.yaml`, `helmfile.yaml` | argo-cd 10.9.6, Load Balancer Controller 3.5.0, External Secrets 2.11.0, cert-manager v1.21.2, Karpenter 1.14.1, metrics-server 3.14.0, Traefik 41.6.0, external-dns 1.23.0, Reloader 2.2.18, kube-prometheus-stack 91.9.0 |

---

## 6. How we got here

| Decision | Where we started | What we considered | What we chose, and why |
|---|---|---|---|
| How tools get installed | Param's PR #44: one helmfile installing 6 tools | Helmfile for everything; ArgoCD App of Apps; one ArgoCD ApplicationSet; Terraform `helm_release` | **ArgoCD App of Apps, with Helmfile installing only ArgoCD.** It's what the brief and Naima asked for, and the same mechanism as the 9 services. ADR 0005. |
| Which tools | The ticket's first list: Karpenter, Traefik, Prometheus, Grafana, ExternalDNS, Let's Encrypt, ArgoCD | Naima's update on 3 Oct added External Secrets, the Load Balancer Controller and Reloader | **All of them, plus metrics-server,** which HPA needs and our Terraform add-ons don't include. |
| Who creates the NLB | A plain `LoadBalancer` Service, which gets a Classic Load Balancer on EKS | EKS's built-in controller with an `nlb` annotation; the AWS Load Balancer Controller | **The Load Balancer Controller.** The EKS docs recommend it over the built-in controller, which only gets critical bug fixes, the accelerator series uses it, and `ip` targets skip the extra hop through every node. It costs one more chart and one more role. |
| Chart versions | Only Traefik pinned; the rest on `latest` | Latest; latest stable major | **Latest stable, pinned,** as agreed at the 2 Oct standup. Karpenter needs 1.9 or later for EKS 1.35. Reloader's newest is a 3.0 beta, so we kept 2.2.18. |
| Grafana datasource | A second datasource marked default | Keep it; remove it | **Removed.** The chart already adds one, and two defaults stop Grafana from starting. |
| App metrics | Prometheus only collected ServiceMonitors labelled `release: prometheus` | Label every app; change the selector | **Changed the selector.** One line here instead of a label in every app. |
| Certificate check | HTTP-01 through Traefik | HTTP-01; DNS-01 through Route 53 | **HTTP-01 for now.** It works once the domain points at the NLB. DNS-01 is what the accelerator series recommends, and cert-manager's role already has the Route 53 permissions, so it's a later switch. |
| Overlap with EKS add-ons | EKS offers metrics-server, kube-state-metrics, node-exporter, cert-manager, ExternalDNS and Fluent Bit as community add-ons | EKS add-ons; ArgoCD | **ArgoCD,** so every non-core tool is managed in one place with values in Git. The 5 core add-ons (VPC CNI, kube-proxy, CoreDNS, pod identity agent, EBS CSI) stay in Terraform. kube-prometheus-stack already includes kube-state-metrics and node-exporter, so they aren't installed twice. |
| Argo Rollouts | Raised by Naima on 3 Oct | Install now; later | **Later (week 4).** It only acts on `Rollout` objects, so the app chart would need converting first. Canary releases also need Traefik and Prometheus wiring. Rolling updates plus a Git revert already cover "safe deployments and rollback", and the team chose rolling updates on 27 Sep. |

---

## 7. What we accepted and what's still open

- **Two placeholders in the values:** `vpcId` for the Load Balancer Controller, and the ExternalDNS domain. Fill `vpcId` from `terraform output -raw vpc_id` after apply, and the domain once the team picks it on 3 Oct.
- **The hosted zone is still a placeholder.** `route53_hosted_zone_arn` defaults to `"n/a"` in the pod-identity module, and the ExternalDNS and cert-manager policies use it. IAM policies identify resources by ARN, so set the chosen zone's ARN before applying, or the apply will most likely fail on those two policies. The team picks the domain on 3 Oct.
- **Secrets are deleted immediately on destroy (dev only).** The Grafana and JWT secrets set `recovery_window_in_days = 0`, so the next apply after a destroy can reuse their names. In prod, keep the default 30-day recovery window.
- **Karpenter won't add nodes yet.** The subnets and security group have no `karpenter.sh/discovery` tags, and the EC2NodeClass in PR #39 names a different cluster (ALPHA-64). Until that's fixed we run on the 3 starter nodes.
- **Teardown order.** The NLB belongs to the Load Balancer Controller, not Terraform. Delete the Traefik Service first, or `terraform destroy` hangs. The destroy pipeline (ALPHA-95) has to do this.
- **Metrics live 24 hours** on a 20 GiB `gp2` disk. That's enough to debug a test cycle, not for trends.

---

## 8. Rules that must stay true

1. The service account names in the values files match the Pod Identity associations in Terraform.
2. Every ArgoCD app uses the same repo URL as the repo credential: `git@github.com:EKS-V2-Alpha-Team/Alpha-Pilot-4week.git`.
3. A tool other tools depend on goes in an earlier wave than its users.
4. Every chart version is pinned. Never `latest`.
5. kube-prometheus-stack keeps `ServerSideApply=true`.
6. Platform changes go through a pull request to `main`. Changes made by hand with `kubectl` get reverted by ArgoCD (`selfHeal`).
7. Delete the Traefik Service before `terraform destroy`.

---

## 9. How we know it works

Checked locally on 3 Oct with `helm template` and `helmfile template`:

- **Load Balancer Controller, External Secrets, metrics-server and Reloader** all rendered with our values and versions.
- **Traefik's Service** rendered with `loadBalancerClass: service.k8s.aws/nlb`, `ip` targets and `internet-facing`.
- **ExternalDNS** rendered with `--source=ingress`, `--source=traefik-proxy`, and read access to Traefik's objects.
- **ArgoCD's config** contains the Application health check.
- **Before the Grafana fix,** the rendered output had `isDefault: true` twice. After it, once.
- **Every file** under `kubernetes/` parses, and the waves are -3, -2, -1 as planned.

Not deployed yet. After the first deploy, check:

```bash
kubectl -n argocd get applications                          # every app Synced and Healthy
kubectl -n traefik get svc traefik                          # EXTERNAL-IP shows the NLB address
kubectl get clustersecretstore aws-secrets-manager          # READY True
kubectl get clusterissuer                                   # both READY True
```

---

## 10. Reference

### Where things live

| What | Where |
|---|---|
| ArgoCD install (by hand) | `kubernetes/helmfile.yaml`, `kubernetes/helm-values/argocd-values.yaml` |
| Root app | `kubernetes/platform/root-app.yaml` |
| One app per tool | `kubernetes/platform/apps/` |
| Our own manifests | `kubernetes/platform/manifests/` |
| Settings per tool | `kubernetes/helm-values/` |
| Bootstrap and teardown commands | `kubernetes/README.md` |
| AWS roles | `infra/terraform/modules/pod-identity/main.tf`, `infra/terraform/modules/eks/addons.tf` |

### Sources

Official docs we designed against:

- ArgoCD: [multiple sources for an Application](https://argo-cd.readthedocs.io/en/stable/user-guide/multiple_sources/), [upgrade notes 1.7 to 1.8 (Application health)](https://argo-cd.readthedocs.io/en/stable/operator-manual/upgrading/1.7-1.8/), [Helm](https://argo-cd.readthedocs.io/en/stable/user-guide/helm/)
- [AWS Load Balancer Controller: NLB](https://kubernetes-sigs.github.io/aws-load-balancer-controller/latest/guide/service/nlb/), [EKS: Network Load Balancers](https://docs.aws.amazon.com/eks/latest/userguide/network-load-balancing.html)
- [Amazon EKS community add-ons](https://docs.aws.amazon.com/eks/latest/userguide/community-addons.html)
- [Karpenter compatibility](https://karpenter.sh/docs/upgrading/compatibility/)
- [Argo Rollouts: migrating](https://argo-rollouts.readthedocs.io/en/stable/migrating/)
- kube-prometheus-stack chart README (Argo CD section) and default values, read on Artifact Hub

Inside the project:

- the eks-v2 README and the project brief;
- the stakeholder assumptions (24 Sep);
- the 2 Oct standup (stable chart versions) and Naima's ticket update (3 Oct);
- `deploy/README.md` in Naima's app chart (what the cluster must provide);
- Param's PR #44, which this work builds on.

Next: ADR 0005 for the install decision, or `kubernetes/README.md` to set up a cluster.
