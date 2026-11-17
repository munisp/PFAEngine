# Analytics & Lakehouse — Maturity Assessment and Architecture

## 1. Pre-change maturity assessment (honest score)

| Capability | Score (0–10) | Evidence |
|---|---|---|
| **Lakehouse** | **1 / 10** | No general lakehouse existed. The only parquet zone was `ml/pfa_ml/extract.py`: two hard-coded tables (`contribution`, `rsa_account`) pulled into a `training/` zone for FraudFusion only. No bronze/silver/gold layering, no partitioning, no watermarks, no reuse outside ML, no failure isolation (one bad table aborts the extract), no BI access path. |
| **Advanced analytics / OLAP** | **3 / 10** | Real ML existed (fraud model, drift, registry, KGQA) — genuine analytical capability — but there was **no OLAP engine, no BI query layer, no business aggregates** (no AUM series, no SLA compliance, no funnel, no employer health). Operational questions were answered by clicking through CRUD queues, not by querying a metrics model. |
| **Data viz / dashboards** | **2 / 10** | The staff portal has competent *operational* views (cases, recon, funds NAV with a hand-rolled SVG sparkline) but zero analytics dashboards, no Grafana/Metabase/Superset, no charting beyond one sparkline. |

**Net**: strong transactional core, real ML, but no analytical data plane.

## 2. What was built (post-change state)

| Capability | Score | What now exists |
|---|---|---|
| **Lakehouse** | **6 / 10** | Medallion lakehouse in `analytics/`: bronze = every operational table (enumerated by parsing `db/migrations/*.sql` — new migrations land automatically) extracted to hive-partitioned parquet `lake/bronze/<table>/dt=YYYY-MM-DD/`, incremental watermarks for append-only/mutable tables, full-refresh idempotent partitions for small dimensions, per-table fault isolation with a run manifest. Silver = cleaned/typed/deduped conformed models in DuckDB SQL. Gold = 7 business aggregates. Compute is DuckDB (zero-infra OLAP) reading parquet directly. |
| **Advanced analytics / OLAP** | **6 / 10** | DuckDB OLAP over the lake; gold models for member growth, contribution monthly by employer, fund AUM daily, case SLA compliance, KYC funnel, employer remittance health, retirement pipeline; gold exported back to Postgres schema `analytics` so any SQL tool can query it; `analytics-svc` (:8097) exposes read-only JSON over the gold schema with a fail-closed 503 contract. |
| **Data viz / dashboards** | **6 / 10** | Grafana-as-code (`deploy/grafana/provisioning`): env-driven Postgres datasource + 3 dashboards (platform-overview, operations, employer-health) auto-provisioned; staff-portal `Analytics` view with stat cards, hand-rolled SVG charts (matching the codebase's existing no-chart-library convention), KYC funnel, employer health and fund AUM tables. |

Scores are deliberately not 9–10: see §5 gaps.

## 3. Data flow

```
┌────────────────────────────────────────────────────────────────────┐
│ Postgres 18 (OLTP — migrations 0001..0009, integer kobo, DA-03)    │
└──────────────┬─────────────────────────────────────────────────────┘
               │ psycopg2, per-table fault isolation, watermarks
               ▼
   lake/bronze/<table>/dt=YYYY-MM-DD/*.parquet      (raw, hive-partitioned)
               │ duckdb read_parquet(hive_partitioning=1, union_by_name)
               ▼
   silver_*  (members, contributions, employers, cases, funds_nav,
              kyc_sessions — deduped by PK, latest partition wins)
               │ models/gold/*.sql
               ▼
   gold_*    (member_growth_daily, contribution_monthly_by_employer,
              fund_aum_daily, case_sla_compliance, kyc_funnel,
              employer_remittance_health, retirement_pipeline)
               │ psycopg2 full-refresh export (CREATE SCHEMA IF NOT EXISTS
               │ analytics; drop+create per table inside one transaction)
               ▼
┌──────────────────────────┴─────────────────────────────────────────┐
│ Postgres schema `analytics`  — the governed BI interface           │
└──────┬───────────────────────────────────┬─────────────────────────┘
       ▼                                    ▼
  Grafana :3001                      analytics-svc :8097
  (provisioned dashboards:           (read-only JSON; 503
   platform-overview, operations,     ANALYTICS_NOT_READY until
   employer-health)                   populated — never fake data)
                                            ▼
                                     staff-portal Analytics view
```

Design decisions and why:

- **Postgres as the serving layer, not parquet.** BI tools and the portal
  query `analytics.*` in Postgres: credentials stay governed, the read
  path is identical to every other service, and parquet remains an
  internal lake implementation detail.
- **DuckDB for silver/gold compute.** Zero additional infrastructure,
  excellent parquet + hive-partition support, and the SQL files are
  portable to Spark/Trino later if volumes demand it.
- **Full refresh, one transaction per gold table.** Readers never see a
  half-loaded or empty table; the analytics schema is always consistent.
- **Fail-closed everywhere.** analytics-svc returns 503 when the schema
  is missing; the staff view renders setup instructions instead of fake
  charts; bronze extraction degrades per-table rather than aborting.
- **Integer kobo / basis points end-to-end**, naira only at presentation.

## 4. Refresh cadence options

| Mode | How | Latency | When to use |
|---|---|---|---|
| **One-shot** | `docker compose run analytics-run` or `python3 analytics/run.py --dsn … --lake ./lake` | manual | dev, demos, ad-hoc |
| **Nightly cron (recommended default)** | cron/systemd timer running the compose one-shot or the CLI | T+1 day | PenCom-style reporting cadence; contributions post in batches anyway |
| **Hourly cron** | same, `0 * * * *` | ~1 h | ops dashboards during business hours |
| **Continuous (future)** | Debezium CDC from Postgres WAL → existing Kafka → streaming sink into bronze | seconds | when ops needs near-real-time SLA/fraud dashboards |

Incremental extraction is watermark-based (`updated_at`/event-time
columns), so hourly runs stay cheap; full-refresh dimensions are small.

## 5. Remaining gaps (not built — honest list)

1. **No streaming CDC yet.** Bronze is micro-batch per run. Upgrade path:
   Debezium on the existing Kafka cluster writing change events into
   bronze; silver/gold SQL is already replay-safe (PK dedupe).
2. **Gold is full-refresh.** Fine at current volumes; at ~10⁸ ledger rows
   move gold to incremental merges or a columnar store.
3. **No dbt.** Models are plain DuckDB SQL files with an ordering
   convention (silver NN_ → gold NN_). dbt-duckdb can adopt these files
   almost verbatim if the team wants lineage/tests-as-code.
4. **No data catalog / governance layer** (no OpenLineage, no DataHub).
   `lake/bronze/_manifest.json` is the only lineage artefact today.
5. **`unit_holding` has no history** — historical AUM uses current units ×
   historical NAV (flagged in `models/silver/05_funds_nav.sql`). A
   holdings snapshot table is a schema change for a future tranche.
6. **No object-storage lake.** Bronze lives on a filesystem volume; S3/
   MinIO (already in the stack) is the natural durable home — the
   extractor writes through `pathlib`, so this is a mount change, not a
   code change.
7. **Analytics schema is cross-tenant.** Gold aggregates span tenants
   (platform-ops view). Per-tenant analytics would add `tenant_id`
   filters in gold SQL + scoping in analytics-svc.
8. **eKYC silver model depends on JSON payload shape** in
   `internal/ekyc` (state_snapshot blobs). Extraction is defensive
   (`TRY_CAST`, COALESCE) but schema drift there is a manual sync point.

## 6. Verification

- `python3 -m unittest discover analytics` — fixture-lake tests prove
  every silver + gold model executes in in-memory DuckDB and produces
  correct numbers (dedupe ordering, kobo AUM math, SLA bps, funnel
  counts); Postgres export test guarded by `TEST_DATABASE_URL`.
- `go build ./cmd/analytics-svc/ && go test ./cmd/analytics-svc/` —
  handler tests with an in-memory fake querier (fail-closed 503, unknown
  metric 400, happy paths).
- All dashboard JSON parses; compose + provisioning YAML parses.
