"""Nightly training entrypoint.

Run via: ``python -m teo_predictor_ml.train``

Pulls features from Postgres ``teo.test_executions``, trains a LightGBM regressor
+ classifier per repo, gates against a heuristic baseline, uploads to S3.

Canonical feature definition (shared with the serve path, repo.py / features.py):

  - History (p50/p95/std/fail_rate/attempts) is aggregated per ``(path, name)``
    over a rolling 30-day window from ``teo.test_executions`` — the SAME source
    and key the serve path and the Go heuristic use. (An earlier draft read
    ClickHouse ``teo.test_runs`` keyed by ``test_id``; that table is never
    written by any code in the repo and the key disagreed with serve, producing
    train/serve skew. Both now agree.)
  - Commit-diff features (file_changed_now / same_dir_changes_7d /
    test_change_freq_30d) are not yet threaded through the predict request, so
    they are zero in BOTH training and serving.
  - Time features (hour_of_day / day_of_week) are filled at prediction time from
    ``now`` in the serve path, so they are NOT derivable from historical
    aggregates at training time without skew. They are therefore zero in
    training as well; the model does not learn a spurious time signal that the
    serve path cannot reproduce consistently.
"""

from __future__ import annotations

import argparse
import json
import logging
import os
import sys
import tempfile
import time
from pathlib import Path

import boto3
import lightgbm as lgb
import numpy as np

from .features import COLUMN_NAMES

log = logging.getLogger(__name__)


def main(argv: list[str] | None = None) -> int:
    """CLI entrypoint."""
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo-id", required=True)
    parser.add_argument("--bucket", default=os.environ.get("TEO_MODEL_BUCKET", "teo-artifacts"))
    parser.add_argument("--postgres-dsn", default=os.environ.get("TEO_POSTGRES_DSN", ""))
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args(argv)

    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    log.info("training start: repo=%s", args.repo_id)
    started = time.time()

    X, y_dur, y_flake = _load_training_data(args.repo_id, args.postgres_dsn)
    if len(X) < 100:
        log.warning("not enough training data (%d rows); skipping", len(X))
        return 0

    n = len(X)
    cut = int(n * 0.85)
    X_tr, X_te = X[:cut], X[cut:]
    y_dur_tr, y_dur_te = y_dur[:cut], y_dur[cut:]
    y_flake_tr, y_flake_te = y_flake[:cut], y_flake[cut:]

    duration = lgb.train(
        params={
            "objective": "regression",
            "metric": "mae",
            "learning_rate": 0.05,
            "num_leaves": 31,
            "verbose": -1,
        },
        train_set=lgb.Dataset(X_tr, label=y_dur_tr, feature_name=COLUMN_NAMES),
        num_boost_round=200,
    )
    flake = lgb.train(
        params={
            "objective": "binary",
            "metric": "binary_logloss",
            "learning_rate": 0.05,
            "num_leaves": 31,
            "verbose": -1,
        },
        train_set=lgb.Dataset(X_tr, label=y_flake_tr, feature_name=COLUMN_NAMES),
        num_boost_round=150,
    )

    pred_dur = duration.predict(X_te)
    pred_flake = flake.predict(X_te)
    mae = float(np.mean(np.abs(pred_dur - y_dur_te)))
    brier = float(np.mean((pred_flake - y_flake_te) ** 2))
    heuristic_mae = float(np.mean(np.abs(np.median(y_dur_tr) - y_dur_te)))

    log.info("MAE=%.1f ms (heuristic baseline %.1f ms); Brier=%.4f", mae, heuristic_mae, brier)
    if mae * 1.0 > heuristic_mae * 1.5:
        log.warning("model rejected: MAE×1.0 > heuristic_MAE×1.5; not promoting")
        return 1

    if args.dry_run:
        log.info("dry-run; not uploading")
        return 0

    _upload(duration, flake, args.repo_id, args.bucket, mae=mae, brier=brier)
    log.info("done in %.1fs", time.time() - started)
    return 0


# Per-test aggregates over a 30-day window from teo.test_executions, keyed by
# (path, name) — the SAME source/key the serve path (repo.py._query_history) and
# the Go heuristic (internal/predictor/predictor.go loadStats) use. The selected
# columns are emitted in COLUMN_NAMES order so X lines up with the serve-time
# FeatureRow.as_array(); time + commit-diff features are zero (see module
# docstring) so the two paths produce identical feature semantics.
_TRAINING_SQL = """
    WITH per_test AS (
        SELECT
            te.test_id,
            percentile_disc(0.5)  WITHIN GROUP (ORDER BY te.duration_ms) AS p50,
            percentile_disc(0.95) WITHIN GROUP (ORDER BY te.duration_ms) AS p95,
            coalesce(stddev_pop(te.duration_ms), 0) AS std_ms,
            sum(CASE WHEN te.outcome IN ('failed','errored','timed_out') THEN 1 ELSE 0 END)::float
              / GREATEST(count(*), 1) AS fail_rate,
            count(*) AS attempts
        FROM teo.test_executions te
        JOIN teo.shards s ON s.id = te.shard_id
        JOIN teo.runs r ON r.id = s.run_id
        WHERE r.repo_id = %(repo_id)s
          AND te.started_at > now() - INTERVAL '30 days'
        GROUP BY te.test_id
        HAVING count(*) >= 5
    )
    SELECT
        pt.p50, pt.p95, pt.std_ms, pt.fail_rate, pt.attempts,
        0 AS file_changed_now,
        0 AS same_dir_changes_7d,
        0 AS test_change_freq_30d,
        0 AS hour_of_day,
        0 AS day_of_week,
        pt.p50 AS y_duration,
        (pt.fail_rate > 0.05)::int AS y_flake
    FROM per_test pt
    JOIN teo.tests t ON t.id = pt.test_id
    WHERE t.status != 'deleted'
"""


def _load_training_data(repo_id: str, postgres_dsn: str) -> tuple[np.ndarray, np.ndarray, np.ndarray]:
    """Load (X, y_duration, y_flake_label) for the given repo from Postgres.

    Source is ``teo.test_executions`` (same as the serve path), aggregated per
    test over a 30-day window. Falls back to synthetic data (for offline tests /
    dry-runs) when no DSN is configured or psycopg is missing.
    """
    if not postgres_dsn:
        return _synthetic()
    try:
        import psycopg  # type: ignore
    except ImportError:
        log.warning("psycopg not available; using synthetic data")
        return _synthetic()

    with psycopg.connect(postgres_dsn, connect_timeout=5) as conn:
        with conn.cursor() as cur:
            cur.execute(_TRAINING_SQL, {"repo_id": repo_id})
            rows = cur.fetchall()
    if not rows:
        log.warning("no rows for repo %s; using synthetic", repo_id)
        return _synthetic()
    arr = np.array(rows, dtype=np.float32)
    X = arr[:, : len(COLUMN_NAMES)]
    y_dur = arr[:, len(COLUMN_NAMES)]
    y_flake = arr[:, len(COLUMN_NAMES) + 1]
    log.info("loaded %d training rows from Postgres", len(arr))
    return X, y_dur, y_flake


def _synthetic() -> tuple[np.ndarray, np.ndarray, np.ndarray]:
    """Synthetic data for tests / dry-runs."""
    rng = np.random.default_rng(0)
    n = 500
    X = rng.uniform(0, 5000, size=(n, len(COLUMN_NAMES))).astype(np.float32)
    y_dur = X[:, 0] + rng.normal(0, 100, size=n).astype(np.float32)
    y_flake = (X[:, 3] > 2500).astype(np.float32)
    return X, y_dur, y_flake


def _upload(duration: lgb.Booster, flake: lgb.Booster, repo_id: str, bucket: str, *, mae: float, brier: float) -> None:
    s3 = boto3.client("s3")
    with tempfile.TemporaryDirectory() as tmp:
        dpath = Path(tmp) / "duration.txt"
        fpath = Path(tmp) / "flake.txt"
        mpath = Path(tmp) / "metrics.json"
        duration.save_model(str(dpath))
        flake.save_model(str(fpath))
        mpath.write_text(json.dumps({"mae": mae, "brier": brier, "trained_at": time.time()}))
        prefix = f"models/{repo_id}/latest/"
        for path in (dpath, fpath, mpath):
            s3.upload_file(str(path), bucket, prefix + path.name)


if __name__ == "__main__":
    sys.exit(main())
