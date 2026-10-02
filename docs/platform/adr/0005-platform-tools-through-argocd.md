# ADR 0005: Install platform tools through ArgoCD (App of Apps)

**Status:** Accepted, 3 Oct (ALPHA-62). Not deployed yet.
**Decision in one line:** Helmfile installs ArgoCD once by hand. ArgoCD then installs every other cluster tool from `kubernetes/platform/apps/`, one Application per tool, in sync waves.

## Context

**What has to be installed.** The cluster needs ten tools before any app can run:

- ArgoCD;
- the AWS Load Balancer Controller;
- Traefik;
- cert-manager;
- ExternalDNS;
- External Secrets;
- Reloader;
- Karpenter;
- metrics-server;
- kube-prometheus-stack.

The list comes from ALPHA-62 as Naima updated it on 3 Oct, plus metrics-server, which our Terraform add-ons don't include.

**What the brief asks for:**

- The project brief asks for GitOps.
- The eks-v2 README asks for "ArgoCD App of Apps, auto-sync on dev".
- Naima asked for the tools to be installed through ArgoCD (3 Oct).

**Where we started.** Param's PR #44 installed 6 tools with one helmfile. It linted cleanly, but only Traefik's version was pinned, and nothing kept the cluster matching Git after the install.

**The deadline.** The first AWS deploy is 3 Oct at 4:30 UK.

## Options

| Option | What it is | For | Against |
|---|---|---|---|
| A. Helmfile for everything | One `helmfile apply` installs all tools | Fastest tonight; already validated | Installs once and never corrects drift; not what the brief or Naima asked for; we'd redo it next week |
| **B. ArgoCD App of Apps (chosen)** | Helmfile installs ArgoCD; one root Application points at a folder holding one Application per tool | Matches the brief; same mechanism as the 9 services (ALPHA-91); every change is a pull request; ArgoCD puts back manual changes | Extra setup: a repo credential, sync waves, an Application health check, `ServerSideApply` for kube-prometheus-stack |
| C. One ArgoCD ApplicationSet for all tools | A template that generates the Applications | Fewer files | Every tool has a different chart, repo, namespace and options, so the template ends up harder to read than ten plain files |
| D. Terraform `helm_release` | Terraform installs the charts | One tool for everything | Ties chart installs to Terraform runs and makes teardown harder; the team was already leaning toward Helm outside Terraform (28 Sep) |

## Decision

**B.**

- Helmfile keeps one release, ArgoCD (argo-cd chart 10.9.6), installed by hand once per cluster.
- `kubernetes/platform/root-app.yaml` is applied by hand once. It points at `kubernetes/platform/apps/`.
- Each tool's Application takes two sources: the upstream chart at a pinned version, and our repo, used only for that tool's values file ([multiple sources](https://argo-cd.readthedocs.io/en/stable/user-guide/multiple_sources/)).
- Sync waves set the order. Controllers that bring CRDs and webhooks go first (-3), then our own manifests (-2), then the tools that use them (-1), then the apps (0).

## Consequences

- **Waves need a health check.** ArgoCD stopped checking child Application health in v1.8, so the waves wouldn't wait for each other. We add the health check from the [ArgoCD upgrade notes](https://argo-cd.readthedocs.io/en/stable/operator-manual/upgrading/1.7-1.8/) to `argocd-cm` through `helm-values/argocd-values.yaml`.
- **kube-prometheus-stack syncs with `ServerSideApply=true`.** Its CRDs are larger than the 262,144-byte annotation a normal apply writes, so a normal sync fails with "Too long". This is from the chart's README.
- **The repo is private, so ArgoCD needs a read-only deploy key.** Every Application must use the same SSH repo URL so the key matches. That includes ALPHA-91's ApplicationSet.
- **ArgoCD reads `main`.** Platform changes take effect only after a merge, and the root app can't be applied before this work is merged.
- **Changes made by hand with `kubectl` get reverted** (`selfHeal`). Fixes go through pull requests.
- **Teardown has an order.** The NLB is created by the Load Balancer Controller, not Terraform, so `terraform destroy` hangs unless the Traefik Service is deleted first (ALPHA-95).

## How we know it works

On 3 Oct, every chart rendered locally with `helm template` at its pinned version with our values. ArgoCD's rendered config contains the health check. Every file under `kubernetes/` parses, with waves -3, -2 and -1.

It isn't deployed yet. The proof is every Application showing Synced and Healthy after the first deploy.

## Revisit when

- The ten small Application files start drifting from each other. That would be the time to template them with an ApplicationSet (option C).
- A second cluster (staging or prod) is added. The root app and the values may need per-environment overlays.
- ArgoCD itself needs upgrading regularly. An Application that manages ArgoCD would replace the manual helmfile step.

## Sources

- ArgoCD: [multiple sources](https://argo-cd.readthedocs.io/en/stable/user-guide/multiple_sources/), [upgrade notes 1.7 to 1.8](https://argo-cd.readthedocs.io/en/stable/operator-manual/upgrading/1.7-1.8/), [Helm](https://argo-cd.readthedocs.io/en/stable/user-guide/helm/)
- The kube-prometheus-stack README (Argo CD section)
- Inside the project:
  - the eks-v2 README;
  - the project brief;
  - ALPHA-62 as updated on 3 Oct;
  - Param's PR #44;
  - the 28 Sep planning session (Helm outside Terraform, teardown order).
