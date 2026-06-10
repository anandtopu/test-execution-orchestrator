"""Feature extraction. Per ADR-0019:

  - history: last-30-day p50, p95, std-dev, fail rate, attempt count
  - path: file changed in this commit (binary), files in same dir changed,
          change frequency in last 30 days
  - worker: instance type, container image hash
  - time-of-day, day-of-week
"""

from __future__ import annotations

import datetime as _dt
from dataclasses import dataclass
from typing import TYPE_CHECKING

import numpy as np

if TYPE_CHECKING:  # pragma: no cover - typing only
    from .models import TestEntry
    from .repo import TestHistory


@dataclass
class FeatureRow:
    """One feature vector for a single (test, run) prediction."""

    p50_history_ms: float
    p95_history_ms: float
    std_history_ms: float
    fail_rate: float
    attempt_count: int
    file_changed_now: int
    same_dir_changes_7d: int
    test_change_freq_30d: int
    hour_of_day: int
    day_of_week: int

    def as_array(self) -> np.ndarray:
        return np.array(
            [
                self.p50_history_ms,
                self.p95_history_ms,
                self.std_history_ms,
                self.fail_rate,
                float(self.attempt_count),
                float(self.file_changed_now),
                float(self.same_dir_changes_7d),
                float(self.test_change_freq_30d),
                float(self.hour_of_day),
                float(self.day_of_week),
            ],
            dtype=np.float32,
        )


COLUMN_NAMES = [
    "p50_history_ms",
    "p95_history_ms",
    "std_history_ms",
    "fail_rate",
    "attempt_count",
    "file_changed_now",
    "same_dir_changes_7d",
    "test_change_freq_30d",
    "hour_of_day",
    "day_of_week",
]


def empty() -> FeatureRow:
    """Return a zero-feature row for cold-start tests."""
    return FeatureRow(
        p50_history_ms=0,
        p95_history_ms=0,
        std_history_ms=0,
        fail_rate=0,
        attempt_count=0,
        file_changed_now=0,
        same_dir_changes_7d=0,
        test_change_freq_30d=0,
        hour_of_day=0,
        day_of_week=0,
    )


def from_history(
    test: "TestEntry",
    history: "dict[str, TestHistory]",
    *,
    now: _dt.datetime | None = None,
) -> FeatureRow:
    """Build a FeatureRow for ``test`` from the per-repo history map.

    A test present in ``history`` yields its rolling p50/p95/std/fail-rate +
    attempt count; a test absent from the map yields :func:`empty` (zeros) so the
    model treats it as cold-start. Time-of-day / day-of-week are filled from
    ``now`` (defaults to UTC now). Commit-diff features (file_changed_now etc.)
    are not available on the prediction request payload and stay zero until the
    Run Manager threads changed-file context through; this is forward-compatible
    with the ADR-0019 feature list.
    """
    when = now or _dt.datetime.now(_dt.timezone.utc)
    key = f"{test.path}::{test.name}"
    h = history.get(key)
    if h is None:
        row = empty()
        row.hour_of_day = when.hour
        row.day_of_week = when.weekday()
        return row
    return FeatureRow(
        p50_history_ms=h.p50_ms,
        p95_history_ms=h.p95_ms,
        std_history_ms=h.std_ms,
        fail_rate=h.fail_rate,
        attempt_count=h.attempt_count,
        file_changed_now=0,
        same_dir_changes_7d=0,
        test_change_freq_30d=0,
        hour_of_day=when.hour,
        day_of_week=when.weekday(),
    )
