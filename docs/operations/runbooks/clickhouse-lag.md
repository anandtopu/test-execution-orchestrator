# Runbook: TeoClickHouseInsertLag

**Symptom:** result-pipeline insert latency p95 above the threshold (default 30s) for 5 minutes.

## Likely causes

1. **OTLP burst** outpaces the per-row insert path (known follow-up — batched insert is named in `progress.md`).
2. **ClickHouse merge backlog**: too many small parts.
3. **Disk full** on the CH PVC.

## Diagnose

```bash
# CH cluster health
kubectl -n teo exec -it chi-teo-0-0-0 -- clickhouse-client \
  -q "SELECT database, table, count() AS parts, sum(bytes_on_disk) FROM system.parts WHERE active GROUP BY database, table ORDER BY parts DESC LIMIT 10"

# PVC usage
kubectl -n teo top pod chi-teo-0-0-0
kubectl -n teo describe pvc -l app=clickhouse | grep -A 2 "Capacity"
```

## Fix

- Many small parts → wait for natural merges or trigger `OPTIMIZE TABLE teo.span_events FINAL` (expensive; off-hours).
- Disk full → expand the PVC (`kubectl edit pvc ...`); ClickHouse re-mounts on restart.
- Sustained burst → tune the result-pipeline batch size (env knob lands with the batched-insert follow-up).

## Verify

Insert lag p95 returns below threshold; alert clears.
