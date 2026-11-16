"""FraudFusion: multi-tower PyTorch fraud classifier.

Towers:
  1. Tabular MLP over the 20-dim engineered features
  2. GRU sequence encoder over the member's last 16 transactions
  3. GNN embedding produced by pfa_ml.models.gnn.GraphSAGEFraud (member-level
     fraud-risk embedding of the member-employer-agent-device graph)
Fusion: concat -> MLP head -> sigmoid fraud probability.

CPU-first: ~120k params, fp32.
"""
from __future__ import annotations

import torch
from torch import nn

from pfa_ml.features import TABULAR_DIM, SEQ_DIM, SEQ_LEN

EMB_DIM = 32


class TabularTower(nn.Module):
    def __init__(self, in_dim: int = TABULAR_DIM, out_dim: int = EMB_DIM):
        super().__init__()
        self.net = nn.Sequential(
            nn.Linear(in_dim, 64), nn.ReLU(), nn.Dropout(0.1),
            nn.Linear(64, out_dim), nn.ReLU())

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        return self.net(x)


class SequenceTower(nn.Module):
    def __init__(self, in_dim: int = SEQ_DIM, hidden: int = 32):
        super().__init__()
        self.gru = nn.GRU(in_dim, hidden, batch_first=True)
        self.proj = nn.Linear(hidden, EMB_DIM)

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        out, _ = self.gru(x)          # (B, L, H)
        return torch.relu(self.proj(out[:, -1]))


class FraudFusion(nn.Module):
    """Fuses tabular, sequence and GNN towers into one fraud probability."""

    def __init__(self, gnn_dim: int = EMB_DIM):
        super().__init__()
        self.tab = TabularTower()
        self.seq = SequenceTower()
        self.gnn_proj = nn.Linear(gnn_dim, EMB_DIM)
        self.head = nn.Sequential(
            nn.Linear(EMB_DIM * 3, 48), nn.ReLU(), nn.Dropout(0.1),
            nn.Linear(48, 1))

    def forward(self, tab: torch.Tensor, seq: torch.Tensor,
                gnn_emb: torch.Tensor) -> torch.Tensor:
        """Returns fraud probability (B,). gnn_emb: (B, gnn_dim) — zeros when
        the member is not in the graph (cold start)."""
        z = torch.cat([
            self.tab(tab), self.seq(seq),
            torch.relu(self.gnn_proj(gnn_emb)),
        ], dim=-1)
        return torch.sigmoid(self.head(z)).squeeze(-1)

    @torch.no_grad()
    def score(self, tab, seq, gnn_emb) -> float:
        self.eval()
        p = self.forward(torch.as_tensor(tab).unsqueeze(0),
                         torch.as_tensor(seq).unsqueeze(0),
                         torch.as_tensor(gnn_emb).unsqueeze(0))
        return float(p.item())
