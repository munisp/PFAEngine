"""Full-platform Postgres -> bronze parquet extraction (lakehouse zone `bronze/`).

Extends the ml/pfa_ml/extract.py idea (Postgres -> parquet for the ML
training zone) to EVERY operational table in the platform. Tables are
enumerated by parsing db/migrations/*.sql — a new migration automatically
lands in the lake on the next run, no code change required.

Layout (Hive-partitioned, DuckDB/Spark/Trino-readable):

  lake/bronze/<table>/dt=YYYY-MM-DD/part-<run>.parquet   (incremental tables)
  lake/bronze/<table>/dt=YYYY-MM-DD/part-full.parquet    (full-refresh tables)
  lake/bronze/<table>/_watermark.json                    (incremental high-water mark)
  lake/bronze/_manifest.json                             (per-run counts + errors)

Extraction modes:
  * incremental — tables with an `updated_at` column, plus known
    append-only/WORM ledgers (contribution, audit_event, ...): only rows
    newer than the stored watermark are pulled. Silver models dedupe by
    primary key keeping the latest watermark, so re-extraction of an
    updated row is safe.
  * full — everything else (small mutable dimensions): the day's `dt=`
    partition is atomically replaced, so re-running the same day is
    idempotent.

Failure policy: bronze must NEVER block on one bad table. A failing table
is logged into the manifest and (if it has no prior partitions) an empty
placeholder parquet is written so downstream DuckDB builds stay green.

Money stays integer kobo end-to-end (DA-03); no floats are introduced.
"""
from __future__ import annotations

import json
import logging
import os
import re
import shutil
import time
from datetime import date
from pathlib import Path

import pyarrow as pa
import pyarrow.parquet as pq

log = logging.getLogger("analytics.extract")

_CREATE_RE = re.compile(
    r"CREATE\s+TABLE\s+IF\s+NOT\s+EXISTS\s+([a-z_][a-z0-9_]*)\s*\((.*?)\);",
    re.IGNORECASE | re.DOTALL,
)
_IDENT_RE = re.compile(r"^[a-z_][a-z0-9_]*$")

# Timestamp columns that make a table safely incremental (a changed row
# always moves the watermark forward). `updated_at` is the strong signal;
# the rest are event-time columns on append-only/WORM tables.
WATERMARK_CANDIDATES = (
    "updated_at", "at", "posted_at", "created_at", "opened_at",
    "submitted_at", "uploaded_at", "initiated_at", "generated_at",
    "registered_at", "accredited_at", "decided_at",
)
# Mutable tables whose only timestamp is a *created-style* column must be
# full-refreshed: their updates never move that column. Everything with
# `updated_at` or an event-time column is extracted incrementally.
FULL_REFRESH_TABLES = {
    "person", "employer", "fund", "fund_price", "unit_holding",
    "rsa_account", "member", "nominee", "consent", "agent",
    "api_key", "webhook_sub", "escalation_rule",
}

# psycopg2 type-OID -> arrow type. Keeps kobo bigint, dates, bools and
# timestamps typed in bronze instead of degenerating to strings.
_PG_ARROW = {
    16: pa.bool_(),                       # bool
    20: pa.int64(),                       # int8
    21: pa.int32(),                       # int2
    23: pa.int32(),                       # int4
    25: pa.string(),                      # text
    700: pa.float64(),                    # float4
    701: pa.float64(),                    # float8
    1043: pa.string(),                    # varchar
    1082: pa.date32(),                    # date
    1114: pa.timestamp("us"),             # timestamp
    1184: pa.timestamp("us", tz="UTC"),   # timestamptz
    1700: pa.float64(),                   # numeric (scores only; never money)
    114: pa.string(),                     # json
    3802: pa.string(),                    # jsonb (serialised below)
}
_JSON_OIDS = {114, 3802}


def enumerate_tables(migrations_dir: str | Path) -> dict[str, dict]:
    """Parse db/migrations/*.sql -> {table: {columns: [...]}}.

    Good enough for the platform's DDL style (one column per line, no
    parens inside column defs except NUMERIC(5,4) which the regex handles
    because we split top-level commas crudely but only need identifiers).
    """
    tables: dict[str, dict] = {}
    for path in sorted(Path(migrations_dir).glob("*.sql")):
        sql = path.read_text()
        for m in _CREATE_RE.finditer(sql):
            name, body = m.group(1), m.group(2)
            cols = []
            depth = 0
            for raw_line in body.splitlines():
                line = raw_line.split("--", 1)[0].strip()
                if not line:
                    continue
                tok = line.split()[0].rstrip(",").lower()
                if tok in {"primary", "foreign", "unique", "check", "constraint"}:
                    continue
                if _IDENT_RE.match(tok):
                    cols.append(tok)
                depth += line.count("(") - line.count(")")
            tables[name] = {"columns": cols, "migration": path.name}
    return tables


def _watermark_col(columns: list[str]) -> str | None:
    for c in WATERMARK_CANDIDATES:
        if c in columns:
            return c
    return None


def _table_from_cursor(cur, columns: list[str]) -> pa.Table:
    """Build a typed arrow table from the last executed query on `cur`."""
    rows = cur.fetchall()
    desc = cur.description or []
    arrays = []
    fields = []
    for i, d in enumerate(desc):
        oid = d.type_code
        typ = _PG_ARROW.get(oid, pa.string())
        vals = [r[i] for r in rows]
        if oid in _JSON_OIDS:
            vals = [None if v is None else (
                v if isinstance(v, str) else json.dumps(v, default=str))
                for v in vals]
        elif typ == pa.string():
            vals = [None if v is None else (v if isinstance(v, str) else str(v))
                    for v in vals]
        try:
            arr = pa.array(vals, type=typ)
        except (pa.ArrowInvalid, pa.ArrowTypeError, ValueError, TypeError):
            arr = pa.array(
                [None if v is None else str(v) for v in vals], type=pa.string())
            typ = pa.string()
        arrays.append(arr)
        fields.append(pa.field(d.name, typ))
    return pa.Table.from_arrays(arrays, schema=pa.schema(fields)) if fields \
        else pa.table({"__empty": pa.array([], type=pa.bool_())})


def _read_watermark(tdir: Path) -> str | None:
    f = tdir / "_watermark.json"
    if f.exists():
        try:
            return json.loads(f.read_text()).get("watermark")
        except (ValueError, OSError):
            return None
    return None


def _write_watermark(tdir: Path, column: str, value: str) -> None:
    (tdir / "_watermark.json").write_text(json.dumps(
        {"watermark_column": column, "watermark": value}))


def extract_table(conn, table: str, columns: list[str], lake: str | Path,
                  ds: str | None = None) -> dict:
    """Extract one table into lake/bronze/<table>/dt=<ds>/. Returns a
    per-table result dict (never raises for data-level failures)."""
    if not _IDENT_RE.match(table):
        raise ValueError(f"unsafe table name: {table!r}")
    ds = ds or date.today().isoformat()
    tdir = Path(lake) / "bronze" / table
    pdir = tdir / f"dt={ds}"
    wm_col = _watermark_col(columns)
    incremental = wm_col is not None and table not in FULL_REFRESH_TABLES

    sql = f"SELECT * FROM {table}"
    params: tuple = ()
    prev_wm = None
    if incremental:
        prev_wm = _read_watermark(tdir)
        if prev_wm:
            sql += f" WHERE {wm_col} > %s"
            params = (prev_wm,)

    with conn.cursor() as cur:
        cur.execute(sql, params)
        tbl = _table_from_cursor(cur, columns)

    tdir.mkdir(parents=True, exist_ok=True)
    if incremental:
        pdir.mkdir(parents=True, exist_ok=True)
        part = pdir / f"part-{int(time.time() * 1000)}.parquet"
        pq.write_table(tbl, part)
        if tbl.num_rows:
            import pyarrow.compute as pc
            col = tbl.column(wm_col) if wm_col in tbl.column_names else None
            if col is not None and col.null_count < len(col):
                new_wm = str(pc.max(col).as_py())
                _write_watermark(tdir, wm_col, new_wm)
        mode = "incremental"
    else:
        # Full refresh: replace the day's partition atomically-ish so a
        # same-day re-run is idempotent.
        if pdir.exists():
            shutil.rmtree(pdir)
        pdir.mkdir(parents=True, exist_ok=True)
        pq.write_table(tbl, pdir / "part-full.parquet")
        mode = "full"
    return {"table": table, "mode": mode, "rows": tbl.num_rows, "ds": ds}


def _write_placeholder(lake: str | Path, table: str, ds: str) -> None:
    """Empty marker parquet so DuckDB globs for a failed table still match
    at least one file (union_by_name fills the missing columns as NULL)."""
    pdir = Path(lake) / "bronze" / table / f"dt={ds}"
    if any(Path(lake).glob(f"bronze/{table}/dt=*/*.parquet")):
        return  # earlier partitions exist — glob still works
    pdir.mkdir(parents=True, exist_ok=True)
    pq.write_table(pa.table({"__empty": pa.array([], type=pa.bool_())}),
                   pdir / "part-empty.parquet")


def extract_all(dsn: str | None, lake: str | Path,
                migrations_dir: str | Path = "db/migrations",
                ds: str | None = None) -> dict:
    """Extract every operational table. Per-table failures are logged and
    collected in the manifest — bronze never blocks on one bad table."""
    import psycopg2

    dsn = dsn or os.environ.get("DATABASE_URL", "")
    if not dsn:
        raise RuntimeError("extract_all: --dsn or DATABASE_URL required")
    ds = ds or date.today().isoformat()
    tables = enumerate_tables(migrations_dir)
    manifest: dict = {"ds": ds, "source": "postgres", "tables": {}, "errors": {}}

    conn = psycopg2.connect(dsn)
    try:
        for name, meta in sorted(tables.items()):
            try:
                res = extract_table(conn, name, meta["columns"], lake, ds)
                manifest["tables"][name] = res
                log.info("bronze %-28s %-11s rows=%d",
                         name, res["mode"], res["rows"])
            except Exception as exc:  # noqa: BLE001 — per-table isolation
                conn.rollback()
                log.warning("bronze %-28s FAILED: %s", name, exc)
                manifest["errors"][name] = str(exc)
                _write_placeholder(lake, name, ds)
    finally:
        conn.close()

    bdir = Path(lake) / "bronze"
    bdir.mkdir(parents=True, exist_ok=True)
    manifest["ok"] = not manifest["errors"]
    (bdir / "_manifest.json").write_text(json.dumps(manifest, indent=2, default=str))
    return manifest


if __name__ == "__main__":  # pragma: no cover
    import argparse

    logging.basicConfig(level=logging.INFO, format="%(levelname)s %(message)s")
    p = argparse.ArgumentParser(description="Postgres -> bronze parquet extract")
    p.add_argument("--dsn", default=os.environ.get("DATABASE_URL", ""))
    p.add_argument("--lake", default="./lake")
    p.add_argument("--migrations", default="db/migrations")
    p.add_argument("--ds", default=None, help="partition date YYYY-MM-DD")
    a = p.parse_args()
    m = extract_all(a.dsn, a.lake, a.migrations, a.ds)
    print(json.dumps({"ok": m["ok"], "tables": len(m["tables"]),
                      "errors": m["errors"]}, indent=2))
