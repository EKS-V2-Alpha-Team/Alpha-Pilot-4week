# Changelog

What changed, release by release. Newest first. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow [Semantic Versioning](https://semver.org) (see [RELEASING.md](RELEASING.md#versioning)).

- **Unreleased**: merged to `main` (so running in dev) but not yet released to prod. Add a line here in your pull request when the change is worth telling people about.
- **[vX.Y.Z] - YYYY-MM-DD**: what reached prod in that release, with a link to its promotion pull request. Created by the release owner in the prod promotion pull request (see [RELEASING.md](RELEASING.md)).

Entries are written for people, not copied from commits: what changed and why it matters, with the ticket.

<!-- Release section template:
## [vX.Y.Z] - YYYY-MM-DD

### Added / Changed / Fixed / Removed / Rolled back
- What changed and why it matters (ALPHA-123)

Promotion: <link to the prod promotion pull request>
-->

## [Unreleased]

### Added
- Promote workflow: copies tested image tags dev → staging → prod through a pull request, without rebuilding (ALPHA-97)
- RELEASING.md (versioning, release steps, rollback) and this CHANGELOG (ALPHA-97)

## Project history (before v1.0.0, the first prod release)

### 5–11 October 2026

#### Added
- Dev, staging and prod environments: separate Terraform state per environment, environment-aware pipelines with a role per environment and approval for prod (ALPHA-108, ALPHA-99)
- Every cluster gets the full platform from a `platform-apps` chart, with per-environment settings (ALPHA-109)
- In-cluster Postgres for dev, RDS kept for staging and prod (ALPHA-114)
- Daily EBS snapshots of the dev database, keeping 7, with a restore procedure (ALPHA-111)
- Grafana dashboards and Prometheus alert rules (ALPHA-29), and a Grafana URL per environment (ALPHA-124)
- Safe payment retries with idempotency keys, an abuse limit on bad tokens, and an alarm when orders reach the dead-letter queue (ALPHA-119)
- Argo Rollouts controller (ALPHA-125) and the rollback strategy (ALPHA-113)
- Horizontal Pod Autoscaling for the stateless services (ALPHA-34)
- Local kind cluster setup (ALPHA-82) and local Prometheus/Grafana stack (ALPHA-103)
- Redis for gateway rate limiting (ALPHA-89), metrics endpoints on every service (ALPHA-102), kube-prometheus-stack (ALPHA-28)
- ArgoCD bootstrapped by Terraform, with every service as its own Application (ALPHA-110, ALPHA-91)
- Project README and documentation updates (ALPHA-98, ALPHA-126)

#### Fixed
- Pipeline role gets EKS cluster admin through Terraform, so the ArgoCD bootstrap survives a cluster rebuild (ALPHA-120)
- ECR repository name and node group sizing (ALPHA-121)
- Order flow gaps and app hardening (ALPHA-81)

### 28 September – 4 October 2026

#### Added
- Terraform modules: SQS with a dead-letter queue (ALPHA-54), Pod Identity (ALPHA-65), RDS (ALPHA-52), Route 53 hosted zone (ALPHA-101)
- Platform tools through Helm: Load Balancer Controller, External Secrets, cert-manager, Traefik, ExternalDNS (ALPHA-62), Karpenter NodePool and EC2NodeClass (ALPHA-64)
- Shared Helm chart for the 9 services (ALPHA-90)
- Destroy pipeline (ALPHA-95), CI updates (ALPHA-96), Makefile (ALPHA-100), CONTRIBUTING.md (ALPHA-93)

#### Changed
- Services tagged `<svc>-<sha>` (ALPHA-105); RDS password moved to a Secrets Manager secret we manage (ALPHA-104)

### 21–27 September 2026

#### Added
- Local AWS emulation with floci (ALPHA-61), pre-commit hooks (ALPHA-5), CODEOWNERS (ALPHA-71)
- Docker images for every service (ALPHA-56) and the CI pipeline (ALPHA-30)
- Terraform folder structure with plan and apply pipelines (ALPHA-72, ALPHA-36, ALPHA-67)
- VPC (ALPHA-63), EKS (ALPHA-14), Secrets Manager and KMS (ALPHA-17)
- Reliable order processing: transactional outbox (ALPHA-44), SQS worker (ALPHA-45), idempotent worker steps (ALPHA-51), stock committed on confirmation (ALPHA-77)
- Gateway rate limiting (ALPHA-73), dashboard routing through the gateway (ALPHA-48), database pool metrics (ALPHA-74)

#### Fixed
- GitHub OIDC trust policy (ALPHA-79)

<!-- Version links, newest first. Each release adds its line and moves [Unreleased] on:
[Unreleased]: https://github.com/EKS-V2-Alpha-Team/Alpha-Pilot-4week/compare/v1.1.0...HEAD
[v1.1.0]: https://github.com/EKS-V2-Alpha-Team/Alpha-Pilot-4week/compare/v1.0.0...v1.1.0
[v1.0.0]: https://github.com/EKS-V2-Alpha-Team/Alpha-Pilot-4week/releases/tag/v1.0.0
-->
