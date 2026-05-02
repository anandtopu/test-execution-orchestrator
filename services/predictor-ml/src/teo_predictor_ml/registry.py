"""Model registry: loads per-repo LightGBM artifacts from S3."""

from __future__ import annotations

import io
import logging
import os
import threading
import time
from dataclasses import dataclass

import boto3
import lightgbm as lgb

log = logging.getLogger(__name__)


@dataclass
class LoadedModel:
    """A loaded per-repo model bundle."""

    repo_id: str
    duration_regressor: lgb.Booster
    flake_classifier: lgb.Booster
    loaded_at: float
    holdout_mae: float
    holdout_brier: float


class ModelRegistry:
    """Loads model artifacts from S3 and serves them to the request path.

    Per-repo cache with bounded TTL. On miss, returns None and the caller falls
    through to the heuristic.
    """

    def __init__(self, *, bucket: str, ttl_seconds: float = 600.0):
        self._bucket = bucket
        self._ttl = ttl_seconds
        self._cache: dict[str, LoadedModel] = {}
        self._lock = threading.RLock()
        self._s3 = boto3.client("s3")

    def get(self, repo_id: str) -> LoadedModel | None:
        with self._lock:
            cached = self._cache.get(repo_id)
            if cached is not None and time.time() - cached.loaded_at < self._ttl:
                return cached

        loaded = self._load(repo_id)
        if loaded is None:
            return None
        with self._lock:
            self._cache[repo_id] = loaded
        return loaded

    def _load(self, repo_id: str) -> LoadedModel | None:
        prefix = f"models/{repo_id}/latest/"
        try:
            duration_obj = self._s3.get_object(Bucket=self._bucket, Key=prefix + "duration.txt")
            flake_obj = self._s3.get_object(Bucket=self._bucket, Key=prefix + "flake.txt")
            metrics_obj = self._s3.get_object(Bucket=self._bucket, Key=prefix + "metrics.json")
        except Exception as e:
            log.warning("model load failed for %s: %s", repo_id, e)
            return None
        import json
        metrics = json.loads(metrics_obj["Body"].read())
        duration = lgb.Booster(model_str=duration_obj["Body"].read().decode("utf-8"))
        flake = lgb.Booster(model_str=flake_obj["Body"].read().decode("utf-8"))
        return LoadedModel(
            repo_id=repo_id,
            duration_regressor=duration,
            flake_classifier=flake,
            loaded_at=time.time(),
            holdout_mae=float(metrics.get("mae", 0.0)),
            holdout_brier=float(metrics.get("brier", 0.0)),
        )


def from_env() -> ModelRegistry:
    bucket = os.environ.get("TEO_MODEL_BUCKET", "teo-artifacts")
    ttl = float(os.environ.get("TEO_MODEL_TTL_SECONDS", "600"))
    return ModelRegistry(bucket=bucket, ttl_seconds=ttl)
