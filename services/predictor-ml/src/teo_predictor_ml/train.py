"""Nightly training entrypoint.

Run via: ``python -m teo_predictor_ml.train``

Pulls features from ClickHouse, trains LightGBM regressor + classifier per repo,
gates against a heuristic baseline, uploads to S3.
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
    parser.add_argument("--clickhouse-dsn", default=os.environ.get("TEO_CLICKHOUSE_DSN", ""))
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args(argv)

    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    log.info("training start: repo=%s", args.repo_id)
    started = time.time()

    X, y_dur, y_flake = _load_training_data(args.repo_id, args.clickhouse_dsn)
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


def _load_training_data(repo_id: str, clickhouse_dsn: str) -> tuple[np.ndarray, np.ndarray, np.ndarray]:
    """Load (X, y_duration, y_flake_label) for the given repo from ClickHouse.

    Falls back to synthetic data (for offline tests / dry-runs) when no DSN
    is configured or the driver is missing.
    """
    if not clickhouse_dsn:
        return _synthetic()
    try:
        from clickhouse_driver import Client  # type: ignore
    except ImportError:
        log.warning("clickhouse_driver not available; using synthetic data")
        return _synthetic()

    parsed = _parse_dsn(clickhouse_dsn)
    client = Client(**parsed)
    sql = """
        WITH per_test AS (
            SELECT
                test_id,
                quantile(0.5)(duration_ms) AS p50,
                quantile(0.95)(duration_ms) AS p95,
                stddevPop(duration_ms) AS std_ms,
                countIf(outcome IN ('failed','errored','timed_out')) / count() AS fail_rate,
                count() AS attempts,
                argMax(toHour(started_at), started_at) AS hour_of_day,
                argMax(toDayOfWeek(started_at), started_at) AS day_of_week
            FROM teo.test_runs
            WHERE repo_id = %(repo_id)s
              AND started_at > now() - INTERVAL 30 DAY
            GROUP BY test_id
        )
        SELECT
            p50, p95, std_ms, fail_rate, attempts,
            0 AS file_changed_now,
            0 AS same_dir_changes_7d,
            0 AS test_change_freq_30d,
            hour_of_day, day_of_week,
            p50 AS y_duration,
            (fail_rate > 0.05) AS y_flake
        FROM per_test
        WHERE attempts >= 5
    """
    rows = client.execute(sql, {"repo_id": repo_id})
    if not rows:
        log.warning("no rows for repo %s; using synthetic", repo_id)
        return _synthetic()
    arr = np.array(rows, dtype=np.float32)
    X = arr[:, : len(COLUMN_NAMES)]
    y_dur = arr[:, len(COLUMN_NAMES)]
    y_flake = arr[:, len(COLUMN_NAMES) + 1]
    log.info("loaded %d training rows from ClickHouse", len(arr))
    return X, y_dur, y_flake


def _synthetic() -> tuple[np.ndarray, np.ndarray, np.ndarray]:
    """Synthetic data for tests / dry-runs."""
    rng = np.random.default_rng(0)
    n = 500
    X = rng.uniform(0, 5000, size=(n, len(COLUMN_NAMES))).astype(np.float32)
    y_dur = X[:, 0] + rng.normal(0, 100, size=n).astype(np.float32)
    y_flake = (X[:, 3] > 2500).astype(np.float32)
    return X, y_dur, y_flake


def _parse_dsn(dsn: str) -> dict[str, object]:
    """Parse a clickhouse://user:pass@host:port/db DSN into Client kwargs."""
    from urllib.parse import urlparse

    u = urlparse(dsn)
    out: dict[str, object] = {
        "host": u.hostname or "localhost",
        "port": u.port or 9000,
    }
    if u.username:
        out["user"] = u.username
    if u.password:
        out["password"] = u.password
    if u.path and len(u.path) > 1:
        out["database"] = u.path[1:]
    return out


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
