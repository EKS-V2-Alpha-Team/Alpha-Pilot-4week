# Postgres

In-cluster PostgreSQL for the dev environment. Runs as a single StatefulSet in `order-platform` with a 20Gi encrypted gp3 volume. Production will continue to use RDS.

| File | What |
|---|---|
| `statefulset.yaml` | PostgreSQL pod, health checks and persistent 20Gi storage |
| `service.yaml` | `postgres` for application traffic and `postgres-headless` for StatefulSet identity |
| `snapshot-schedule.yaml` | Daily EBS snapshot of the Postgres volume at 01:00 UTC (02:00 UK in summer), keeps the last 7 (SnapScheduler) |
| `../manifests/volumesnapshotclass-ebs.yaml` | Default VolumeSnapshotClass `ebs-csi` used by the schedule |
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

## Snapshots

The snapshot-controller EKS add-on (Terraform) installs the VolumeSnapshot CRDs. SnapScheduler (`../apps/snapscheduler.yaml`) creates a `VolumeSnapshot` of `postgres-data-postgres-0` every day and deletes the oldest past 7. Each one is a real EBS snapshot (`deletionPolicy: Delete`, so retention removes the EBS snapshot too).

Snapshots are crash-consistent: restoring one is like Postgres restarting after a power cut, and it replays its WAL on startup.

```bash
kubectl -n order-platform get snapshotschedule postgres-daily     # NEXTSNAPSHOT shows the next run
kubectl -n order-platform get volumesnapshot                      # READYTOUSE true once EBS finishes
```

### Take one now (before a risky change)

```bash
kubectl apply -f - <<YAML
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: postgres-manual-$(date +%Y%m%d%H%M)
  namespace: order-platform
spec:
  volumeSnapshotClassName: ebs-csi
  source:
    persistentVolumeClaimName: postgres-data-postgres-0
YAML
```

Manual snapshots aren't counted by the schedule's retention; delete them when done.

### Restore test (doesn't touch the live database)

Restores a snapshot into a new volume, starts a throwaway Postgres on it and reads the data.

```bash
SNAP=<name from: kubectl -n order-platform get volumesnapshot>
kubectl apply -f - <<YAML
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: postgres-restore-test
  namespace: order-platform
spec:
  storageClassName: gp3
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 20Gi
  dataSource:
    apiGroup: snapshot.storage.k8s.io
    kind: VolumeSnapshot
    name: $SNAP
---
apiVersion: v1
kind: Pod
metadata:
  name: postgres-restore-test
  namespace: order-platform
spec:
  containers:
    - name: postgres
      image: postgres:16-alpine
      env:
        - name: PGDATA
          value: /var/lib/postgresql/data/pgdata
      volumeMounts:
        - name: data
          mountPath: /var/lib/postgresql/data
  volumes:
    - name: data
      persistentVolumeClaim:
        claimName: postgres-restore-test
YAML
kubectl -n order-platform wait pod/postgres-restore-test --for=condition=Ready --timeout=5m
kubectl -n order-platform exec postgres-restore-test -- psql -U dbadmin -d orders -c 'SELECT count(*) FROM orders;'
```

The count should match the live database at the time of the snapshot. Clean up (deleting the PVC deletes its EBS volume, not the snapshot):

```bash
kubectl -n order-platform delete pod/postgres-restore-test pvc/postgres-restore-test
```

### Restore the live database

Replaces the live volume with a snapshot. Everything written after the snapshot is lost.

1. Turn off auto-sync on the `postgres` ArgoCD app, or it scales the StatefulSet straight back up.
2. `kubectl -n order-platform scale statefulset postgres --replicas=0`
3. `kubectl -n order-platform delete pvc postgres-data-postgres-0` (deletes the current EBS volume)
4. Create a PVC named `postgres-data-postgres-0` with the same spec and `dataSource` as the restore test above.
5. `kubectl -n order-platform scale statefulset postgres --replicas=1`, check the data, then turn auto-sync back on.
