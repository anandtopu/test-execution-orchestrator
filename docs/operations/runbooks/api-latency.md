# Runbook: TeoApiHighLatency

**Symptom:** API gateway p95 latency above the configured threshold (default 0.5s) for 5 minutes.

## Likely causes

1. **Postgres saturated** — long-running query or pool exhaustion.
2. **GC pressure** in the API process — sudden traffic spike.
3. **Auth path slow** — the API-key cache miss rate is high (argon2id is intentionally expensive).

## Diagnose

```bash
# 1. Which endpoint?
# Inspect the bundled "API latency" Grafana dashboard, panel "p95 latency by endpoint".

# 2. Postgres connection pool
kubectl -n teo exec -it deploy/teo-api -- \
  psql ${TEO_POSTGRES_DSN} -c "SELECT state, count(*) FROM pg_stat_activity WHERE application_name = 'teo' GROUP BY state;"

# 3. API-key cache hit rate
kubectl -n teo logs deploy/teo-api --tail=500 | grep -c "api_key_cache_miss"
```

## Fix

- **Pool exhaustion**: bump `internal/db/db.go` MaxConns (currently 25) or scale API replicas.
- **Hot endpoint**: identify in the dashboard, file an issue with a profile attached (`/debug/pprof/profile?seconds=30`).
- **Argon2id storms**: increase the API-key cache TTL beyond 30s. We chose 30s for FR-805 (revoke within 30s); operators can raise it if they're willing to widen that window.

## Verify

p95 latency drops below threshold within 5 minutes; alert clears.
