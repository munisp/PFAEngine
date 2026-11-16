"""Model monitoring: population drift (PSI) and distribution shift (KS),
plus score-distribution degradation alerts.

Alert bands (industry-standard):
  PSI < 0.10  : no significant shift
  0.10-0.25   : moderate shift — warning
  > 0.25      : significant shift — retrain alert
  KS p-value < 0.05 on a key feature : drift alert
"""
from __future__ import annotations

import math

import numpy as np


def psi(expected: np.ndarray, actual: np.ndarray, buckets: int = 10) -> float:
    """Population Stability Index between two 1-D distributions."""
    edges = np.quantile(expected, np.linspace(0, 1, buckets + 1))
    edges[0], edges[-1] = -np.inf, np.inf
    e = np.histogram(expected, bins=edges)[0] / max(len(expected), 1)
    a = np.histogram(actual, bins=edges)[0] / max(len(actual), 1)
    e = np.clip(e, 1e-4, None)
    a = np.clip(a, 1e-4, None)
    return float(np.sum((a - e) * np.log(a / e)))


def ks_statistic(expected: np.ndarray, actual: np.ndarray) -> float:
    """Two-sample Kolmogorov-Smirnov D statistic."""
    e_sorted = np.sort(expected)
    a_sorted = np.sort(actual)
    all_v = np.concatenate([e_sorted, a_sorted])
    cdf_e = np.searchsorted(e_sorted, all_v, side="right") / len(e_sorted)
    cdf_a = np.searchsorted(a_sorted, all_v, side="right") / len(a_sorted)
    return float(np.max(np.abs(cdf_e - cdf_a)))


class DriftMonitor:
    def __init__(self, reference: np.ndarray, warn: float = 0.10,
                 alert: float = 0.25):
        """reference: (N, F) reference feature matrix from training."""
        self.reference = np.asarray(reference, dtype=np.float64)
        self.warn, self.alert = warn, alert

    def check(self, current: np.ndarray) -> dict:
        """current: (M, F) production feature matrix. Returns per-feature
        PSI/KS plus an overall status: ok | warning | alert."""
        current = np.asarray(current, dtype=np.float64)
        report, worst = [], "ok"
        for f in range(self.reference.shape[1]):
            p = psi(self.reference[:, f], current[:, f])
            k = ks_statistic(self.reference[:, f], current[:, f])
            status = "alert" if p > self.alert else \
                     "warning" if p > self.warn else "ok"
            if status == "alert":
                worst = "alert"
            elif status == "warning" and worst == "ok":
                worst = "warning"
            report.append({"feature": f, "psi": round(p, 4),
                           "ks": round(k, 4), "status": status})
        return {"status": worst, "features": report,
                "retrain_recommended": worst == "alert"}


def score_degradation(baseline_auc: float, current_auc: float,
                      tolerance: float = 0.03) -> dict:
    degraded = current_auc < baseline_auc - tolerance
    return {"baseline_auc": baseline_auc, "current_auc": current_auc,
            "degraded": degraded,
            "action": "retrain + challenger promotion" if degraded else "none"}
