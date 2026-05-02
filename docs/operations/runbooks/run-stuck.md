# Runbook: TeoRunStuck

**Symptom:** one or more TEO runs have been in a non-terminal status (`pending`, `planning`, `dispatching`, `running`, `finalizing`) for longer than `2 × budget_seconds`.

## What it usually means

1. **Worker pool saturated** — every worker is busy and the run is starved.
2. **Worker crash + lost-shard reschedule loop** — a flaky test repeatedly preempts and retries.
3. **Run Manager replicas can't acquire the advisory lock** — both replicas crash-looping.
4. **Predictor is down** and no fallback fired — `pending` runs accumulate.

## Diagnose

```bash
# 1. List the stuck runs
kubectl -n teo exec -it deploy/teo-api -- psql ${TEO_POSTGRES_DSN} \
  -c "SELECT id, repo_id, status, started_at, budget_seconds, now() - started_at AS age
      FROM teo.runs WHERE status NOT IN ('succeeded','failed','cancelled')
                      AND started_at IS NOT NULL
                      AND now() - started_at > make_interval(secs => budget_seconds * 2);"

# 2. Are workers healthy?
kubectl -n teo get pods -l app.kubernetes.io/component=worker
kubectl -n teo top pods -l app.kubernetes.io/component=worker

# 3. Run Manager replicas?
kubectl -n teo get pods -l app.kubernetes.io/component=run-manager
kubectl -n teo logs -l app.kubernetes.io/component=run-manager --tail=200 | grep -E "(error|stuck|lock)"

# 4. Predictor health
kubectl -n teo get pods -l app.kubernetes.io/component=predictor
kubectl -n teo logs deploy/teo-predictor-ml --tail=100
```

## Fix

- **Stuck `pending`**: cancel via `POST /api/v1/runs/<id>/cancel` from a privileged token, then resubmit. Investigate why the planner didn't pick it up.
- **Stuck `running`**: workers are gone; scale the worker Deployment up or wait for Karpenter to bring on-demand nodes online.
- **Stuck `finalizing`**: ClickHouse insert lag is the usual cause — see [clickhouse-lag.md](./clickhouse-lag.md).
- **Run Manager wedged**: `kubectl rollout restart deploy/teo-run-manager`. Lease holders re-acquire within 10s per ADR-0013.

## Verify

After action, the alert clears within 5 minutes (the alert's `for: 5m`). Confirm with:

```bash
kubectl -n teo exec -it deploy/teo-api -- psql ${TEO_POSTGRES_DSN} \
  -c "SELECT count(*) FROM teo.runs WHERE status NOT IN ('succeeded','failed','cancelled') AND now() - started_at > make_interval(secs => budget_seconds * 2);"
```

Should return `0`.
