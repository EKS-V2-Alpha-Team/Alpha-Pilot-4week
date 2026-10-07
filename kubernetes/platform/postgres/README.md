# Postgres

In-cluster PostgreSQL for the dev environment. Runs as a single StatefulSet in `order-platform` with a 20Gi encrypted gp3 volume. Production will continue to use RDS.

| File | What |
|---|---|
| `statefulset.yaml` | PostgreSQL pod, health checks and persistent 20Gi storage |
| `service.yaml` | `postgres` for application traffic and `postgres-headless` for StatefulSet identity |
| `../apps/postgres.yaml` | ArgoCD Application, sync wave `-1` |
| `../manifests/postgres-external-secret.yaml` | Creates `postgres-credentials` from AWS Secrets Manager |
| `../manifests/storageclass-gp3.yaml` | Encrypted gp3 StorageClass |

## Important design points

- Applications connect through `postgres.order-platform.svc.cluster.local:5432`.
- `postgres-headless` is used by the StatefulSet for stable pod network identity.
- Database username and password come from `eks-v2-dev-db-credentials` through External Secrets.
- The `db-connection` secret stores the Postgres host and port because the shared Helm chart already reads database connection details from Secrets Manager.
- Dev uses `sslMode: disable`; production RDS uses SSL.
- `PGDATA` uses `/var/lib/postgresql/data/pgdata` so PostgreSQL initialises inside a clean subdirectory of the persistent disk.
- The PVC uses `ReadWriteOnce` and is retained if the StatefulSet is deleted or scaled down.
- ArgoCD manages the Postgres manifests with automated sync and self-healing.
