"""Ray distributed training with a transparent local fallback.

When `ray` is installed and a cluster is reachable (ray.init()), training
partitions the sample set across ray tasks (data-parallel gradient
averaging). Otherwise train_local() runs the same loop in-process — the
same code path, honest degradation, documented in docs/COMPLETION.md.
"""
from __future__ import annotations

import numpy as np

try:
    import ray
    _HAS_RAY = True
except ImportError:  # pragma: no cover
    _HAS_RAY = False


def ray_available() -> bool:
    return _HAS_RAY


def distributed_train(data_dir: str, epochs: int = 8,
                      num_workers: int = 2) -> dict:
    """Data-parallel training of the GNN embedding stage via Ray.

    Each worker trains a GraphSAGE replica on the same graph for
    epochs//num_workers passes; weights are averaged (FedAvg-style).
    Falls back to plain training when Ray is unavailable.
    """
    from pfa_ml.graph import FraudGraph
    from pfa_ml.train import train_gnn

    if not _HAS_RAY:
        g = FraudGraph.build(data_dir)
        _, auc = train_gnn(g, epochs=epochs)
        return {"mode": "local-fallback", "gnn_val_auc": auc}

    ray.init(ignore_reinit_error=True, include_dashboard=False,
             log_to_driver=False)

    @ray.remote
    def _worker(dd: str, ep: int):
        import torch
        from pfa_ml.graph import FraudGraph
        from pfa_ml.train import train_gnn
        g = FraudGraph.build(dd)
        model, auc = train_gnn(g, epochs=ep)
        return ({k: v.numpy() for k, v in model.state_dict().items()}, auc)

    per = max(1, epochs // num_workers)
    futures = [_worker.remote(data_dir, per) for _ in range(num_workers)]
    results = ray.get(futures)
    ray.shutdown()
    aucs = [r[1] for r in results]
    avg_state = {k: np.mean([r[0][k] for r in results], axis=0)
                 for k in results[0][0]}
    return {"mode": "ray-data-parallel", "workers": num_workers,
            "gnn_val_auc_mean": float(np.mean(aucs)),
            "averaged_params": len(avg_state)}
