# ADR-0019: LightGBM GBT predictor in a Python service

**Status:** Accepted
**Date:** 2026-04-30

## Context
The PRD §7 (Predictor) and §6 (#3 failure-first ordering) describe a model that predicts per-test duration (p50, p95) and flake probability from history, code-churn, and message-clustering signals. The earlier 1-month scope cut deferred this. With 3 months we ship it in v1.0.

## Decision
- **Model:** LightGBM gradient-boosted trees, separate models for `duration_regressor` and `flake_classifier`.
- **Service:** Python 3.12 + FastAPI, serving the same gRPC `Predictor` contract as the heuristic. Workers/Run Manager call gRPC; never know which implementation answered.
- **Training:** nightly Cron Job, reads from ClickHouse, writes the model artifact to S3 (`teo-artifacts/models/<repo_id>/<yyyy-mm-dd>/`). Per-repo models for the regressor; a single global model for the flake classifier (sparse positive class per repo).
- **Features (v1.0):**
  - Test history: last-30-day p50, p95, std-dev, fail rate, attempt count.
  - Test path features: file changed in this commit (binary), files in same directory changed, change frequency in last 30 days.
  - Worker context: instance type, container image hash.
  - Time-of-day, day-of-week.
- **Features (v1.5):** code embeddings (deferred per PRD §11).
- **Fallback:** if the Python service is down, MAE drifts beyond threshold, or the per-repo model is missing, the Run Manager calls the **Go heuristic predictor** (rolling mean) instead. This is a non-negotiable property: the system runs without ML.

## Consequences

**+** Cold-start performance is materially better than rolling mean for repos with at least 1-2 weeks of history.
**+** Failure-first ordering (PRD §6 #3) becomes useful with calibrated flake probability.
**+** Fallback path keeps the system operational under predictor outage; OSS operators can disable the Python service entirely if they prefer.

**−** Second runtime (Python) in the chart. Mitigated by making it optional and fallback-graceful.
**−** Model training failures need monitoring; we ship Prometheus metrics for `model_age_seconds`, `train_job_failures_total`, `prediction_mae`.
**−** Per-repo models fragment as the repo count grows; v1.0 ships per-repo only because expected operator scale is small. v1.5 may consolidate.

## Calibration & evaluation
- Holdout: temporal split (last 7 days of history reserved for evaluation).
- MAE on `duration_regressor` measured against the holdout; if MAE × 1.5 > heuristic-baseline MAE, the model is **rejected** for that repo and the heuristic is used.
- Brier score on `flake_classifier`; reject if Brier > heuristic baseline.
- Champion/challenger: every nightly retrain produces a candidate; gating job validates against holdout before promoting.

## Alternatives considered
- **Heuristic predictor only at v1.0** (the old scope cut). Rejected per ADR-0012 revision.
- **Neural net (PyTorch).** Rejected: training cost, dependency footprint, no measurable lift over GBT for tabular data.
- **scikit-learn GBT.** Acceptable; LightGBM picked for training speed on large feature tables.
- **Embed the predictor in the Run Manager (Go).** Rejected: dependent on a Go GBT library that's significantly less maintained than LightGBM.
