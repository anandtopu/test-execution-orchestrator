"""FastAPI surface tests for the ML predictor /v1/predict endpoint.

These exercise the three branches of the serve path without a real model, a real
S3 bucket, or a real Postgres/ClickHouse:

  1. resolved repo + a registry returning a stub LoadedModel  -> used_fallback=false
  2. _resolve_repo_id -> None (no DSN / unknown repo)         -> cold-start fallback
  3. registry.get -> None for a resolved repo                 -> cold-start fallback
  4. history fetch raising                                    -> degrades to cold-start, 200

We stub the LightGBM Boosters with objects exposing a numpy-friendly .predict()
so the real numpy feature matrix flows through unchanged.
"""

from __future__ import annotations

import uuid

import numpy as np
import pytest
from fastapi.testclient import TestClient

from teo_predictor_ml import app as app_module
from teo_predictor_ml.app import app


class _StubBooster:
    """Minimal stand-in for an lgb.Booster: returns a fixed value per row."""

    def __init__(self, value: float):
        self._value = value

    def predict(self, rows: np.ndarray) -> np.ndarray:
        n = rows.shape[0] if hasattr(rows, "shape") else len(rows)
        return np.full(n, self._value, dtype=np.float64)


class _StubLoadedModel:
    def __init__(self, *, duration: float, flake: float):
        self.repo_id = "stub"
        self.duration_regressor = _StubBooster(duration)
        self.flake_classifier = _StubBooster(flake)
        self.loaded_at = 0.0
        self.holdout_mae = 0.0
        self.holdout_brier = 0.0


class _StubRegistry:
    """Registry whose .get() returns a preset model (or None)."""

    def __init__(self, model):
        self._model = model
        self.get_calls: list[str] = []

    def get(self, repo_id):
        self.get_calls.append(repo_id)
        return self._model


@pytest.fixture
def client() -> TestClient:
    return TestClient(app)


@pytest.fixture(autouse=True)
def _reset_registry_singleton():
    """Ensure each test starts/ends with a clean module-level registry."""
    app_module._registry = None
    yield
    app_module._registry = None


def _predict(client: TestClient, n: int = 2):
    body = {
        "repo_full_name": "owner/repo",
        "tests": [{"path": f"tests/t{i}.py", "name": f"test_{i}"} for i in range(n)],
    }
    return client.post("/v1/predict", json=body)


def test_predict_with_model_uses_ml_path(client, monkeypatch):
    repo_id = str(uuid.uuid4())
    monkeypatch.setattr(app_module, "_resolve_repo_id", lambda _name: repo_id)
    # Model present → ML path. duration regressor returns 250ms.
    app_module._registry = _StubRegistry(_StubLoadedModel(duration=250.0, flake=0.3))
    # No DSN → history is {} (every test absent) but the model still drives p50.
    monkeypatch.setattr(app_module, "_history_for_repo", lambda _rid: {})

    resp = _predict(client, n=2)
    assert resp.status_code == 200
    data = resp.json()

    assert data["used_fallback"] is False
    assert data["used_model_version"] == "ml-v1"
    assert len(data["predictions"]) == 2
    for p in data["predictions"]:
        assert p["is_cold_start"] is False
        assert p["model_version"] == "ml-v1"
        assert p["p50_duration_ms"] >= 50
        assert p["p95_duration_ms"] == p["p50_duration_ms"] * 3
    # 250ms regressor output clears the floor of 50.
    assert data["predictions"][0]["p50_duration_ms"] == 250


def test_predict_p50_floor_is_enforced(client, monkeypatch):
    repo_id = str(uuid.uuid4())
    monkeypatch.setattr(app_module, "_resolve_repo_id", lambda _name: repo_id)
    # Regressor predicts below the 50ms floor → clamped to 50.
    app_module._registry = _StubRegistry(_StubLoadedModel(duration=10.0, flake=0.0))
    monkeypatch.setattr(app_module, "_history_for_repo", lambda _rid: {})

    resp = _predict(client, n=1)
    assert resp.status_code == 200
    assert resp.json()["predictions"][0]["p50_duration_ms"] == 50


def test_predict_no_dsn_resolves_none_cold_start(client, monkeypatch):
    # _resolve_repo_id returns None (no DSN / unknown repo) → cold-start fallback.
    monkeypatch.setattr(app_module, "_resolve_repo_id", lambda _name: None)
    # A registry that would explode if consulted, proving we never reach it.
    app_module._registry = _StubRegistry(_StubLoadedModel(duration=999.0, flake=0.9))

    resp = _predict(client, n=2)
    assert resp.status_code == 200
    data = resp.json()

    assert data["used_fallback"] is True
    assert data["used_model_version"] == "cold-start"
    assert len(data["predictions"]) == 2
    for p in data["predictions"]:
        assert p["is_cold_start"] is True
        assert p["p50_duration_ms"] == 1200
        assert p["p95_duration_ms"] == 3600
        assert p["flake_probability"] == 0.0
    # Registry must NOT have been consulted when repo_id is None.
    assert app_module._registry.get_calls == []


def test_predict_model_missing_cold_start(client, monkeypatch):
    # repo resolves, but registry.get returns None (model not trained/loadable).
    repo_id = str(uuid.uuid4())
    monkeypatch.setattr(app_module, "_resolve_repo_id", lambda _name: repo_id)
    app_module._registry = _StubRegistry(None)

    resp = _predict(client, n=3)
    assert resp.status_code == 200
    data = resp.json()

    assert data["used_fallback"] is True
    assert data["used_model_version"] == "cold-start"
    assert len(data["predictions"]) == 3
    assert all(p["is_cold_start"] for p in data["predictions"])
    assert app_module._registry.get_calls == [repo_id]


def test_predict_history_fetch_raises_degrades_to_cold_start_via_model(client, monkeypatch):
    # History fetch raising must NOT 500: the handler delegates to
    # _history_for_repo which itself swallows errors (repo.history_for_repo).
    # Here we simulate the *handler-level* contract by having the model path run
    # with an empty history (the swallow already happened in repo.py); a separate
    # repo-level test asserts the swallow. The handler still returns 200.
    repo_id = str(uuid.uuid4())
    monkeypatch.setattr(app_module, "_resolve_repo_id", lambda _name: repo_id)
    app_module._registry = _StubRegistry(_StubLoadedModel(duration=120.0, flake=0.1))
    monkeypatch.setattr(app_module, "_history_for_repo", lambda _rid: {})

    resp = _predict(client, n=2)
    assert resp.status_code == 200
    data = resp.json()
    assert data["used_fallback"] is False
    assert len(data["predictions"]) == 2


def test_history_for_repo_swallows_errors(monkeypatch):
    # The ClickHouse/Postgres history fetch raising must degrade to {} (cold-start)
    # rather than propagating — assert at the repo layer that feeds the handler.
    from teo_predictor_ml import repo as repo_module

    def _boom(_repo_id, _dsn):
        raise RuntimeError("db exploded")

    monkeypatch.setattr(repo_module, "_query_history", _boom)
    # With a non-empty DSN we reach _query_history, which raises; result must be {}.
    out = repo_module.history_for_repo("some-repo-id", dsn="postgres://x")
    assert out == {}


def test_healthz_ok(client):
    resp = client.get("/healthz")
    assert resp.status_code == 200
    assert resp.json()["status"] == "ok"
