"""Feature-extraction tests: from_history / _features_for + empty()."""

from __future__ import annotations

import datetime as dt

from teo_predictor_ml.app import _features_for
from teo_predictor_ml.features import COLUMN_NAMES, empty, from_history
from teo_predictor_ml.models import TestEntry
from teo_predictor_ml.repo import TestHistory


def _entry(path="tests/a.py", name="test_a") -> TestEntry:
    return TestEntry(path=path, name=name)


def test_from_history_present_test_uses_history():
    t = _entry()
    hist = {
        "tests/a.py::test_a": TestHistory(
            p50_ms=420.0,
            p95_ms=900.0,
            std_ms=55.0,
            fail_rate=0.125,
            attempt_count=8,
        )
    }
    row = from_history(t, hist, now=dt.datetime(2026, 6, 9, 14, 30, tzinfo=dt.timezone.utc))

    assert row.p50_history_ms == 420.0
    assert row.p95_history_ms == 900.0
    assert row.std_history_ms == 55.0
    assert row.fail_rate == 0.125
    assert row.attempt_count == 8
    # 2026-06-09 is a Tuesday → weekday() == 1; hour 14.
    assert row.hour_of_day == 14
    assert row.day_of_week == 1


def test_from_history_absent_test_is_zero_row():
    t = _entry(path="tests/missing.py", name="test_missing")
    hist = {
        "tests/a.py::test_a": TestHistory(
            p50_ms=420.0, p95_ms=900.0, std_ms=55.0, fail_rate=0.1, attempt_count=8
        )
    }
    row = from_history(t, hist, now=dt.datetime(2026, 6, 9, 9, 0, tzinfo=dt.timezone.utc))

    assert row.p50_history_ms == 0
    assert row.p95_history_ms == 0
    assert row.std_history_ms == 0
    assert row.fail_rate == 0
    assert row.attempt_count == 0
    # time-of-day still filled even for an absent test.
    assert row.hour_of_day == 9


def test_features_for_present_matches_history():
    t = _entry()
    hist = {
        "tests/a.py::test_a": TestHistory(
            p50_ms=333.0, p95_ms=777.0, std_ms=20.0, fail_rate=0.05, attempt_count=5
        )
    }
    row = _features_for(t, hist)
    assert row.p50_history_ms == 333.0
    assert row.p95_history_ms == 777.0
    assert row.fail_rate == 0.05


def test_features_for_absent_is_empty_zeros():
    t = _entry(path="tests/x.py", name="test_x")
    row = _features_for(t, {})  # empty history map
    assert row.p50_history_ms == 0
    assert row.p95_history_ms == 0
    assert row.fail_rate == 0
    assert row.attempt_count == 0


def test_features_for_none_history_is_empty_zeros():
    # _features_for must route a None history through the zero path, not crash.
    t = _entry(path="tests/x.py", name="test_x")
    row = _features_for(t, None)
    assert row.p50_history_ms == 0
    assert row.attempt_count == 0


def test_as_array_length_matches_column_names():
    arr = empty().as_array()
    assert len(arr) == len(COLUMN_NAMES)


def test_as_array_length_for_populated_row():
    t = _entry()
    hist = {
        "tests/a.py::test_a": TestHistory(
            p50_ms=1.0, p95_ms=2.0, std_ms=3.0, fail_rate=0.4, attempt_count=9
        )
    }
    arr = from_history(t, hist).as_array()
    assert len(arr) == len(COLUMN_NAMES)
