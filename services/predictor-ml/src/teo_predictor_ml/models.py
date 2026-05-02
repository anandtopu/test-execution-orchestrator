"""Pydantic schemas for the predictor HTTP/gRPC surface."""

from __future__ import annotations

from typing import Sequence

from pydantic import BaseModel, Field


class TestEntry(BaseModel):
    """One test in the manifest."""

    path: str
    name: str
    params_hash: str = ""
    tags: list[str] = Field(default_factory=list)


class PredictRequest(BaseModel):
    repo_full_name: str
    tests: Sequence[TestEntry]


class Prediction(BaseModel):
    fingerprint: str
    p50_duration_ms: int
    p95_duration_ms: int
    flake_probability: float
    is_cold_start: bool
    model_version: str = "ml-v1"
    confidence: float = 0.0


class PredictResponse(BaseModel):
    predictions: list[Prediction]
    used_fallback: bool = False
    used_model_version: str = "ml-v1"


class HealthResponse(BaseModel):
    status: str
    model_age_seconds: float | None = None
    last_mae: float | None = None
    last_brier: float | None = None
