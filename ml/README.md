# PFAEngine ML/AI stack — PyTorch (CPU-first) fraud/credit/GNN platform

Real end-to-end stack: synthetic Nigerian-pattern data generation, production
Postgres -> lakehouse extraction, FraudFusion (tabular MLP + GRU sequence
encoder + GNN embedding), GraphSAGE-style GNN, MCMC Bayesian threshold
calibration, MLflow-compatible model registry with champion/challenger,
A/B routing, drift monitoring (PSI/KS), CPU inference service, Ray
distributed training (local fallback), and a continuous-training driver
that retrains from platform data.

## Layout

- `pfa_ml/synthgen.py` — realistic Nigerian pension synthetic data (salary-cycle
  timing, 36 states + FCT, employer payroll batches, fraud archetypes:
  dormant-then-large, velocity spike, geo-jump, agent bust-out, account takeover)
- `pfa_ml/features.py` — deterministic tabular/sequence feature encoding
- `pfa_ml/graph.py` — member–employer–agent–device graph construction
- `pfa_ml/models/` — FraudFusion, GNN (GraphSAGE, pure PyTorch), MCMC calibrator
- `pfa_ml/extract.py` — production Postgres -> parquet lakehouse extraction
- `pfa_ml/train.py` — real training loops -> versioned artifacts + registry
- `pfa_ml/registry.py` — MLflow tracking if MLFLOW_TRACKING_URI set, else
  file-based registry (models/registry.json) with champion/challenger slots
- `pfa_ml/serve.py` — CPU inference HTTP service (champion model, A/B split,
  graceful fallback signal so Go falls back to the rules engine)
- `pfa_ml/drift.py` — PSI + KS drift detection with alert thresholds
- `pfa_ml/abtest.py` — deterministic hash-split A/B + two-proportion z-test
- `pfa_ml/raydist.py` — Ray data-parallel training when ray is installed,
  single-process fallback otherwise
- `pfa_ml/continuous.py` — extract -> train -> evaluate -> promote-if-better
- `tests/` — unittest suite (no GPU, no network, no PG required)

## Quick start

    pip install -r ml/requirements.txt
    python -m pfa_ml.synthgen --out /tmp/pfa-data --members 2000 --months 18
    python -m pfa_ml.train --data /tmp/pfa-data --epochs 5
    python -m pfa_ml.serve --port 8077
    python -m pfa_ml.continuous --dsn postgres://... --interval 3600

## Design constraints

- CPU-only inference: all models < 1M params, fp32, torch.set_num_threads(4)
- Integer-kobo amounts at the edge; features normalized inside the encoder
- Fail-closed: serve returns `degraded: true` when no champion is registered;
  the Go mlscore client then falls back to the rules engine (internal/fraud)
- Every training run records: dataset hash, code version, metrics, artifact path
