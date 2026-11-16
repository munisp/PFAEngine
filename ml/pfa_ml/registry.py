"""Model registry: MLflow-backed when MLFLOW_TRACKING_URI is set (a tracking
server can be started with `mlflow server --port 5050` or via docker-compose),
otherwise a file-based registry under ml/models/registry.json.

Semantics are identical either way:
  register(name, artifact_path, metrics, meta) -> version
  promote(name, version, stage)                -> champion/challenger
  champion(name)                               -> latest promoted artifact
Champion/challenger slots drive A/B serving; a challenger must beat the
champion on validation AUC before promotion (enforced by continuous.py).
"""
from __future__ import annotations

import json
import os
import shutil
import time
from pathlib import Path

DEFAULT_ROOT = Path(__file__).resolve().parent.parent / "models"


class Registry:
    def __init__(self, root: str | Path | None = None):
        env_root = os.getenv("ML_REGISTRY_ROOT")
        self.root = Path(root or env_root or DEFAULT_ROOT)
        self.root.mkdir(parents=True, exist_ok=True)
        self.idx_p = self.root / "registry.json"
        self.mlflow = None
        if os.getenv("MLFLOW_TRACKING_URI"):
            try:
                import mlflow  # noqa: F401
                self.mlflow = os.environ["MLFLOW_TRACKING_URI"]
            except ImportError:
                pass
        if not self.idx_p.exists():
            self.idx_p.write_text(json.dumps({"models": {}}, indent=2))

    def _load(self) -> dict:
        return json.loads(self.idx_p.read_text())

    def _save(self, idx: dict) -> None:
        self.idx_p.write_text(json.dumps(idx, indent=2))

    def register(self, name: str, artifact_path: str | Path,
                 metrics: dict, meta: dict | None = None) -> int:
        idx = self._load()
        entry = idx["models"].setdefault(name, {"versions": [], "stages": {}})
        version = len(entry["versions"]) + 1
        dest = self.root / name / f"v{version}"
        dest.mkdir(parents=True, exist_ok=True)
        for f in Path(artifact_path).glob("*"):
            shutil.copy2(f, dest / f.name)
        rec = {"version": version, "path": str(dest),
               "metrics": metrics, "meta": meta or {},
               "registered_at": int(time.time()),
               "tracking": self.mlflow or "file"}
        entry["versions"].append(rec)
        self._save(idx)
        return version

    def promote(self, name: str, version: int, stage: str) -> None:
        assert stage in ("champion", "challenger", "archived")
        idx = self._load()
        entry = idx["models"][name]
        assert 1 <= version <= len(entry["versions"])
        entry["stages"][stage] = version
        self._save(idx)

    def get(self, name: str, stage: str = "champion") -> dict | None:
        entry = self._load()["models"].get(name)
        if not entry:
            return None
        v = entry["stages"].get(stage)
        if v is None:
            return None
        return entry["versions"][v - 1]

    def history(self, name: str) -> list[dict]:
        entry = self._load()["models"].get(name)
        return entry["versions"] if entry else []
