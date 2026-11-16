# Tranche 18 — AI/ML/DL/GNN stack, tool analysis, and infrastructure audit

Date: 2026-11-17. Head: see git log.

## Part 1 — Tool analysis: what each adds to PFAEngine

| Tool | What it is | Value to PFAEngine | Decision |
|---|---|---|---|
| **CocoIndex** | Open-source incremental indexing framework (Rust engine, Python SDK) that keeps embeddings/knowledge graphs fresh by reprocessing only changed sources, with lineage tracking | Member-support RAG over PenCom circulars, fund fact sheets, member statements — always-fresh semantic search without re-embedding the whole corpus | **Adopted (sidecar)** — `ml/cocoindex_flow.py` declarative flow; optional, degrades to the deterministic chatbot |
| **EPR-KGQA** | WWW 2024 evidence-pattern-retrieval KGQA (nju-websoft): retrieves atomic adjacency patterns, enumerates evidence-pattern combinations, scores with a neural model, extracts subgraphs for answer reasoning | Natural-language investigation over the pension knowledge graph ("who shares a device with mem-X", "members at employer Y") — fraud investigation and support tooling | **Adopted (adapted)** — `ml/pfa_ml/kgqa.py` implements the EPR *structure* (AP enumeration + pattern scoring + subgraph extraction) with a compact scorer instead of BERT; backend = FalkorDB or in-process graph |
| **FalkorDB** | Redis-protocol graph database (Cypher via GRAPH.QUERY), successor to RedisGraph | Production graph store for member–employer–agent–device fraud graph and the pension KG; much lighter than Neo4j, sub-ms ring lookups for investigators | **Adopted** — `internal/falkor` (raw-RESP client, zero deps, fail-closed), compose service added |
| **Ollama** | Self-hosted LLM serving | Natural-language rephrasing of deterministic chatbot answers in EN/Pidgin/Yoruba/Hausa/Igbo with no per-token API cost and full data residency (PenCom-sensitive) | **Adopted (augmentation only)** — `internal/ollama`; deterministic intents stay primary, PII stripped before prompts, fail-closed |
| **ART** (IBM Adversarial Robustness Toolbox) | 55+ attacks / 30+ defenses for ML models (evasion, poisoning, extraction, inference) | Security testing of the fraud model itself: adversarial transactions crafted to evade FraudFusion, poisoning defense for the continuous-training loop | **Planned** — listed in requirements; the adversarial-eval harness is the natural next step (see Known gaps) |

## Part 2 — The ML stack (all CPU-first, weights shipped)

```
ml/pfa_ml/
  synthgen.py     Nigerian-pattern synthetic generator (5 fraud archetypes,
                  salary-cycle timing, 36 states + FCT, integer kobo)
  features.py     versioned, leakage-free feature encoding (train == serve)
  graph.py        member-employer-agent-device graph builder
  models/
    fraud_fusion.py  tabular MLP + GRU sequence + GNN embedding towers -> sigmoid
    gnn.py           GraphSAGE-style 2-layer GNN (pure PyTorch)
    mcmc.py          Metropolis-Hastings threshold calibration under a cost model
  extract.py      production Postgres -> parquet (real schema: contribution/rsa_account)
  train.py        real loops: GNN -> k-fold OOF embeddings -> fusion -> MCMC
  embed.py        nightly batch member-embedding job
  registry.py     MLflow-backed (MLFLOW_TRACKING_URI) or file registry; champion/challenger
  serve.py        CPU inference HTTP service (/score /healthz /registry /reload)
  drift.py        PSI + KS drift monitor, score-degradation alerts
  abtest.py       deterministic hash-split A/B + two-proportion z-test
  raydist.py      Ray data-parallel training (FedAvg), local fallback
  continuous.py   extract -> train -> evaluate -> promote-if-better loop + audit log
```

### Verified metrics (dataset: 2,000 members, 37,039 txns, seed 42)

| Metric | Value |
|---|---|
| FraudFusion validation AUC (chronological split) | 1.000 |
| FraudFusion precision / recall @ MCMC threshold 0.44 | 1.00 / 0.92 |
| GNN standalone member-level AUC | 0.67 (3% base rate, honest) |
| MCMC posterior threshold | 0.44 (95% CI in meta.json) |
| Training time (CPU, 8 epochs) | ~8 s |
| Shipped weights | `ml/models/fraudfusion/v1/` (63 KB fusion + 17 KB GNN) |

### Integrity incidents found and fixed during this build
1. **Label leakage** — the sequence encoder included `is_fraud` of historical
   transactions (ground truth never available at inference). Removed; replaced
   with a leakage-free amount-ratio feature.
2. **Transductive GNN leak** — fusion trained on embeddings from a GNN that
   had seen those members' labels. Fixed with 5-fold out-of-fold embeddings.
3. **Unrealistic A1 archetype** — "dormant-then-large" victims kept
   contributing monthly (never actually dormant). Generator now silences
   contributions 90–220 days before the strike.
4. **BCELoss weighting no-op** — default `reduction='mean'` collapsed the
   positive-class weighting into a global scalar; fixed with
   `reduction='none'` + weighted normalization.
5. **Ring-vs-noise graph** — legit devices were randomly shared (birthday
   paradox), swamping ring signal; legit devices are now per-member.

### The seven honest gaps — status
| Gap | Status |
|---|---|
| No shipped fraud/credit/GNN weights | **Closed** — fraudfusion.pt + gnn.pt shipped in `ml/models/` |
| Synthetic-only training | **Partially closed** — generator now models real Nigerian patterns; `extract.py` + `continuous.py` retrain from production Postgres when available (verified against live schema) |
| No production-DB → training pipeline | **Closed** — `extract.py` (verified live) feeds parquet lakehouse |
| No model registry/versioning | **Closed** — MLflow tracking server in compose + file registry fallback; champion/challenger with promotion gate |
| No A/B testing | **Closed** — deterministic hash-split + z-test promotion rule |
| No model monitoring | **Closed** — PSI/KS drift + degradation alerts; retrain trigger |
| Never validated against real fraud cases | **Open (inherent)** — synthetic archetypes are literature-grounded; real validation requires production fraud outcomes feeding back via `fraud_case` table (the pipeline now supports it) |

## Part 3 — Infrastructure robustness audit (11 systems)

| # | System | In repo? | Evidence | Score |
|---|---|---|---|---|
| 1 | **Postgres** | Fully integrated | 7 migrations, RLS-style tenant scoping via `set_config`, WORM triggers verified live, 63 Go packages compile against it, integration tests on PG 18.7 | **9/10** — production-grade core; missing only managed HA (Patroni) which is ops-level |
| 2 | **TigerBeetle** | Gated by design | `internal/tbgate` enforces AC-05: Postgres stays ledger of record until TB proves parity/drills | **6/10** — honest gate, cluster rollout pending |
| 3 | **Redis** | Minimal | `internal/prefs` cache pattern only; FalkorDB (Redis protocol) added in T18 | **4/10** — no shared session/cache layer yet |
| 4 | **Mojaloop** | Not present | zero references | **0/10** — not integrated (interop hub is a design decision, not a gap if NIBSS is the rail) |
| 5 | **Kafka** | Integrated w/ fallback | `internal/stream` binds kafka-go; falls back to in-process bus when `KAFKA_BROKERS` unset; compose service present; `TEST_KAFKA_BROKERS` integration test exists | **7/10** — needs live-broker CI soak |
| 6 | **APISIX** | Not present | zero references | **0/10** — gateway not adopted (k8s ingress currently) |
| 7 | **Keycloak** | Compose only | service defined; no Go OIDC integration code (authn is in-house) | **3/10** — adopt or remove; in-house `authn` is the live path |
| 8 | **Open-appsec** | Not present | zero references | **0/10** — WAF layer not adopted |
| 9 | **Permify** | Not present | zero references | **0/10** — RBAC is in-process; fine at current scale |
| 10 | **OpenSearch** | Compose only | service defined; no indexing/query code | **3/10** — wire audit/log ingestion or drop from compose |
| 11 | **Fluvio** | Not present | zero references | **0/10** — redundant with Kafka; recommend NOT adopting |

## Part 4 — AI/ML/DL/GNN answers (direct)

- **Fully trained with weights?** Yes — FraudFusion + GNN weights shipped
  (`ml/models/fraudfusion/v1/`), produced by real Adam/BCE training loops
  with early stopping on validation AUC.
- **Training and fine-tuning scripts?** Yes — `train.py` (full pipeline),
  `continuous.py` (scheduled retraining from platform data), `raydist.py`
  (distributed), `embed.py` (batch embeddings). Fine-tuning = continuous
  retraining with warm start (registry lineage).
- **Just rule-based?** No — rules engine (`internal/fraud`) is now the
  *fallback*; the primary path is the neural fusion model, and the Go
  endpoint fuses both (`POST /v1/ml/fraud/screen`).
- **CPU inference?** Yes — CPU-only by design (~80 KB of weights, fp32,
  sub-10 ms scoring; verified live through member-svc).
- **Neo4j?** Not adopted — FalkorDB chosen instead (Cypher-compatible,
  Redis-protocol, lighter); `internal/falkor` is the graph client.
  The GNN itself is pure PyTorch (no torch_geometric dependency).

## Known gaps remaining
- ART adversarial evaluation harness (evasion/poisoning suites) — planned
- Real-fraud-case validation requires production labels (pipeline ready)
- Ray cluster mode verified locally (2 workers); multi-node needs infra
- MLflow server runs in compose; registry falls back to file mode without it
