"""Postgres repo resolution + Postgres per-test history.

These are the data-access helpers the /v1/predict handler uses to actually
exercise the trained-model path. Every DB call is wrapped so that a failure
degrades to cold-start (return None / {}) rather than a 500 — the Run Manager
must always get a usable response, and the Go Fallback treats a down ML service
as a heuristic fallback anyway.

History source: Postgres ``teo.test_executions`` (joined shards -> runs for
repo_id, tests for path/name), the SAME source the always-on Go heuristic uses
(internal/predictor/predictor.go loadStats). This is deliberate: per-test
outcome history in TEO lives in Postgres, NOT ClickHouse. The ClickHouse
``teo.test_runs`` table referenced by an earlier draft of this file does not
exist / is never written by any code in the repo (the result pipeline only
writes span_events + failure_clusters), so sourcing history from there yielded a
permanently-empty map and the trained model only ever emitted its bias term.
Keying by ``path::name`` matches both the Go heuristic and features.from_history.

Connections are intentionally per-call + short-timeout to keep the dependency
surface small; repo_id resolution is memoized in a small TTL cache because the
same repo is hammered across a run.
"""

from __future__ import annotations

import logging
import os
import threading
import time
from dataclasses import dataclass

log = logging.getLogger(__name__)


@dataclass
class TestHistory:
    """Rolling 30-day stats for one test, keyed by ``path::name``."""

    p50_ms: float
    p95_ms: float
    std_ms: float
    fail_rate: float
    attempt_count: int


# ---- Postgres repo resolution ------------------------------------------------

_repo_cache: dict[str, tuple[str | None, float]] = {}
_repo_cache_lock = threading.RLock()
_REPO_TTL = float(os.environ.get("TEO_REPO_CACHE_TTL_SECONDS", "300"))


def resolve_repo_id(full_name: str, *, dsn: str | None = None) -> str | None:
    """Resolve a repo full_name to its UUID via Postgres.

    Returns None when no DSN is configured, the repo is unknown, or the query
    fails — all of which the caller treats as cold-start. Memoized per-repo with
    a bounded TTL.
    """
    if dsn is None:
        dsn = os.environ.get("TEO_POSTGRES_DSN", "")
    if not dsn:
        return None

    now = time.time()
    with _repo_cache_lock:
        cached = _repo_cache.get(full_name)
        if cached is not None and now - cached[1] < _REPO_TTL:
            return cached[0]

    repo_id = _query_repo_id(full_name, dsn)
    with _repo_cache_lock:
        _repo_cache[full_name] = (repo_id, now)
    return repo_id


def _query_repo_id(full_name: str, dsn: str) -> str | None:
    try:
        import psycopg

        with psycopg.connect(dsn, connect_timeout=2) as conn:
            with conn.cursor() as cur:
                cur.execute(
                    "SELECT id FROM teo.repos WHERE full_name = %s AND vcs = 'github'",
                    (full_name,),
                )
                row = cur.fetchone()
                if row is None:
                    return None
                return str(row[0])
    except Exception as e:  # noqa: BLE001 - degrade to cold-start on any failure
        log.warning("repo resolve failed for %s: %s", full_name, e)
        return None


# ---- Postgres per-repo test history -----------------------------------------


def history_for_repo(repo_id: str, *, dsn: str | None = None) -> dict[str, TestHistory]:
    """Return ``{path::name: TestHistory}`` for a repo over the last 30 days.

    Sourced from Postgres ``teo.test_executions`` (TEO_POSTGRES_DSN), matching the
    always-on Go heuristic. On any failure (no DSN, connection error, query error)
    returns ``{}`` so the handler degrades to per-test cold-start without a 500.
    """
    if dsn is None:
        dsn = os.environ.get("TEO_POSTGRES_DSN", "")
    if not dsn:
        return {}
    try:
        return _query_history(repo_id, dsn)
    except Exception as e:  # noqa: BLE001 - degrade to cold-start on any failure
        log.warning("history fetch failed for repo %s: %s", repo_id, e)
        return {}


# Mirrors internal/predictor/predictor.go loadStats(): 30-day window over
# teo.test_executions, p50/p95 via percentile_disc, fail_rate over
# failed/errored/timed_out, keyed by (path, name).
_HISTORY_SQL = """
    WITH recent AS (
        SELECT te.test_id, te.duration_ms, te.outcome
        FROM teo.test_executions te
        JOIN teo.shards s ON s.id = te.shard_id
        JOIN teo.runs r ON r.id = s.run_id
        WHERE r.repo_id = %(repo_id)s
          AND te.started_at > now() - INTERVAL '30 days'
    )
    SELECT t.path,
           t.name,
           percentile_disc(0.5)  WITHIN GROUP (ORDER BY r.duration_ms) AS p50,
           percentile_disc(0.95) WITHIN GROUP (ORDER BY r.duration_ms) AS p95,
           coalesce(stddev_pop(r.duration_ms), 0)                      AS std,
           sum(CASE WHEN r.outcome IN ('failed','errored','timed_out') THEN 1 ELSE 0 END)::float
             / GREATEST(count(r.duration_ms), 1)                       AS fail_rate,
           count(r.duration_ms)                                        AS attempts
    FROM teo.tests t
    LEFT JOIN recent r ON r.test_id = t.id
    WHERE t.repo_id = %(repo_id)s AND t.status != 'deleted'
    GROUP BY t.path, t.name
"""


def _query_history(repo_id: str, dsn: str) -> dict[str, TestHistory]:
    import psycopg

    out: dict[str, TestHistory] = {}
    with psycopg.connect(dsn, connect_timeout=2) as conn:
        with conn.cursor() as cur:
            cur.execute(_HISTORY_SQL, {"repo_id": repo_id})
            for test_path, test_name, p50, p95, std, fail_rate, attempts in cur.fetchall():
                key = f"{test_path}::{test_name}"
                out[key] = TestHistory(
                    p50_ms=float(p50 or 0.0),
                    p95_ms=float(p95 or 0.0),
                    std_ms=float(std or 0.0),
                    fail_rate=float(fail_rate or 0.0),
                    attempt_count=int(attempts or 0),
                )
    return out
