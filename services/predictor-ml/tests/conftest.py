"""Pytest bootstrap: make the src/ layout importable without an editable install.

These unit tests run in CI and locally without `pip install -e .[dev]`, so we
prepend the package's src/ directory to sys.path. (When the package IS installed,
the path entry is harmless.)
"""

from __future__ import annotations

import sys
from pathlib import Path

SRC = Path(__file__).resolve().parents[1] / "src"
if str(SRC) not in sys.path:
    sys.path.insert(0, str(SRC))
