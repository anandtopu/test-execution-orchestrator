# Runbook: TeoControlPlaneReplicaDown

**Symptom:** a control-plane Deployment (`teo-api`, `teo-run-manager`, `teo-result-pipeline`, `teo-web`) has fewer ready replicas than desired for 5 minutes.

## Diagnose

```bash
kubectl -n teo get pods -l app.kubernetes.io/part-of=teo
kubectl -n teo describe deploy/<name>
kubectl -n teo logs deploy/<name> --tail=300
kubectl -n teo get events --sort-by='.lastTimestamp' | tail -30
```

## Common patterns

- `CrashLoopBackOff` — usually a missing env var (DSN, JWT secret) or a bad Secret. The Pod logs print the missing key on startup.
- `ImagePullBackOff` — image isn't pushed yet, or pull credentials missing. Check the chart `global.image.registry` / `namespace`.
- `OOMKilled` — bump the Deployment resources block in `values.yaml`.
- Node pressure — see Karpenter / cluster-autoscaler health.

## Fix

Restart after fixing the root cause:

```bash
kubectl -n teo rollout restart deploy/<name>
kubectl -n teo rollout status deploy/<name>
```

Run Manager specifically uses Postgres advisory locks per ADR-0013, so failover is automatic; the alert clears as soon as the second replica is healthy.

## Verify

Replicas Ready = Desired for at least 5 minutes; alert clears.
