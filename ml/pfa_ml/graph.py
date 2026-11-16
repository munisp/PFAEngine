"""Member-employer-agent-device graph construction for the GNN.

Node types share one index space:
  members:   0 .. M-1
  employers: M .. M+E-1
  agents:    M+E .. M+E+A-1
  devices:   M+E+A .. end
Edges (undirected, stored both directions):
  member -[employed_by]-> employer
  member -[onboarded_by]-> agent
  txn    -[on_device]-> device  (folded to member-device edges)
Fraud label propagation target: member nodes.
"""
from __future__ import annotations

import json
from pathlib import Path

import numpy as np


class FraudGraph:
    def __init__(self):
        self.member_idx: dict[str, int] = {}
        self.ent_idx: dict[str, int] = {}
        self.edges: list[tuple[int, int]] = []
        self.node_feat: np.ndarray | None = None
        self.member_labels: np.ndarray | None = None

    @classmethod
    def build(cls, data_dir: str | Path) -> "FraudGraph":
        data_dir = Path(data_dir)
        members = [json.loads(l) for l in
                   open(data_dir / "members.jsonl")]
        txns = [json.loads(l) for l in
                open(data_dir / "transactions.jsonl")]
        g = cls()
        for i, m in enumerate(members):
            g.member_idx[m["member_id"]] = i
        offset = len(members)
        for e in sorted({m["employer_id"] for m in members}
                        | {m["agent_id"] for m in members}
                        | {t["device_id"] for t in txns}):
            g.ent_idx[e] = offset
            offset += 1
        n_nodes = offset
        fraud_by_member: dict[str, int] = {m["member_id"]: 0 for m in members}
        for t in txns:
            mi = g.member_idx[t["member_id"]]
            di = g.ent_idx[t["device_id"]]
            g.edges.append((mi, di))
            g.edges.append((di, mi))
            if t.get("is_fraud"):
                fraud_by_member[t["member_id"]] = 1
        for m in members:
            mi = g.member_idx[m["member_id"]]
            for key in ("employer_id", "agent_id"):
                ei = g.ent_idx[m[key]]
                g.edges.append((mi, ei))
                g.edges.append((ei, mi))
        # node features: [is_member, is_employer, is_agent, is_device,
        #                 log_salary, micro, degree(placeholder)]
        feat = np.zeros((n_nodes, 7), dtype=np.float32)
        for m in members:
            i = g.member_idx[m["member_id"]]
            feat[i] = [1, 0, 0, 0,
                       np.log1p(m["salary_monthly_kobo"] / 100.0) / 12.0,
                       1.0 if m["micro_pension"] else 0.0, 0]
        for e, i in g.ent_idx.items():
            pref = e.split("-", 1)[0]
            feat[i] = [0, 1.0 if pref == "emp" else 0,
                       1.0 if pref == "agt" else 0,
                       1.0 if pref.startswith("dev") else 0,
                       0, 0, 0]
        deg = np.zeros(n_nodes, dtype=np.float32)
        for a, _ in g.edges:
            deg[a] += 1
        feat[:, 6] = np.log1p(deg) / 6.0
        g.node_feat = feat
        g.member_labels = np.asarray(
            [fraud_by_member[m["member_id"]] for m in members],
            dtype=np.int64)
        return g

    def edge_index(self) -> np.ndarray:
        return np.asarray(self.edges, dtype=np.int64).T  # (2, E)

    def member_ids(self) -> list[str]:
        inv = sorted(self.member_idx.items(), key=lambda kv: kv[1])
        return [k for k, _ in inv]
