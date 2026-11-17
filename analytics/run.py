"""Lakehouse orchestrator: bronze extract -> DuckDB silver -> gold ->
Postgres `analytics` schema export (so BI tools query Postgres, never
parquet, and credentials/row counts stay governed).

Usage:
    python3 run.py --dsn postgres://... --lake ./lake
    python3 run.py --lake ./lake --skip-extract     # rebuild models only
    python3 run.py --lake ./lake --skip-export      # models only, no pg

Exit code is non-zero if the extract fails entirely, if any model SQL
fails, or if the export fails. Per-table bronze errors are tolerated
(see extract_full.py) and surfaced in the manifest.
"""
from __future__ import annotations

import argparse
import logging
import os
import re
import sys
from pathlib import Path

log = logging.getLogger("analytics.run")

HERE = Path(__file__).resolve().parent
SILVER_DIR = HERE / "models" / "silver"
GOLD_DIR = HERE / "models" / "gold"

# DuckDB -> Postgres type map for the analytics schema export. Gold models
# are expected to only emit these column types (kobo stays BIGINT).
_PG_TYPES = {
    "BIGINT": "BIGINT", "INTEGER": "INTEGER", "HUGEINT": "NUMERIC",
    "VARCHAR": "TEXT", "BOOLEAN": "BOOLEAN", "DATE": "DATE",
    "DOUBLE": "DOUBLE PRECISION", "FLOAT": "DOUBLE PRECISION",
    "TIMESTAMP": "TIMESTAMPTZ", "TIMESTAMP_S": "TIMESTAMPTZ",
    "TIMESTAMP WITH TIME ZONE": "TIMESTAMPTZ",
}


def _load_models(model_dir: Path) -> list[tuple[str, str]]:
    return [(p.stem, p.read_text()) for p in sorted(model_dir.glob("*.sql"))]


def build_models(con, lake: str | Path) -> list[str]:
    """Run silver then gold SQL (alphabetical = dependency order) against
    the bronze parquet lake. Returns the gold table names built."""
    lake = str(Path(lake).resolve())
    for layer, mdir in (("silver", SILVER_DIR), ("gold", GOLD_DIR)):
        for name, sql in _load_models(mdir):
            sql = sql.replace("{{LAKE}}", lake)
            try:
                con.execute(sql)
            except Exception as exc:
                raise RuntimeError(f"{layer}/{name}.sql failed: {exc}") from exc
            tbl = _table_name(sql)
            n = con.execute(f"SELECT count(*) FROM {tbl}").fetchone()[0]
            log.info("%-7s %-40s rows=%d", layer, tbl, n)
    return [_table_name(sql) for _, sql in _load_models(GOLD_DIR)]


def _table_name(sql: str) -> str:
    m = re.search(r"CREATE\s+OR\s+REPLACE\s+TABLE\s+(\w+)", sql, re.IGNORECASE)
    if not m:
        raise ValueError("model SQL must start with CREATE OR REPLACE TABLE <name>")
    return m.group(1)


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
    a = p.parse_args(argv)

    import duckdb

    if not a.skip_extract:
        sys.path.insert(0, str(HERE))
        from extract_full import extract_all
        manifest = extract_all(a.dsn, a.lake, a.migrations, a.ds)
        if manifest["errors"]:
            log.warning("extract finished with %d table errors: %s",
                        len(manifest["errors"]), list(manifest["errors"]))

    con = duckdb.connect()  # in-memory compute over the parquet lake
    try:
        gold = build_models(con, a.lake)
    finally:
        pass

    if not a.skip_export:
        if not a.dsn:
            log.error("--dsn (or DATABASE_URL) required for export")
            return 2
        export_gold(con, a.dsn, gold)
    con.close()
    log.info("lakehouse run complete: %d gold models", len(gold))
    return 0


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO, format="%(levelname)s %(message)s")
    sys.exit(main())
