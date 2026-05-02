# Runbook: TeoApiErrorRateHigh

**Symptom:** 5xx response rate from `teo-api` above 5% for 10 minutes.

## Diagnose

```bash
kubectl -n teo logs deploy/teo-api --tail=300 | grep -E '"level":"(error|warn)"' | head -50
kubectl -n teo describe deploy/teo-api | grep -A 5 "Conditions"
```

Common patterns:
- `"err":"pool acquire"` — Postgres pool exhausted, see [api-latency.md](./api-latency.md).
- `"err":"context deadline exceeded"` — downstream service (predictor, ClickHouse) is slow.
- 5xx specifically on `/graphql` — see graphql resolver logs and [clickhouse-lag.md](./clickhouse-lag.md) if reads are involved.

## Fix

Triage by stack class. If you see correlated alerts (Postgres or ClickHouse), fix those first — the API errors are usually downstream.

## Verify

5xx rate falls below 1% sustained for 10 minutes; alert clears.
