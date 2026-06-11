# TEO — Restore drill

**Audience:** operators of a self-hosted TEO deployment.
**Cadence:** quarterly minimum (per ADR-0017 RTO commitment).
**Goal:** rebuild a full TEO instance from cold backups and verify it boots, accepts a run, and serves the UI.

This drill exercises the same code paths a real disaster would hit. **Do not skip steps; record actual wall-clock time at each gate.**

---

## 0. Pre-flight (5 min)

- [ ] Confirm the backup workflows ran in the last 24h:
  - CloudNativePG `Backup` resource: `kubectl -n teo get backup -o wide`
  - `clickhouse-backup` last successful run: check the cron log
  - S3 versioning enabled on `teo-artifacts` bucket
- [ ] Pick a target namespace (`teo-restore`) — never restore over the live `teo` namespace.
- [ ] Have the operator-supplied secrets ready (`teo-jwt`, GitHub App key, OIDC client secret).

## 0b. Chart-render pre-check (offline — no AWS/cluster needed)

A cheap gate to run **before** you book cluster/AWS time: confirm the chart §4
installs renders cleanly in the drill's exact configuration
(`postgres.enabled=false`, `clickhouse.enabled=false`, since §2–§3 restore
those stores externally). It catches template/values regressions that would
otherwise blow up mid-drill, and needs nothing but `helm` + network.

```bash
helm repo add cloudnative-pg            https://cloudnative-pg.github.io/charts
helm repo add nats                      https://nats-io.github.io/k8s/helm/charts
helm repo add minio                     https://charts.min.io/
helm repo add dex                       https://charts.dexidp.io
helm repo add altinity-clickhouse-operator https://docs.altinity.com/clickhouse-operator/
helm repo update
helm dependency build deploy/helm/teo

helm lint     deploy/helm/teo --set postgres.enabled=false --set clickhouse.enabled=false
helm template teo deploy/helm/teo -n teo-restore \
  --set postgres.enabled=false --set clickhouse.enabled=false > /tmp/teo-render.yaml
```

**Gate:** `helm lint` reports 0 failed, `helm template` exits 0 with no
warnings, and the render contains the three core workloads:

```bash
grep -E 'name: (teo-api|teo-run-manager|teo-result-pipeline)$' /tmp/teo-render.yaml
```

Sanity-check that the disable flags did the right thing: there should be **no**
CloudNativePG `Cluster` CR and **no** Altinity/ClickHouse-operator resources in
the render, yet the workloads must still reference the externally-restored
`teo-postgres-creds` secret (`secretKeyRef … key: dsn`) — that's the seam §4
relies on.

Clean up the vendored deps afterward (`helm dependency build` writes
`charts/*.tgz` — already `.gitignore`d — plus a `Chart.lock`):

```bash
rm -rf deploy/helm/teo/charts deploy/helm/teo/Chart.lock
```

> **Scope:** this validates **chart rendering only** (§4). It does *not*
> exercise the Postgres PITR (§2), ClickHouse restore (§3), or the live smoke
> test (§5) — those need real AWS backups + a cluster and are the substance of
> the drill. Passing this pre-check is necessary, not sufficient.
>
> _Last run: 2026-06-10 — green (helm v4.0.5; lint 0 failed, template exit 0,
> all three workloads present, external creds wired)._

## 1. Provision a clean cluster (15 min)

We assume EKS; adapt for any 1.29+ cluster.

```bash
# Optional: a separate small EKS for drills
eksctl create cluster --name teo-restore --version 1.29 --nodes 3 --node-type t3.large
kubectl create namespace teo-restore
```

Add the Helm subchart repos and build deps:

```bash
helm repo add cloudnative-pg     https://cloudnative-pg.github.io/charts
helm repo add nats               https://nats-io.github.io/k8s/helm/charts
helm repo add minio              https://charts.min.io/
helm repo add dex                https://charts.dexidp.io
helm repo add clickhouse-operator https://docs.altinity.com/clickhouse-operator
helm repo update
helm dependency build deploy/helm/teo
```

## 2. Restore Postgres from PITR (10 min)

CloudNativePG's `Cluster` resource accepts a `bootstrap.recovery` block that
restores from the WAL archive in S3. Apply this manifest first (before
`helm install`):

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: teo-restore-postgres-cluster
  namespace: teo-restore
spec:
  instances: 2
  bootstrap:
    recovery:
      source: teo-prod
  externalClusters:
    - name: teo-prod
      barmanObjectStore:
        destinationPath: s3://teo-pg-backups
        s3Credentials:
          accessKeyId:    { name: pg-backup-creds, key: ACCESS_KEY_ID }
          secretAccessKey:{ name: pg-backup-creds, key: SECRET_ACCESS_KEY }
        wal:
          compression: gzip
```

Verify:

```bash
kubectl -n teo-restore wait --for=condition=Ready cluster/teo-restore-postgres-cluster --timeout=15m
kubectl -n teo-restore exec -it teo-restore-postgres-cluster-1 -- psql -U postgres -d teo \
  -c "SELECT count(*) FROM teo.runs;"
```

**Gate:** the row count matches your most-recent prod metric. If it doesn't, **stop the drill and investigate the backup chain.**

## 3. Restore ClickHouse (10 min)

```bash
# Pull the latest backup tarball (clickhouse-backup convention)
kubectl -n teo-restore run ch-restore --rm -i --tty \
  --image=altinity/clickhouse-backup:latest -- \
  bash -c "clickhouse-backup restore --rm 2026-04-30T02-00-00 --remote --resume"
```

Spot-check:

```bash
kubectl -n teo-restore exec -it chi-teo-0-0-0 -- clickhouse-client \
  -q "SELECT count(*) FROM teo.test_runs WHERE started_at > now() - interval 7 day"
```

If ClickHouse is **fully unrecoverable**, we accept reduced analytics (per
ADR-0017): the OLAP store is rebuildable from `teo.test_executions` in Postgres
via a one-shot job (TODO: ship as `teo backfill clickhouse` in a follow-up).

## 4. Install the chart (5 min)

```bash
helm install teo deploy/helm/teo -n teo-restore \
  --set postgres.enabled=false  \
  --set clickhouse.enabled=false \
  -f your-values.yaml
```

`postgres.enabled=false` here is critical — we restored Postgres ourselves in §2.
Same for ClickHouse. The chart's API/Run-Manager Deployments will pick up the
existing services via the `teo-restore-postgres-creds` Secret (which the CNPG
`Cluster` creates).

Watch for Ready:

```bash
kubectl -n teo-restore rollout status deploy/teo-api          --timeout=5m
kubectl -n teo-restore rollout status deploy/teo-run-manager  --timeout=5m
kubectl -n teo-restore rollout status deploy/teo-result-pipeline --timeout=5m
```

## 5. Smoke test (5 min)

```bash
# Port-forward and submit a tiny synthetic run
kubectl -n teo-restore port-forward svc/teo-api 8080:8080 &
PF=$!
sleep 2
curl -fsS -X POST http://localhost:8080/api/v1/runs \
  -H "Authorization: Bearer ${TEO_DRILL_API_KEY}" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: drill-$(date +%s)" \
  -d '{
    "repo_full_name": "teo-dev/restore-drill",
    "commit_sha":    "0000000000000000000000000000000000000000",
    "branch":        "main",
    "manifest":      {"runner": "pytest", "tests": [{"path": "smoke.py", "name": "test_ok"}]}
  }'
kill $PF
```

Expected: `201 Created` with a `pending` run that the Run Manager picks up
within 1s and walks to `failed` (no worker available — that's the smoke gate).
What we're verifying is the API + Run Manager + Postgres path; not actual
test execution.

## 6. UI sanity (2 min)

```bash
kubectl -n teo-restore port-forward svc/teo-web 3000:3000
```

Open http://localhost:3000/runs in a browser; the new run should appear with
`failed` status. Click into it — the Gantt page should render (empty shards is
expected for our smoke run).

## 7. Tear down

```bash
helm uninstall teo -n teo-restore
kubectl delete namespace teo-restore
# eksctl delete cluster --name teo-restore   # if you provisioned one
```

## 8. Recording results

Append to `docs/operations/restore-drill-history.md`:

```
| Date       | Operator | Wall-clock | Postgres rows lost | ClickHouse rebuilt | Notes |
|------------|----------|------------|---------------------|--------------------|-------|
| 2026-04-30 | alice    | 47 min     | 0                   | n/a                | first drill run |
```

If wall-clock exceeds the **1-hour RTO** target, file an issue tagged `dr-regression`.

---

## Failure modes seen in past drills

_Empty for now; populate as drills uncover real issues._
