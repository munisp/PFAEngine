"""GraphSAGE-style GNN over the member-employer-agent-device graph.

Pure PyTorch (no torch_geometric) so it runs anywhere CPU-only:
  h_v^{k+1} = ReLU( W_self h_v^k + W_neigh mean_{u in N(v)} h_u^k )
Two layers -> 32-dim node embedding; member embeddings feed FraudFusion
and a standalone member-level fraud classifier (used for graph analytics).
"""
from __future__ import annotations

import torch
from torch import nn

FEAT_DIM = 7
HID = 32


class SAGELayer(nn.Module):
    def __init__(self, in_dim: int, out_dim: int):
        super().__init__()
        self.self_w = nn.Linear(in_dim, out_dim)
        self.neigh_w = nn.Linear(in_dim, out_dim)

    def forward(self, x: torch.Tensor, edge_index: torch.Tensor) -> torch.Tensor:
        n = x.size(0)
        src, dst = edge_index[0], edge_index[1]
        agg = torch.zeros_like(x)
        agg.index_add_(0, dst, x[src])
        deg = torch.zeros(n, device=x.device)
        deg.index_add_(0, dst, torch.ones_like(dst, dtype=torch.float))
        agg = agg / deg.clamp(min=1).unsqueeze(1)
        return torch.relu(self.self_w(x) + self.neigh_w(agg))


class GraphSAGEFraud(nn.Module):
    def __init__(self, in_dim: int = FEAT_DIM, emb_dim: int = 32):
        super().__init__()
        self.l1 = SAGELayer(in_dim, HID)
        self.l2 = SAGELayer(HID, emb_dim)
        self.member_head = nn.Sequential(
            nn.Linear(emb_dim, 16), nn.ReLU(), nn.Linear(16, 1))
        self.emb_dim = emb_dim

    def embed(self, x: torch.Tensor, edge_index: torch.Tensor) -> torch.Tensor:
        return self.l2(self.l1(x, edge_index), edge_index)

    def forward(self, x: torch.Tensor, edge_index: torch.Tensor,
                member_idx: torch.Tensor) -> torch.Tensor:
        """Fraud probability for the member nodes at member_idx."""
        emb = self.embed(x, edge_index)
        return torch.sigmoid(self.member_head(emb[member_idx])).squeeze(-1)
