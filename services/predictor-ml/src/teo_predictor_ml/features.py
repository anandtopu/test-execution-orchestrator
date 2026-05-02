"""Feature extraction. Per ADR-0019:

  - history: last-30-day p50, p95, std-dev, fail rate, attempt count
  - path: file changed in this commit (binary), files in same dir changed,
          change frequency in last 30 days
  - worker: instance type, container image hash
  - time-of-day, day-of-week
"""

from __future__ import annotations

from dataclasses import dataclass

import numpy as np


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
