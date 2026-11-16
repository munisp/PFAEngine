"""Nightly batch job: compute GNN member embeddings for production serving.

Builds the graph from the latest extraction/synthetic dataset, loads the
champion GNN, and writes member_id -> 32-dim embedding JSON that the Go
service (or serve.py callers) uses as the `gnn_embedding` input. This is
the production pattern: embeddings are computed offline per batch, never
from a model that trained on the member's own label.
"""
from __future__ import annotations

import argparse
import json
from pathlib import Path

import torch

from pfa_ml.graph import FraudGraph
from pfa_ml.models.gnn import GraphSAGEFraud
from pfa_ml.registry import Registry


def compute_embeddings(data_dir: str | Path, out: str | Path) -> dict:
    reg = Registry()
    rec = reg.get("fraudfusion", "champion")
    if rec is None:
        raise RuntimeError("no champion registered")
    gnn = GraphSAGEFraud()
    gnn.load_state_dict(torch.load(f"{rec['path']}/gnn.pt",
                                   map_location="cpu", weights_only=True))
    gnn.eval()
    g = FraudGraph.build(data_dir)
    with torch.no_grad():
        emb = gnn.embed(torch.as_tensor(g.node_feat),
                        torch.as_tensor(g.edge_index())).numpy()
    result = {mid: emb[i].tolist() for mid, i in g.member_idx.items()}
    Path(out).write_text(json.dumps(result))
    return {"members_embedded": len(result), "out": str(out)}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--data", default="/tmp/pfa-data")
    ap.add_argument("--out", default="/tmp/member_embeddings.json")
    args = ap.parse_args()
    print(json.dumps(compute_embeddings(args.data, args.out)))


if __name__ == "__main__":
    main()
