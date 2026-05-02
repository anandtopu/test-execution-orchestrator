# Runbook: TeoNatsConsumerLag

**Symptom:** a JetStream consumer's pending-message count stays above the threshold (default 10K) for 5 minutes.

## Diagnose

```bash
# Per-consumer state
kubectl -n teo exec -it teo-nats-0 -- nats consumer ls TEO_SHARDS
kubectl -n teo exec -it teo-nats-0 -- nats consumer info TEO_SHARDS teo-worker-<host>

# Worker pool health
kubectl -n teo get pods -l app.kubernetes.io/component=worker
```

## Fix

- **Worker shortage**: scale the worker Deployment, or wait for Karpenter to bring up nodes.
- **Stuck consumer**: the worker pod is alive but not pulling. Check its logs for "context deadline" or repeated `NakWithDelay`.
- **Single bad message** poisoning the queue: rare; the consumer max-deliver bumps it to DLQ — inspect via `nats stream view TEO_SHARDS`.

Workers fall back to Postgres SKIP-LOCKED claim if NATS is unavailable, so users still get throughput — just reduced.

## Verify

Pending count drops below threshold; alert clears.
