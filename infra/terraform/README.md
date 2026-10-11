# Terraform Environments

Infrastructure for the EKS v2 Order Fulfillment Platform, split into one folder per environment.

```
infra/terraform/
├── modules/
│   ├── eks/
│   ├── pod-identity/
│   ├── rds/
│   ├── secrets-manager/
│   ├── sqs/
│   └── vpc/
│
└── environments/
    ├── dev/
    ├── staging/
    └── prod/
```

Each environment folder has its own backend and `terraform.tfvars`.



## Environments at a glance

| | dev | staging | prod |
|---|---|---|---|
| Purpose | Cheap, breakable, fast feedback | Prod-shaped release gate | Live |
| State | `eks-v2-nonprod-tfstate` / `dev/` | `eks-v2-nonprod-tfstate` / `staging/` | `eks-v2-prod-tfstate` / `prod/` |
| VPC CIDR | 10.6.0.0/16 | 10.7.0.0/16 | 10.8.0.0/16 |
| System nodes (t3.medium, on-demand) | 2 (max 3) | 2 (max 4) | 3 (max 6), one per AZ |
| Postgres | In-cluster | RDS `db.t4g.medium`, single-AZ | RDS `db.m7g.large`, Multi-AZ |
| RDS backups | – | 3 days | 14 days |
| RDS protection | – | None | Deletion protection + final snapshot |
| RDS alerts | – | Slack `#alerts-staging` | Slack `#alerts-prod` |

Workload nodes, replicas, Redis and hostnames are configured in Helm / Karpenter, not here.


## Conventions

- **Same code, different inputs.** `staging/main.tf` and `prod/main.tf` are identical. Every difference lives in `terraform.tfvars`. Compare the two tfvars files to see how they differ.
- **Dev is the exception:** it has no `module "rds"` (it uses in-cluster Postgres).
- **No defaults for environment-specific values** (`environment`, CIDRs, node sizes, RDS settings). A missing value fails the plan instead of silently building dev resources.
- **Naming:** `${project_name}-${environment}-<resource>`, e.g. `eks-v2-staging-cluster`.



## Safety rails

- **State buckets** have versioning and `prevent_destroy`. Prod state is in its own bucket.
- **RDS prod precondition** (`modules/rds/main.tf`): prod must have Multi-AZ, deletion protection, a final snapshot and at least 7 days of backups. The plan fails otherwise. Instance class is not enforced — it is chosen in `terraform.tfvars`.


## Alerting

- The RDS module creates 4 CloudWatch alarms (CPU, freeable memory, free storage, connections), an RDS event subscription (failover, failure, maintenance, low storage) and an SNS topic.
- `alarm_email` subscribes an address to the topic. We use each Slack channel's email address.
- After the first apply, open the Slack channel and click **Confirm subscription**, or no alerts arrive.
- Alarm thresholds depend on the instance size; update `alarm_max_connections` and `alarm_min_freeable_memory_bytes` if `instance_class` changes.
