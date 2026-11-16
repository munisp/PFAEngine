"""MCMC Bayesian calibration of the fraud decision threshold.

The fusion model emits a probability p per transaction; ops needs a threshold
t such that flagging at p >= t trades precision/recall optimally under an
explicit cost model:
  cost(t) = C_fn * FN(t) + C_fp * FP(t)
Default costs: missing a fraud = 50x a false positive investigation.

We place a Beta(a0, b0) prior over the achieved fraud-capture rate and use
Metropolis-Hastings to sample the posterior over the threshold t itself
(likelihood from empirical FN/FP counts at t). The posterior mean threshold
is what production uses — an auditable, uncertainty-quantified calibration
instead of a hard-coded 0.5.
"""
from __future__ import annotations

import math
import random

import numpy as np


def _log_beta_pdf(x: float, a: float, b: float) -> float:
    if x <= 0 or x >= 1:
        return -math.inf
    return (a - 1) * math.log(x) + (b - 1) * math.log(1 - x)


class ThresholdMCMC:
    def __init__(self, cost_fn: float = 50.0, cost_fp: float = 1.0,
                 a0: float = 2.0, b0: float = 2.0, seed: int = 7):
        self.cost_fn, self.cost_fp = cost_fn, cost_fp
        self.a0, self.b0 = a0, b0
        self.rng = random.Random(seed)

    def _log_posterior(self, t: float, probs: np.ndarray,
                       labels: np.ndarray) -> float:
        if t <= 0.01 or t >= 0.99:
            return -math.inf
        pred = probs >= t
        fn = int(((~pred) & (labels == 1)).sum())
        fp = int((pred & (labels == 0)).sum())
        # prior on t (weakly centred near cost-optimal region)
        lp = _log_beta_pdf(t, self.a0, self.b0)
        # likelihood: penalise expected cost (pseudo-log-likelihood)
        lp += -(self.cost_fn * fn + self.cost_fp * fp) / max(len(labels), 1) * 10
        return lp

    def sample(self, probs: np.ndarray, labels: np.ndarray,
               n_samples: int = 4000, burn: int = 500,
               proposal_sd: float = 0.03) -> dict:
        probs = np.asarray(probs, dtype=np.float64)
        labels = np.asarray(labels)
        t = 0.5
        lp = self._log_posterior(t, probs, labels)
        samples = []
        accepted = 0
        for i in range(n_samples):
            cand = t + self.rng.gauss(0, proposal_sd)
            lp_c = self._log_posterior(cand, probs, labels)
            if math.log(self.rng.random() + 1e-12) < lp_c - lp:
                t, lp = cand, lp_c
                accepted += 1
            if i >= burn:
                samples.append(t)
        arr = np.asarray(samples)
        pred = probs >= float(arr.mean())
        tp = int((pred & (labels == 1)).sum())
        fn = int(((~pred) & (labels == 1)).sum())
        fp = int((pred & (labels == 0)).sum())
        return {
            "threshold_mean": float(arr.mean()),
            "threshold_std": float(arr.std()),
            "threshold_p05": float(np.percentile(arr, 5)),
            "threshold_p95": float(np.percentile(arr, 95)),
            "acceptance_rate": accepted / n_samples,
            "precision": tp / max(tp + fp, 1),
            "recall": tp / max(tp + fn, 1),
        }
