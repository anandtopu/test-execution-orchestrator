"""FastAPI HTTP surface for the ML predictor.

Mirrors the gRPC contract in proto/teo/v1/predictor.proto. The Run Manager
calls gRPC; this HTTP wrapper exists so that operators can curl-debug.
"""

from __future__ import annotations

import logging
import os
import time

import numpy as np
from fastapi import FastAPI

from .features import FeatureRow, empty
from .models import (
    HealthResponse,
    PredictRequest,
    PredictResponse,
    Prediction,
)
from .registry import ModelRegistry, from_env

log = logging.getLogger(__name__)

app = FastAPI(title="TEO ML predictor", version=os.environ.get("TEO_VERSION", "dev"))
_started = time.time()
_registry: ModelRegistry | None = None


def _registry_singleton() -> ModelRegistry:
    global _registry
    if _registry is None:
        _registry = from_env()
    return _registry


@app.get("/healthz", response_model=HealthResponse)
def healthz() -> HealthResponse:
    return HealthResponse(status="ok")


@app.get("/readyz", response_model=HealthResponse)
def readyz() -> HealthResponse:
    return HealthResponse(status="ok", model_age_seconds=time.time() - _started)


@app.post("/v1/predict", response_model=PredictResponse)
def predict(req: PredictRequest) -> PredictResponse:
    """Predict per-test duration + flake probability.

    Falls back gracefully when the per-repo model is not yet trained or
    cannot be loaded — returns cold-start defaults so the Run Manager
    receives a usable response.
    """
    registry = _registry_singleton()
    repo_id = _resolve_repo_id(req.repo_full_name)
    model = registry.get(repo_id) if repo_id else None

    out: list[Prediction] = []
    if model is None:
        for t in req.tests:
            out.append(_cold_start(t))
        return PredictResponse(predictions=out, used_fallback=True, used_model_version="cold-start")

    rows = np.array([_features_for(t).as_array() for t in req.tests])
    durations = model.duration_regressor.predict(rows)
    flake_probs = model.flake_classifier.predict(rows)
    for i, t in enumerate(req.tests):
        p50 = max(50, int(durations[i]))
        out.append(
            Prediction(
                fingerprint=f"{t.path}::{t.name}",
                p50_duration_ms=p50,
                p95_duration_ms=p50 * 3,
                flake_probability=float(flake_probs[i]),
                is_cold_start=False,
                model_version="ml-v1",
                confidence=1.0 - abs(0.5 - float(flake_probs[i])) * 2,
            )
        )
    return PredictResponse(predictions=out, used_fallback=False, used_model_version="ml-v1")


def _cold_start(t):
    return Prediction(
        fingerprint=f"{t.path}::{t.name}",
        p50_duration_ms=1200,
        p95_duration_ms=3600,
        flake_probability=0.0,
        is_cold_start=True,
        model_version="cold-start",
    )


def _features_for(_t) -> FeatureRow:
    # In production we'd query ClickHouse for per-test history. For now we emit
    # zero features and let the model's bias term carry the prediction. This is
    # explicit so feature extraction can be added incrementally without
    # changing the gRPC contract.
    return empty()


def _resolve_repo_id(_full_name: str) -> str | None:
    """Return the repo UUID. In a real deployment we read from Postgres."""
    return None
