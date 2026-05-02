# Runbooks

The bundled Prometheus alerts in the Helm chart point `runbook_url` at this
directory. Each alert has its own page so the on-call link goes straight to
the diagnostic checklist — no extra clicks.

| Alert | File |
|---|---|
| `TeoApiHighLatency` | [api-latency.md](./api-latency.md) |
| `TeoApiErrorRateHigh` | [api-errors.md](./api-errors.md) |
| `TeoRunStuck` | [run-stuck.md](./run-stuck.md) |
| `TeoClickHouseInsertLag` | [clickhouse-lag.md](./clickhouse-lag.md) |
| `TeoNatsConsumerLag` | [nats-lag.md](./nats-lag.md) |
| `TeoControlPlaneReplicaDown` | [replica-down.md](./replica-down.md) |
