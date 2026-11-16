"""Deterministic feature encoding shared by training and serving.

IMPORTANT: encode_tabular must be pure and versioned (FEATURE_VERSION) —
training and inference MUST produce identical vectors for identical input.
"""
from __future__ import annotations

import hashlib
import json
import math
from typing import Iterable

import numpy as np

FEATURE_VERSION = 1
CHANNELS = ["ussd", "mobile", "web", "branch", "agent"]
KINDS = ["contribution", "withdrawal", "transfer", "inquiry", "beneficiary_change"]
TABULAR_DIM = 20
SEQ_LEN = 16
SEQ_DIM = 8


def _one_hot(value: str, vocab: list[str]) -> list[float]:
    out = [0.0] * len(vocab)
    if value in vocab:
        out[vocab.index(value)] = 1.0
    return out


def encode_tabular(txn: dict, history: list[dict]) -> np.ndarray:
    """20-dim tabular vector for one transaction given the member's history.

    history: prior transactions (same member), oldest first.
    """
    amount = txn["amount_kobo"] / 100.0  # naira
    hist_amts = [h["amount_kobo"] / 100.0 for h in history if h["amount_kobo"] > 0]
    avg = float(np.mean(hist_amts)) if hist_amts else 0.0
    std = float(np.std(hist_amts)) if len(hist_amts) > 1 else 1.0
    ratio = amount / avg if avg > 0 else (10.0 if amount > 0 else 0.0)
    days_since = txn["day"] - history[-1]["day"] if history else 999
    dormancy = min(days_since, 365) / 365.0
    # velocity: txns in last 60 "minutes" of the same day
    vel = sum(1 for h in history
              if h["day"] == txn["day"]
              and 0 <= txn["minute_of_day"] - h["minute_of_day"] <= 60)
    # device novelty
    seen_devices = {h["device_id"] for h in history}
    novel_dev = 1.0 if txn["device_id"] not in seen_devices else 0.0
    # state novelty
    seen_states = {h["state"] for h in history}
    novel_state = 1.0 if txn["state"] not in seen_states and history else 0.0

    vec = (
        [math.log1p(amount) / 12.0, ratio / 10.0, dormancy, vel / 8.0,
         novel_dev, novel_state, txn["minute_of_day"] / 1440.0,
         txn["day"] % 30 / 30.0, len(history) / 100.0, math.log1p(avg) / 12.0]
        + _one_hot(txn["channel"], CHANNELS)
        + _one_hot(txn["kind"], KINDS)
    )
    assert len(vec) == TABULAR_DIM, len(vec)
    return np.asarray(vec, dtype=np.float32)


def encode_sequence(history: list[dict]) -> np.ndarray:
    """(SEQ_LEN, SEQ_DIM) sequence of the last SEQ_LEN prior transactions.

    NOTE: no label-derived features here — is_fraud is ground truth and is
    never available at inference time; including it would leak labels.
    """
    seq = np.zeros((SEQ_LEN, SEQ_DIM), dtype=np.float32)
    tail = history[-SEQ_LEN:]
    amts = [h["amount_kobo"] / 100.0 for h in history if h["amount_kobo"] > 0]
    running_mean = 0.0
    for i, h in enumerate(tail):
        gap = h["day"] - tail[i - 1]["day"] if i > 0 else 0
        amt = h["amount_kobo"] / 100.0
        ratio = min(amt / running_mean, 10.0) / 10.0 if running_mean > 0 else 0.0
        if amt > 0:
            running_mean = (running_mean * i + amt) / (i + 1) if i > 0 else amt
        seq[i] = [
            math.log1p(amt) / 12.0,
            1.0 if h["kind"] == "withdrawal" else 0.0,
            1.0 if h["kind"] == "contribution" else 0.0,
            1.0 if h["kind"] == "transfer" else 0.0,
            min(gap, 60) / 60.0,
            h["minute_of_day"] / 1440.0,
            CHANNELS.index(h["channel"]) / (len(CHANNELS) - 1)
            if h["channel"] in CHANNELS else 0.0,
            ratio,
        ]
    return seq


def feature_version_hash() -> str:
    src = json.dumps({"v": FEATURE_VERSION, "tab": TABULAR_DIM,
                      "seq": [SEQ_LEN, SEQ_DIM]}, sort_keys=True)
    return hashlib.sha256(src.encode()).hexdigest()[:12]


def batch_encode(samples: Iterable[tuple[dict, list[dict]]]):
    tabs, seqs, labels = [], [], []
    for txn, hist in samples:
        tabs.append(encode_tabular(txn, hist))
        seqs.append(encode_sequence(hist))
        labels.append(int(txn.get("is_fraud", 0)))
    return (np.stack(tabs), np.stack(seqs),
            np.asarray(labels, dtype=np.int64))
