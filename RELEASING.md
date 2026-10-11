# Releasing

How a change goes from `main` to prod, who does what, and how to undo it. The reasoning behind this flow is in [ARCHITECTURE.md](ARCHITECTURE.md#deployments-and-rollback); the history of what was released is in [CHANGELOG.md](CHANGELOG.md).

## What a release is

A release is a set of image tags promoted to prod, given a version number. Images are built once, on merge to `main`, and tagged `<svc>-<sha>`. CI puts each new tag in dev; Promote copies the tags that were tested to staging, then to prod. Nothing is rebuilt along the way, so prod runs the exact images staging tested.

| Who | Does |
|---|---|
| Release owner (whoever runs the prod promotion) | Picks the version, runs Promote, updates the CHANGELOG in the promotion PR, syncs prod, checks it, tags it, publishes the GitHub Release, announces it |
| Reviewer (anyone else in CODEOWNERS) | Approves the prod promotion PR. The release owner can't approve their own |

## Versioning

Releases follow [Semantic Versioning](https://semver.org): `vMAJOR.MINOR.PATCH`, starting at `v1.0.0` for the first prod release. The version describes the whole platform release; images keep their `<svc>-<sha>` tags (ECR tags are immutable). The prod promotion pull request records which tag each service got.

| Bump | When the release contains | Example |
|---|---|---|
| **MAJOR** `v2.0.0` | A change clients or operators must act on: an API endpoint or field removed or renamed, a database change the previous version can't run against, a service removed | `/api/orders` response shape changes |
| **MINOR** `v1.1.0` | Something new that doesn't break anything: a feature, endpoint, service or platform tool | Grafana gets a URL |
| **PATCH** `v1.0.1` | Fixes only: bugs, security fixes, dependency updates, config tweaks | Payment retry bug fixed |

The highest change in the release wins: a release with one new feature and three fixes is a MINOR bump.

## Image tags

| Where | Tag | Set by |
|---|---|---|
| Built on merge to `main` | `<svc>-<sha>` (first 8 characters of the commit) | CI, pushed to the shared ECR repository |
| Dev | the newest `<svc>-<sha>` | CI, committed to `kubernetes/deploy/envs/dev/` |
| Staging and prod | the same `<svc>-<sha>` that was tested, copied, never rebuilt | Promote, through a pull request |
| Release `vX.Y.Z` | a Git tag on the promotion commit; images are not retagged | Release owner |

There is no `latest` tag: every environment names an exact image, so what runs is always what Git says. ECR tags are immutable, so a tag can never be overwritten with different code.

Keep exact image tags out of anything published outside the repo (GitHub Release notes, Slack posts shared beyond the team): a list of what prod runs tells an attacker which known vulnerabilities to try. The tags stay in the promotion pull request, inside the private repo.

## What you need

**People**

- Write access to the repo (to run Promote and push tags), and someone else to approve the prod PR
- Access to the prod cluster (`kubectl`, through the SSO role's EKS access entry) or the ArgoCD UI, to sync prod
- AWS access to read ECR, for the optional image checks

**Repository settings** (Settings → Secrets and variables → Actions), used by the Promote workflow:

| Name | Type | What it is |
|---|---|---|
| `APP_ID` | Variable | The GitHub App that pushes the promotion branch and opens the PR |
| `APP_PRIVATE_KEY` | Secret | That App's private key. The App needs **Contents** and **Pull requests**: read and write |
| `AWS_ECR_ROLE_ARN` | Variable | The CI image role, used to check each image exists in ECR |
| `AWS_REGION` | Variable | `eu-west-2` |
| `AWS_ECR_REPO` | Secret | The shared ECR repository name |

## Before you start

- [ ] Dev and staging show every Application `Synced` and `Healthy` (`kubectl -n argocd get applications`)
- [ ] The release was checked in staging (dashboard loads, an order goes through)
- [ ] Any Terraform change in this release has been applied to dev and staging, and its prod plan shows only the expected changes
- [ ] You've picked the version (see [Versioning](#versioning)) and checked it isn't taken: `git ls-remote --tags origin`
- [ ] The team knows a prod release is happening (Slack)

## Steps

1. **Promote to staging**: Actions → **Promote**, `from: dev`, `to: staging`, `services: all` (or a list). Review and merge the pull request it opens. Staging deploys on merge.
2. **Check staging**: Applications `Synced` and `Healthy`, the dashboard works, an order completes.
3. **Promote to prod**: Actions → **Promote**, `from: staging`, `to: prod`. The workflow refuses any tag that isn't in ECR.
4. **Update the CHANGELOG in that pull request**: move the items under `Unreleased` into a new `## [vX.Y.Z] - YYYY-MM-DD` section, link the promotion pull request, and add the version's compare link at the bottom of the file.
5. **Get it approved and merge it.**
6. **Sync prod**: prod services don't auto-sync. Sync each changed Application in the ArgoCD UI, or:
   ```bash
   kubectl -n argocd patch application <svc>-prod --type merge -p '{"operation":{"initiatedBy":{"username":"<you>"},"sync":{"revision":"HEAD"}}}'
   ```
7. **Verify** (next section).
8. **Tag the release** on the promotion PR's merge commit (copy its SHA from the PR):
   ```bash
   git fetch origin && git tag -a vX.Y.Z <merge-commit-sha> -m "vX.Y.Z" && git push origin vX.Y.Z
   ```
9. **Publish the GitHub Release**: Releases → **Draft a new release** → choose tag `vX.Y.Z` → title `vX.Y.Z` → paste that version's CHANGELOG section → **Publish**. With the GitHub CLI:
   ```bash
   awk '/^## \[vX.Y.Z\]/{f=1;next} /^## \[/{f=0} f' CHANGELOG.md > /tmp/notes.md && gh release create vX.Y.Z --title vX.Y.Z --notes-file /tmp/notes.md
   ```
10. **Announce it** in Slack: version, what changed, links to the PR and the GitHub Release.

## Verify the image tags

**Compare environments** (which tag each service runs in dev, staging and prod, according to Git):

```bash
for f in kubernetes/deploy/envs/dev/*.yaml; do svc=$(basename "$f" .yaml); [ "$svc" = common ] && continue; printf '%-22s' "$svc"; for e in dev staging prod; do printf '%-34s' "$(sed -n 's/^  tag: *//p' kubernetes/deploy/envs/$e/$svc.yaml)"; done; echo; done
```

**Before promoting** a tag by hand, check it exists:

```bash
aws ecr describe-images --repository-name eks-v2-shared-ecr --region eu-west-2 --image-ids imageTag=<svc>-<sha>
```

**After syncing prod**, check every service runs the tag Git says it should (run from an up-to-date `main`, with `kubectl` on the prod cluster):

```bash
for f in kubernetes/deploy/envs/prod/*.yaml; do svc=$(basename "$f" .yaml); [ "$svc" = common ] && continue; want=$(sed -n 's/^  tag: *//p' "$f"); have=$(kubectl -n order-platform get deploy "$svc" -o jsonpath='{.spec.template.spec.containers[0].image}' | sed 's/.*://'); [ "$want" = "$have" ] && echo "OK    $svc $have" || echo "DIFF  $svc running $have, Git says $want"; done
```

Every line should start with `OK`. A `DIFF` means that Application hasn't been synced yet, or its rollout failed (check it in ArgoCD).

## Roll back

One service: revert the commit or PR line that changed its tag in `kubernetes/deploy/envs/<env>/<svc>.yaml`, merge, and in prod sync that Application. The previous image is still in ECR, so nothing is rebuilt. For prod, release the rollback as a PATCH version (`vX.Y.Z+1`) with a `Rolled back` entry in the CHANGELOG, so the history matches what's running.

Never use `kubectl rollout undo` or edit a Deployment: ArgoCD's self-heal puts the Git version straight back. The full procedure is the runbook "A bad release reached an environment".

## What Promote doesn't cover

- **Terraform**: merging changes the code, not the environments. Each environment changes when its Terraform Apply runs, so apply dev, then staging, then prod (prod needs approval).
- **Shared Kubernetes config** (`kubernetes/charts/`, `kubernetes/platform/`, `kubernetes/helm-values/`): every environment's ArgoCD follows `main`, so these reach all three as soon as they merge. Test them in dev (and with `helm template`) before merging.
