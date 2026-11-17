"""Lakehouse orchestrator: bronze extract -> DuckDB silver -> gold ->
Postgres `analytics` schema export (so BI tools query Postgres, never
parquet, and credentials/row counts stay governed).

Usage:
    python3 run.py --dsn postgres://... --lake ./lake
    python3 run.py --lake ./lake --skip-extract     # rebuild models only
    python3 run.py --lake ./lake --skip-export      # models only, no pg
    python3 run.py --lake ./lake --skip-extract --incremental
        # CDC mode: rebuild only models whose upstream bronze tables have
        # new CDC events since the last run (see docs/CDC.md); falls back
        # to a full rebuild when the CDC watermark/state is missing.

Exit code is non-zero if the extract fails entirely, if any model SQL
fails, or if the export fails. Per-table bronze errors are tolerated
(see extract_full.py) and surfaced in the manifest.
"""
from __future__ import annotations

import argparse
import json
import logging
import os
import re
import sys
from datetime import datetime, timezone
from pathlib import Path

log = logging.getLogger("analytics.run")

HERE = Path(__file__).resolve().parent
SILVER_DIR = HERE / "models" / "silver"
GOLD_DIR = HERE / "models" / "gold"

# CDC dependency map (docs/CDC.md). bronze table -> silver models that
# read it; silver model -> gold models that read it; plus gold models
# that read bronze directly. Kept in sync with models/** by the
# dependency-map unit test in cdc_test.py (it re-derives the map from the
# SQL and fails if this drifts). Conservative: an unknown dirty table
# rebuilds EVERYTHING.
TABLE_TO_SILVER: dict[str, tuple[str, ...]] = {
    "person": ("01_members",),
    "member": ("01_members",),
    "rsa_account": ("01_members",),
    "contribution": ("02_contributions",),
    "contribution_schedule": ("02_contributions", "03_employers"),
    "employer": ("03_employers",),
    "ops_case": ("04_cases",),
    "fund": ("05_funds_nav",),
    "fund_price": ("05_funds_nav",),
    "unit_holding": ("05_funds_nav",),
    "state_snapshot": ("06_kyc_sessions",),
}
SILVER_TO_GOLD: dict[str, tuple[str, ...]] = {
    "01_members": ("01_member_growth_daily", "07_retirement_pipeline"),
    "02_contributions": ("02_contribution_monthly_by_employer",
                         "06_employer_remittance_health"),
    "03_employers": ("02_contribution_monthly_by_employer",
                     "06_employer_remittance_health"),
    "04_cases": ("04_case_sla_compliance",),
    "05_funds_nav": ("03_fund_aum_daily",),
    "06_kyc_sessions": ("05_kyc_funnel",),
}
TABLE_TO_GOLD_DIRECT: dict[str, tuple[str, ...]] = {
    "benefit_claim": ("07_retirement_pipeline",),
}

# DuckDB -> Postgres type map for the analytics schema export. Gold models
# are expected to only emit these column types (kobo stays BIGINT).
_PG_TYPES = {
    "BIGINT": "BIGINT", "INTEGER": "INTEGER", "HUGEINT": "NUMERIC",
    "VARCHAR": "TEXT", "BOOLEAN": "BOOLEAN", "DATE": "DATE",
    "DOUBLE": "DOUBLE PRECISION", "FLOAT": "DOUBLE PRECISION",
    "TIMESTAMP": "TIMESTAMPTZ", "TIMESTAMP_S": "TIMESTAMPTZ",
    "TIMESTAMP WITH TIME ZONE": "TIMESTAMPTZ",
}


def _load_models(model_dir: Path,
                 only: set[str] | None = None) -> list[tuple[str, str]]:
    """(name, sql) pairs sorted by filename. `only` filters by stem."""
    return [(p.stem, p.read_text()) for p in sorted(model_dir.glob("*.sql"))
            if only is None or p.stem in only]


def build_models(con, lake: str | Path,
                 silver_only: set[str] | None = None,
                 gold_only: set[str] | None = None) -> list[str]:
    """Run silver then gold SQL (alphabetical = dependency order) against
    the bronze parquet lake. Returns the gold table names built.
    silver_only/gold_only restrict which model files run (incremental
    mode); None means all (full refresh)."""
    lake = str(Path(lake).resolve())
    for layer, mdir, only in (("silver", SILVER_DIR, silver_only),
                              ("gold", GOLD_DIR, gold_only)):
        for name, sql in _load_models(mdir, only):
            sql = sql.replace("{{LAKE}}", lake)
            try:
                con.execute(sql)
            except Exception as exc:
                raise RuntimeError(f"{layer}/{name}.sql failed: {exc}") from exc
            tbl = _table_name(sql)
            n = con.execute(f"SELECT count(*) FROM {tbl}").fetchone()[0]
            log.info("%-7s %-40s rows=%d", layer, tbl, n)
    return [_table_name(sql) for _, sql in _load_models(GOLD_DIR, gold_only)]


def _table_name(sql: str) -> str:
    m = re.search(r"CREATE\s+OR\s+REPLACE\s+TABLE\s+(\w+)", sql, re.IGNORECASE)
    if not m:
        raise ValueError("model SQL must start with CREATE OR REPLACE TABLE <name>")
    return m.group(1)


# ---- incremental (CDC) mode ----

_STATE_FILE = "_incremental_state.json"


def select_models(dirty_tables: set[str]) -> tuple[set[str], set[str]] | None:
    """Map dirty bronze tables -> (silver stems, gold stems) to rebuild.

    Conservative: returns None ("rebuild everything") when any dirty
    table is not in the dependency map — a missed edge is far worse than
    a redundant rebuild. Empty input -> empty sets (nothing to do)."""
    silver: set[str] = set()
    gold: set[str] = set()
    for t in dirty_tables:
        s = TABLE_TO_SILVER.get(t, ())
        g = TABLE_TO_GOLD_DIRECT.get(t, ())
        if not s and not g:
            log.warning("table %r not in CDC dependency map — full rebuild", t)
            return None
        silver.update(s)
        gold.update(g)
    for s in silver:
        gold.update(SILVER_TO_GOLD.get(s, ()))
    return silver, gold


def _watermark_path(lake: str | Path) -> Path:
    return Path(os.environ.get(
        "CDC_WATERMARK_PATH", str(Path(lake) / "bronze" / "cdc_watermark.json")))


def dirty_tables_since_last_run(lake: str | Path) -> set[str] | None:
    """Bronze tables with CDC events newer than the last incremental run.

    None => CDC watermark unavailable (consumer never ran / file lost):
    the caller must fall back to a full rebuild. An empty set => fully
    caught up; the run is a no-op."""
    wm_file = _watermark_path(lake)
    if not wm_file.exists():
        return None
    try:
        wm = json.loads(wm_file.read_text())
    except (ValueError, OSError):
        return None
    tables = wm.get("tables", {})
    state_file = Path(lake) / "bronze" / _STATE_FILE
    seen: dict = {}
    if state_file.exists():
        try:
            seen = json.loads(state_file.read_text()).get("tables", {})
        except (ValueError, OSError):
            seen = {}
    return {t for t, meta in tables.items()
            if meta.get("last_seq", 0) > seen.get(t, -1)}


def mark_incremental_run(lake: str | Path) -> None:
    """Snapshot the consumer watermark's per-table last_seq as processed."""
    wm_file = _watermark_path(lake)
    if not wm_file.exists():
        return
    try:
        tables = json.loads(wm_file.read_text()).get("tables", {})
    except (ValueError, OSError):
        return
    state = {"run_at": datetime.now(timezone.utc).isoformat(),
             "tables": {t: m.get("last_seq", 0) for t, m in tables.items()}}
    state_file = Path(lake) / "bronze" / _STATE_FILE
    state_file.parent.mkdir(parents=True, exist_ok=True)
    state_file.write_text(json.dumps(state, indent=2))


def clear_cdc_partitions(lake: str | Path, tables: set[str]) -> int:
    """Remove CDC compacted partitions (dt=9999-12-31) for `tables`.

    Called after a successful FULL extract: the fresh extract is
    authoritative and newer than any prior CDC state, and the CDC
    consumer re-seeds its compacted partition from the new extract
    partitions on the next event. Tables the extract failed on keep
    their CDC state (stale CDC beats an empty placeholder)."""
    import shutil
    removed = 0
    for t in tables:
        cdir = Path(lake) / "bronze" / t / "dt=9999-12-31"
        if cdir.is_dir():
            shutil.rmtree(cdir)
            removed += 1
            log.info("cleared CDC compacted partition for %s "
                     "(full extract is authoritative)", t)
    return removed


def export_gold(con, dsn: str, gold_tables: list[str]) -> dict:
    """Full-refresh each gold table into the Postgres `analytics` schema.

    Strategy: CREATE SCHEMA IF NOT EXISTS; DROP + CREATE per model with a
    type-mapped DDL; bulk COPY via psycopg2. Read-only consumers (Grafana,
    analytics-svc) always see a complete table — drops and reloads happen
    inside one transaction per table so there is no empty-window.
    """
    import psycopg2
    from psycopg2.extras import execute_values

    counts = {}
    conn = psycopg2.connect(dsn)
    try:
        with conn:
            with conn.cursor() as cur:
                cur.execute("CREATE SCHEMA IF NOT EXISTS analytics")
        for tbl in gold_tables:
            arrow = con.execute(f"SELECT * FROM {tbl}").fetch_arrow_table()
            cols = []
            for f in arrow.schema:
                base = str(f.type).upper()
                pg = _PG_TYPES.get(base)
                if pg is None:
                    if base.startswith("DECIMAL"):
                        pg = "NUMERIC"
                    elif base.startswith("TIMESTAMP"):
                        pg = "TIMESTAMPTZ"
                    else:
                        pg = "TEXT"
                cols.append((f.name, pg))
            ddl = ", ".join(f'"{n}" {t}' for n, t in cols)
            rows = [tuple(r) for r in arrow.to_pylist()]
            with conn:  # one txn per table: no empty-window for readers
                with conn.cursor() as cur:
                    cur.execute(f'DROP TABLE IF EXISTS analytics."{tbl}"')
                    cur.execute(f'CREATE TABLE analytics."{tbl}" ({ddl})')
                    if rows:
                        ph = "(" + ",".join(["%s"] * len(cols)) + ")"
                        execute_values(
                            cur, f'INSERT INTO analytics."{tbl}" VALUES {ph}',
                            rows, page_size=10_000)
            counts[tbl] = len(rows)
            log.info("export analytics.%-38s rows=%d", tbl, len(rows))
    finally:
        conn.close()
    return counts


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(description="PFAEngine lakehouse runner")
    p.add_argument("--dsn", default=os.environ.get("DATABASE_URL", ""),
                   help="Postgres DSN (extract source AND analytics export target)")
    p.add_argument("--lake", default=os.environ.get("LAKE_DIR", "./lake"))
    p.add_argument("--migrations", default="db/migrations")
    p.add_argument("--ds", default=None, help="partition date YYYY-MM-DD")
    p.add_argument("--skip-extract", action="store_true")
    p.add_argument("--skip-export", action="store_true")
    p.add_argument("--incremental", action="store_true",
                   help="CDC mode: rebuild only models whose upstream tables "
                        "have new events in the CDC watermark since the last "
                        "run; export only the affected gold tables. Falls "
                        "back to a full rebuild when CDC state is missing.")
    a = p.parse_args(argv)

    import duckdb

    if not a.skip_extract:
        sys.path.insert(0, str(HERE))
        from extract_full import extract_all
        manifest = extract_all(a.dsn, a.lake, a.migrations, a.ds)
        if manifest["errors"]:
            log.warning("extract finished with %d table errors: %s",
                        len(manifest["errors"]), list(manifest["errors"]))
        # Fresh extract supersedes CDC-compacted state (docs/CDC.md).
        clear_cdc_partitions(a.lake, set(manifest["tables"]))

    silver_only = gold_only = None
    if a.incremental:
        dirty = dirty_tables_since_last_run(a.lake)
        if dirty is None:
            log.warning("incremental: no CDC watermark at %s — "
                        "falling back to FULL rebuild", _watermark_path(a.lake))
        elif not dirty:
            log.info("incremental: no new CDC events — nothing to do")
            return 0
        else:
            selected = select_models(dirty)
            if selected is None:
                log.warning("incremental: unmapped dirty table — FULL rebuild")
            else:
                silver_only, gold_only = selected
                log.info("incremental: dirty=%s -> silver=%s gold=%s",
                         sorted(dirty), sorted(silver_only), sorted(gold_only))

    con = duckdb.connect()  # in-memory compute over the parquet lake
    try:
        gold = build_models(con, a.lake,
                            silver_only=silver_only, gold_only=gold_only)
    finally:
        pass

    if not a.skip_export:
        if not a.dsn:
            log.error("--dsn (or DATABASE_URL) required for export")
            return 2
        # Transactional swap per table (export_gold) — incremental mode
        # exports only the affected gold tables, leaving the rest of the
        # analytics schema untouched.
        export_gold(con, a.dsn, gold)
    con.close()
    if a.incremental:
        mark_incremental_run(a.lake)
    log.info("lakehouse run complete: %d gold models", len(gold))
    return 0


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO, format="%(levelname)s %(message)s")
    sys.exit(main())
