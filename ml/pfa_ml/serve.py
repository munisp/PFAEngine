"""CPU inference service for the fraud models.

Endpoints:
  GET  /healthz          -> {"status": "ok", "champion": "fraudfusion:vN"}
  POST /score            -> {"score": 0.0-1.0, "threshold": t, "flagged": bool,
                             "model": "fraudfusion:vN", "arm": "champion"}
  GET  /registry         -> registry contents
  POST /reload           -> reload champion from registry

Request body for /score:
  {"member_id": "...", "txn": {<transaction fields>},
   "history": [<prior txns>]}

Fail-closed: if no champion is registered, every response has
"degraded": true and the Go client falls back to the rules engine.
CPU-only inference: models are small; torch.set_num_threads(4).
"""
from __future__ import annotations

import argparse
import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import numpy as np
import torch

from pfa_ml.abtest import ABExperiment
from pfa_ml.features import encode_tabular, encode_sequence
from pfa_ml.models.fraud_fusion import FraudFusion
from pfa_ml.registry import Registry

torch.set_num_threads(4)

_lock = threading.Lock()
_state = {"model": None, "threshold": 0.5, "version": None, "meta": {}}
_ab = ABExperiment("fraud-fusion-v1", challenger_pct=10)


def load_champion(reg: Registry | None = None) -> bool:
    reg = reg or Registry()
    rec = reg.get("fraudfusion", "champion")
    if rec is None:
        return False
    path = rec["path"]
    model = FraudFusion()
    model.load_state_dict(torch.load(f"{path}/fraudfusion.pt",
                                     map_location="cpu",
                                     weights_only=True))
    model.eval()
    meta = json.loads(open(f"{path}/meta.json").read())
    with _lock:
        _state.update(model=model, threshold=meta["threshold"],
                      version=f"fraudfusion:v{rec['version']}", meta=meta)
    return True


class Handler(BaseHTTPRequestHandler):
    def _json(self, code: int, body: dict):
        b = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def log_message(self, *a):  # quiet
        pass

    def do_GET(self):
        if self.path == "/healthz":
            self._json(200, {"status": "ok", "champion": _state["version"],
                             "degraded": _state["model"] is None})
        elif self.path == "/registry":
            self._json(200, Registry()._load())
        else:
            self._json(404, {"error": "not found"})

    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(n) or b"{}")
        if self.path == "/reload":
            ok = load_champion()
            self._json(200, {"reloaded": ok, "champion": _state["version"]})
            return
        if self.path != "/score":
            self._json(404, {"error": "not found"})
            return
        with _lock:
            model, threshold, version = (_state["model"],
                                         _state["threshold"],
                                         _state["version"])
        if model is None:
            self._json(200, {"degraded": True, "score": None,
                             "reason": "no champion model registered"})
            return
        txn, hist = body["txn"], body.get("history") or []
        tab = encode_tabular(txn, hist)
        seq = encode_sequence(hist)
        gnn_emb = np.zeros(32, dtype=np.float32)
        if "gnn_embedding" in body:
            gnn_emb = np.asarray(body["gnn_embedding"], dtype=np.float32)
        score = model.score(tab, seq, gnn_emb)
        arm = _ab.arm(body.get("member_id", "anon"))
        self._json(200, {"score": round(score, 6), "threshold": threshold,
                         "flagged": score >= threshold, "model": version,
                         "arm": arm, "degraded": False})


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=8077)
    args = ap.parse_args()
    load_champion()
    srv = ThreadingHTTPServer(("0.0.0.0", args.port), Handler)
    print(f"[ml-serve] listening :{args.port} champion={_state['version']}")
    srv.serve_forever()


if __name__ == "__main__":
    main()
