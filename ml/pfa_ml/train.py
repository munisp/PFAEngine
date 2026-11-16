"""Training pipeline: GNN -> member embeddings -> FraudFusion -> MCMC
threshold calibration -> versioned artifact + registry entry.

Real training loops (Adam, BCE, early stopping on validation AUC).
Chronological split (last 20% of days = validation) to mimic production
forward deployment — no random leakage.

Artifacts per run (registry-managed):
  fraudfusion.pt   — FraudFusion weights (torch.save state_dict)
  gnn.pt           — GraphSAGEFraud weights
  meta.json        — feature version, dataset hash, metrics, threshold
"""
from __future__ import annotations

import argparse
import json
import tempfile
import time
from collections import defaultdict
from pathlib import Path

import numpy as np
import torch
from torch import nn

from pfa_ml.features import batch_encode, feature_version_hash
from pfa_ml.graph import FraudGraph
from pfa_ml.models.fraud_fusion import FraudFusion
from pfa_ml.models.gnn import GraphSAGEFraud
from pfa_ml.models.mcmc import ThresholdMCMC
from pfa_ml.registry import Registry


def load_samples(data_dir: Path):
    members = {json.loads(l)["member_id"]: json.loads(l)
               for l in open(data_dir / "members.jsonl")}
    txns = [json.loads(l) for l in open(data_dir / "transactions.jsonl")]
    by_member: dict[str, list[dict]] = defaultdict(list)
    samples = []
    for t in txns:
        hist = by_member[t["member_id"]]
        samples.append((t, list(hist)))
        hist.append(t)
    return members, txns, samples


def auc(scores: np.ndarray, labels: np.ndarray) -> float:
    order = np.argsort(scores)
    ranks = np.empty_like(order, dtype=np.float64)
    ranks[order] = np.arange(1, len(scores) + 1)
    pos = labels == 1
    n_pos, n_neg = pos.sum(), (~pos).sum()
    if n_pos == 0 or n_neg == 0:
        return 0.5
    return float((ranks[pos].sum() - n_pos * (n_pos + 1) / 2) / (n_pos * n_neg))


def train_gnn(g: FraudGraph, epochs: int, lr: float = 5e-3,
              exclude: torch.Tensor | None = None,
              verbose: bool = False) -> tuple[GraphSAGEFraud, float]:
    """Train GraphSAGE. `exclude` (bool mask over member nodes) removes
    members from BOTH training and its own validation — used for k-fold
    out-of-fold embedding generation (prevents transductive label leak
    into the fusion tower)."""
    x = torch.as_tensor(g.node_feat)
    ei = torch.as_tensor(g.edge_index())
    mi = torch.arange(len(g.member_idx))
    y = torch.as_tensor(g.member_labels, dtype=torch.float32)
    if exclude is not None:
        keep = ~exclude
        pool = mi[keep]
    else:
        pool = mi
    n_val = max(1, len(pool) // 5)
    val_idx, tr_idx = pool[:n_val], pool[n_val:]
    model = GraphSAGEFraud()
    opt = torch.optim.Adam(model.parameters(), lr=lr)
    lossf = nn.BCELoss(reduction="none")
    best_auc, best_state = 0.0, None
    for ep in range(epochs):
        model.train()
        opt.zero_grad()
        p = model(x, ei, tr_idx)
        pos_w = torch.where(y[tr_idx] == 1, 20.0, 1.0)
        loss = (lossf(p, y[tr_idx]) * pos_w).sum() / pos_w.sum()
        loss.backward()
        opt.step()
        model.eval()
        with torch.no_grad():
            pv = model(x, ei, val_idx).numpy()
        a = auc(pv, y[val_idx].numpy())
        if a >= best_auc:
            best_auc, best_state = a, {k: v.clone() for k, v in
                                       model.state_dict().items()}
        if verbose and ep % 5 == 0:
            print(f"  gnn epoch {ep}: loss={loss.item():.4f} val_auc={a:.4f}")
    if best_state:
        model.load_state_dict(best_state)
    return model, best_auc


def train_fusion(samples, member_emb: dict[str, np.ndarray],
                 epochs: int, lr: float = 5e-3, verbose: bool = False):
    max_day = max(t["day"] for t, _ in samples)
    cut = int(max_day * 0.8)
    train = [(t, h) for t, h in samples if t["day"] < cut]
    val = [(t, h) for t, h in samples if t["day"] >= cut]
    Xtab, Xseq, y = batch_encode(train)
    Vtab, Vseq, vy = batch_encode(val)

    def emb_of(sampleset):
        return np.stack([member_emb.get(t["member_id"],
                                        np.zeros(32, np.float32))
                         for t, _ in sampleset]).astype(np.float32)

    Xemb, Vemb = emb_of(train), emb_of(val)
    model = FraudFusion()
    opt = torch.optim.Adam(model.parameters(), lr=lr)
    lossf = nn.BCELoss(reduction="none")
    yt = torch.as_tensor(y, dtype=torch.float32)
    pos_w = torch.where(yt == 1, 20.0, 1.0)
    Xt = torch.as_tensor(Xtab); Xs = torch.as_tensor(Xseq)
    Xe = torch.as_tensor(Xemb)
    best_auc, best_state = 0.0, None
    bs = 512
    for ep in range(epochs):
        model.train()
        perm = torch.randperm(len(Xt))
        tot = 0.0
        for i in range(0, len(Xt), bs):
            idx = perm[i:i + bs]
            opt.zero_grad()
            p = model(Xt[idx], Xs[idx], Xe[idx])
            loss = (lossf(p, yt[idx]) * pos_w[idx]).sum() / pos_w[idx].sum()
            loss.backward()
            opt.step()
            tot += loss.item()
        model.eval()
        with torch.no_grad():
            pv = model(torch.as_tensor(Vtab), torch.as_tensor(Vseq),
                       torch.as_tensor(Vemb)).numpy()
        a = auc(pv, vy)
        if a >= best_auc:
            best_auc, best_state = a, {k: v.clone() for k, v in
                                       model.state_dict().items()}
        if verbose and ep % 2 == 0:
            print(f"  fusion epoch {ep}: loss={tot:.3f} val_auc={a:.4f}")
    if best_state:
        model.load_state_dict(best_state)
    return model, best_auc, pv, vy


def out_of_fold_embeddings(g: FraudGraph, epochs: int, folds: int = 5,
                           verbose: bool = False) -> dict[str, np.ndarray]:
    """K-fold transductive-leakage control: for each fold, train a GNN with
    that fold's members EXCLUDED, then embed them. Fusion therefore trains
    on embeddings produced by models that never saw those members' labels —
    matching production, where embeddings are computed nightly and never
    encode the scored member's own future label."""
    n = len(g.member_idx)
    emb = np.zeros((n, 32), dtype=np.float32)
    fold_id = np.arange(n) % folds
    x = torch.as_tensor(g.node_feat)
    ei = torch.as_tensor(g.edge_index())
    for f in range(folds):
        excl = torch.zeros(n, dtype=torch.bool)
        excl[fold_id == f] = True
        model, _ = train_gnn(g, epochs=epochs, exclude=excl)
        model.eval()
        with torch.no_grad():
            e = model.embed(x, ei).numpy()
        emb[fold_id == f] = e[:n][fold_id == f]  # members occupy nodes 0..n-1
        if verbose:
            print(f"  fold {f}: embedded {(fold_id == f).sum()} members")
    return {mid: emb[i] for mid, i in g.member_idx.items()}


def run_training(data_dir: str | Path, epochs: int = 8, verbose: bool = True,
                 registry: Registry | None = None) -> dict:
    data_dir = Path(data_dir)
    reg = registry or Registry()
    t0 = time.time()
    manifest = json.loads((data_dir / "manifest.json").read_text())
    if verbose:
        print(f"[train] dataset {manifest['dataset_sha256'][:12]} "
              f"txns={manifest['transactions']} fraud={manifest['fraud_txns']}")

    g = FraudGraph.build(data_dir)
    gnn, gnn_auc = train_gnn(g, epochs=epochs, verbose=verbose)
    member_emb = out_of_fold_embeddings(g, epochs=max(3, epochs // 2),
                                        verbose=verbose)

    _, _, samples = load_samples(data_dir)
    fusion, fusion_auc, val_probs, val_labels = train_fusion(
        samples, member_emb, epochs=epochs, verbose=verbose)

    mcmc = ThresholdMCMC()
    cal = mcmc.sample(val_probs, val_labels)

    metrics = {"gnn_val_auc": round(gnn_auc, 4),
               "fusion_val_auc": round(fusion_auc, 4),
               "mcmc_threshold": round(cal["threshold_mean"], 4),
               "precision": round(cal["precision"], 4),
               "recall": round(cal["recall"], 4),
               "train_seconds": round(time.time() - t0, 1)}
    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)
        torch.save(fusion.state_dict(), tmp / "fraudfusion.pt")
        torch.save(gnn.state_dict(), tmp / "gnn.pt")
        (tmp / "meta.json").write_text(json.dumps({
            "feature_version": feature_version_hash(),
            "dataset_sha256": manifest["dataset_sha256"],
            "threshold": cal["threshold_mean"],
            "threshold_ci": [cal["threshold_p05"], cal["threshold_p95"]],
            "metrics": metrics,
        }, indent=2))
        version = reg.register("fraudfusion", tmp, metrics,
                               meta={"data_source": manifest.get("source",
                                                                 "synthetic"),
                                     "dataset": manifest["dataset_sha256"]})
    if verbose:
        print(f"[train] registered fraudfusion v{version}: {metrics}")
    return {"version": version, "metrics": metrics, "threshold": cal}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--data", default="/tmp/pfa-data")
    ap.add_argument("--epochs", type=int, default=8)
    args = ap.parse_args()
    run_training(args.data, epochs=args.epochs)


if __name__ == "__main__":
    main()
