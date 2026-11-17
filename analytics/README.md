# PFAEngine Analytics Lakehouse

Medallion lakehouse **on top of the existing ML extract pattern**
(`ml/pfa_ml/extract.py`). Where the ML pipeline only pulls two tables into
a `training/` zone for FraudFusion, this layer extracts **every
operational table** into a governed lake, models it in DuckDB, and exports
business aggregates back into Postgres (`analytics` schema) so BI tools
(Grafana) and the staff portal query Postgres — never parquet files.

```
Postgres (OLTP, migrations 0001-0009)
   │  extract_full.py  (psycopg2, per-table fault isolation)
   ▼
lake/bronze/<table>/dt=YYYY-MM-DD/*.parquet     (raw, hive-partitioned, watermarked)
   │  run.py → duckdb, models/silver/*.sql
   ▼
silver_*  (cleaned, typed, deduped, conformed — in DuckDB)
   │  models/gold/*.sql
   ▼
gold_*    (business aggregates)
   │  run.py export (psycopg2, CREATE SCHEMA IF NOT EXISTS analytics)
   ▼
Postgres schema `analytics`  ──►  Grafana (provisioned dashboards)
                              └─  analytics-svc (:8097) → staff-portal Analytics view
```

## Layout

| Path | What |
|---|---|
| `extract_full.py` | Postgres → bronze parquet. Enumerates tables by parsing `db/migrations/*.sql`; incremental tables use a watermark (`_watermark.json`), small mutable dimensions are full-refreshed per `dt=` partition (idempotent). Per-table failures are logged into `_manifest.json` and replaced with an empty placeholder parquet — **bronze never blocks on one bad table**. |
| `models/silver/*.sql` | Cleaned/typed conformed models: `members`, `contributions`, `employers`, `cases`, `funds_nav`, `kyc_sessions`. Each dedupes bronze partitions by primary key (`row_number() ... ORDER BY dt DESC`) so both incremental and full-refresh loads are correct. |
| `models/gold/*.sql` | Business aggregates: `member_growth_daily`, `contribution_monthly_by_employer`, `fund_aum_daily`, `case_sla_compliance`, `kyc_funnel`, `employer_remittance_health`, `retirement_pipeline`. |
| `run.py` | Orchestrator: extract → silver → gold → export gold to Postgres `analytics` schema (full refresh, one transaction per table — readers never see an empty window). |
| `lakeio.py` | Lake I/O adapter: when `LAKE_URI` is set (e.g. `s3://bucket/lake`, `fs:///lake`, `memory://`, `gcs://…`, `azblob://…`) all bronze reads/writes go through Apache OpenDAL (`opendal` pip package); otherwise plain local FS — unchanged behaviour. Silver/gold DuckDB builds read parquet from a local path, so remote lakes need `fs://` for model builds. See docs/OPENDAL.md. |
| `lakehouse_test.py` | Unit tests: fixture parquet lake → all silver+gold SQL in in-memory DuckDB; Postgres export guarded by `TEST_DATABASE_URL`. (`test_lakehouse.py` is only a discovery shim — unittest's default pattern is `test*.py`.) |
| `lakeio_test.py` | lakeio tests: memory:// and fs:// round-trips, CDC applier against an OpenDAL lake, local fallback unchanged. (`test_lakeio.py` shim.) |
| `requirements.txt` | duckdb, psycopg2-binary, pyarrow, pandas, kafka-python, opendal (optional — only used when `LAKE_URI` is set). |

## Conventions

- **Money is integer kobo everywhere** (DA-03). `aum_kobo`, `gross_kobo`,
  etc. stay BIGINT; naira formatting happens only in dashboards
  (Grafana `currency` units / `naira()` in the staff portal).
- Percentages that must be presentation-safe are integer **basis points**
  (`sla_compliance_bps`, `schedule_posted_bps`), matching the codebase's
  no-float convention. Derived informational metrics (`avg_resolution_hours`)
  may be doubles.
- Units in `unit_holding` are 8-decimal bigint (`1e8 = 1 unit`);
  `aum_kobo = units_raw * price_kobo / 1e8` (computed in HUGEINT, cast back
  to BIGINT).
- SQL files contain a `{{LAKE}}` placeholder substituted by `run.py` /
  tests with the absolute lake path; models are otherwise plain DuckDB SQL
  (`read_parquet(..., hive_partitioning=1, union_by_name=true)`).

## Running

```bash
pip install -r analytics/requirements.txt

# Full run: extract from Postgres, build models, export to analytics schema
python3 analytics/run.py --dsn postgres://postgres:pfaengine@localhost:5432/pfaengine --lake ./lake

# Rebuild models only (lake already populated)
python3 analytics/run.py --lake ./lake --skip-extract

# Models without the Postgres export (local dev)
python3 analytics/run.py --lake ./lake --skip-extract --skip-export

# Extract only
python3 analytics/extract_full.py --dsn ... --lake ./lake --ds 2024-06-01

# Tests (no Postgres needed)
python3 -m unittest discover analytics
# With a scratch Postgres for the export round-trip:
TEST_DATABASE_URL=postgres://... python3 -m unittest discover analytics
```

`docker compose -f deploy/docker-compose.yml up analytics-run` runs the
same pipeline one-shot inside the stack; schedule it via cron/systemd for
nightly refresh (see `docs/ANALYTICS.md` for cadence options).

## Known limitations (honest list)

- **Full-refresh gold**: every run recomputes all gold models. Fine at
  pension-platform volumes; revisit if tables exceed ~10⁸ rows.
- **`unit_holding` is a current snapshot**, so historical `fund_aum_daily`
  = current units × historical NAV. True historical AUM needs a holdings
  snapshot/history table (schema change, not analytics change).
- eKYC sessions come from `state_snapshot` JSON payloads
  (`kind='ekyc.session'`); extraction is defensive (`TRY_CAST`) but a
  payload schema change in `internal/ekyc` should be mirrored in
  `models/silver/06_kyc_sessions.sql`.
- No streaming/CDC yet — bronze is micro-batch per run. Debezium on the
  existing Kafka is the upgrade path (see docs/ANALYTICS.md).
