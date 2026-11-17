"""Discovery shim for `python3 -m unittest discover analytics`.

unittest's default discovery pattern is `test*.py`; the actual CDC suite
lives in cdc_test.py (named per project convention, like
lakehouse_test.py). This shim re-exports it so standard discovery picks
it up.
"""
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from cdc_test import *  # noqa: F401,F403
