"""Discovery shim for `python3 -m unittest discover analytics`.

unittest's default discovery pattern is `test*.py`; the actual suite lives
in lakehouse_test.py (named per project convention). This shim re-exports
it so standard discovery picks it up.
"""
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from lakehouse_test import *  # noqa: F401,F403
