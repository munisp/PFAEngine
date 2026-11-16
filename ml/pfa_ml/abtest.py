"""A/B testing infrastructure for model comparison in production.

Deterministic assignment: member_id hash -> bucket 0..99; challenger gets
`challenger_pct` percent of traffic. Deterministic = the same member always
sees the same model (stable experience, no per-request flicker), and the
split is reproducible offline for analysis.

significance() runs a two-proportion z-test on fraud-capture outcomes.
"""
from __future__ import annotations

import hashlib
import math


def bucket(member_id: str) -> int:
    h = hashlib.sha256(member_id.encode()).hexdigest()
    return int(h[:8], 16) % 100


def assign(member_id: str, challenger_pct: int = 10) -> str:
    return "challenger" if bucket(member_id) < challenger_pct else "champion"


def z_test_two_proportion(success_a: int, n_a: int,
                          success_b: int, n_b: int) -> dict:
    """H0: p_a == p_b. Returns z, p-value (two-sided), and verdict."""
    if n_a == 0 or n_b == 0:
        return {"z": 0.0, "p_value": 1.0, "significant": False}
    p1, p2 = success_a / n_a, success_b / n_b
    p_pool = (success_a + success_b) / (n_a + n_b)
    se = math.sqrt(p_pool * (1 - p_pool) * (1 / n_a + 1 / n_b))
    if se == 0:
        return {"z": 0.0, "p_value": 1.0, "significant": False}
    z = (p1 - p2) / se
    # normal CDF via erf
    p = math.erfc(abs(z) / math.sqrt(2))
    return {"z": round(z, 4), "p_value": round(p, 6),
            "significant": p < 0.05,
            "champion_rate": round(p1, 4), "challenger_rate": round(p2, 4)}


class ABExperiment:
    """Tracks outcomes for champion vs challenger fraud-capture."""

    def __init__(self, name: str, challenger_pct: int = 10):
        self.name = name
        self.challenger_pct = challenger_pct
        self.outcomes = {"champion": [0, 0], "challenger": [0, 0]}  # [ok, n]

    def arm(self, member_id: str) -> str:
        return assign(member_id, self.challenger_pct)

    def record(self, member_id: str, captured_fraud: bool) -> str:
        arm = self.arm(member_id)
        self.outcomes[arm][1] += 1
        if captured_fraud:
            self.outcomes[arm][0] += 1
        return arm

    def report(self) -> dict:
        (ca, na), (cb, nb) = self.outcomes["champion"], self.outcomes["challenger"]
        res = z_test_two_proportion(ca, na, cb, nb)
        res["experiment"] = self.name
        res["promote_challenger"] = bool(
            res["significant"] and nb >= 200 and cb / max(nb, 1) > ca / max(na, 1))
        return res
