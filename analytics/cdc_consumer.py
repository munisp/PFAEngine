"""CDC consumer: Debezium (Kafka) -> bronze parquet upsert/delete applier.

Consumes *unwrapped* Debezium events (ExtractNewRecordState SMT — flat row
JSON with `__op` / `__deleted` / `__lsn` / `__source_ts_ms` metadata) from
`pfaengine.public.<table>` topics and applies them into the bronze zone.

Apply strategy — per-table compacted current-state partition
(documented in docs/CDC.md):

    lake/bronze/<table>/dt=9999-12-31/part-cdc.parquet

The compacted partition holds the COMPLETE current state of the table:
on first flush it is seeded by merging all of the table's existing
bronze partitions (dedupe by PK, latest `dt` wins), which are then
removed — the compacted partition is a strict superset of them. This is
what makes deletes correct: deletes are physical removals from the only
partition silver reads for that table, so no tombstone markers are
needed and the silver/gold SQL is unchanged. (Keeping the old partitions
would resurrect deleted rows: silver dedupes across ALL partitions by
`dt DESC`, and a partition that lacks a key cannot mask a deleted one.)

Whole-table rewrite per flush is O(table); upsert/delete by primary key
is idempotent, so replay after a crash converges. At this platform's
table sizes that is cheap (see docs/CDC.md for the cost discussion).

Full-refresh interplay: `run.py` (default full mode) removes the
`dt=9999-12-31` partitions after a successful extract, because a fresh
extract is authoritative and newer than any prior CDC state; this
consumer simply re-seeds from the new partitions on the next event.

Watermark: `lake/bronze/cdc_watermark.json` (path overridable via
CDC_WATERMARK_PATH) holding per topic-partition offsets, last LSN,
per-table event counters and a global seq. `analytics/run.py
--incremental` diffs this against its own state file to decide which
models to rebuild; `analytics-svc` serves it at /v1/analytics/cdc-status.

Usage:
    python3 analytics/cdc_consumer.py --brokers localhost:9092 --lake ./lake

Degrades gracefully: if Kafka is unreachable the consumer exits non-zero
with a clear log line — analytics keeps working via the scheduled full
refresh (`run.py` without --incremental).
"""
from __future__ import annotations

import argparse
import json
import logging
import os
import re
import signal
import time
from datetime import date, datetime, timedelta, timezone
from pathlib import Path

import pyarrow as pa
import pyarrow.parquet as pq

log = logging.getLogger("analytics.cdc")

HERE = Path(__file__).resolve().parent
DEFAULT_MIGRATIONS = HERE.parent / "db" / "migrations"

# The compacted current-state partition. Sorts after any real dt= date.
CDC_PARTITION = "dt=9999-12-31"

# Topic prefix configured in deploy/debezium/postgres-source.json.
TOPIC_PREFIX = "pfaengine."

_CREATE_RE = re.compile(
    r"CREATE\s+TABLE\s+IF\s+NOT\s+EXISTS\s+([a-z_][a-z0-9_]*)\s*\((.*?)\);",
    re.IGNORECASE | re.DOTALL,
)
_PK_INLINE_RE = re.compile(
    r"^\s*([a-z_][a-z0-9_]*)\s+[A-Z]+(?:\([^)]*\))?[^,]*\bPRIMARY\s+KEY\b",
    re.IGNORECASE,
)
_PK_TABLE_RE = re.compile(r"PRIMARY\s+KEY\s*\(([^)]+)\)", re.IGNORECASE)

# Debezium time encoding (schemas disabled): TIMESTAMP -> int64 µs since
# epoch; DATE -> int32 days since epoch; TIMESTAMPTZ -> ISO-8601 string.
_TS_SUFFIXES = ("_at", "_ts", "updated", "created")  # heuristic fallback
_US_RANGE = (10 ** 14, 10 ** 17)   # plausible µs-since-epoch (1973..5138)
_DAY_RANGE = (0, 100_000)          # plausible days-since-epoch


# ---------------------------------------------------------------- schema


def parse_primary_keys(migrations_dir: str | Path) -> dict[str, tuple[str, ...]]:
    """Parse db/migrations/*.sql -> {table: (pk_cols,)}.

    Handles both inline (`id TEXT PRIMARY KEY`) and table-level
    (`PRIMARY KEY (tenant_id, id)`) declarations. Defaults to ("id",)
    when a table has no explicit PK — matches the platform's DDL style.
    """
    pks: dict[str, tuple[str, ...]] = {}
    for path in sorted(Path(migrations_dir).glob("*.sql")):
        sql = path.read_text()
        for m in _CREATE_RE.finditer(sql):
            name, body = m.group(1), m.group(2)
            table_pk = _PK_TABLE_RE.search(body)
            if table_pk:
                cols = tuple(c.strip().strip('"').lower()
                             for c in table_pk.group(1).split(","))
                pks[name] = cols
                continue
            for line in body.splitlines():
                im = _PK_INLINE_RE.match(line.split("--", 1)[0])
                if im:
                    pks[name] = (im.group(1).lower(),)
                    break
    return pks


_IDENT_RE = re.compile(r"^[a-z_][a-z0-9_]*$")


def topic_to_table(topic: str) -> str | None:
    """pfaengine.public.member -> member. None for non-table topics or
    anything that isn't a safe identifier (path-traversal guard — the
    result is used to build lake paths)."""
    if not topic.startswith(TOPIC_PREFIX):
        return None
    parts = topic[len(TOPIC_PREFIX):].split(".")
    if len(parts) != 2:
        return None
    table = parts[-1].lower()
    return table if _IDENT_RE.match(table) else None


def _row_key(record: dict, pk_cols: tuple[str, ...]) -> str:
    return "\x1f".join("" if record.get(c) is None else str(record.get(c))
                       for c in pk_cols)


# ---------------------------------------------------------------- events


def classify_event(value, key) -> tuple[str, dict | None]:
    """Classify one unwrapped Debezium record.

    Returns ("upsert", row) | ("delete", row-with-pk) | ("skip", None).
    `value` is the decoded JSON value (None for a raw Kafka tombstone),
    `key` the decoded JSON key ({"id": ...} — always carries the PK).
    """
    if value is None:  # tombstone (defensive: connector disables them)
        return ("delete", dict(key)) if isinstance(key, dict) else ("skip", None)
    if not isinstance(value, dict):
        return ("skip", None)
    deleted = value.get("__deleted")
    op = value.get("__op")
    row = {k: v for k, v in value.items() if not k.startswith("__")}
    if isinstance(key, dict):  # guarantee PK presence for deletes
        for k, v in key.items():
            row.setdefault(k, v)
    if op == "d" or deleted in (True, "true", "True"):
        return ("delete", row)
    if op in (None, "c", "u", "r"):
        return ("upsert", row)
    return ("skip", None)


# ---------------------------------------------------------------- typing


def _coerce_value(typ: pa.DataType, v):
    """Best-effort coercion of a Debezium JSON value to an existing
    bronze column type. Returns the value unchanged when no rule fires —
    pyarrow will then infer/fallback."""
    if v is None:
        return None
    try:
        if pa.types.is_timestamp(typ):
            if isinstance(v, (int, float)):      # µs since epoch
                return datetime.fromtimestamp(v / 1e6, tz=timezone.utc)
            if isinstance(v, str):
                return datetime.fromisoformat(v.replace("Z", "+00:00"))
        elif pa.types.is_date(typ):
            if isinstance(v, int):               # days since epoch
                return date(1970, 1, 1) + timedelta(days=v)
            if isinstance(v, str):
                return date.fromisoformat(v[:10])
        elif pa.types.is_time(typ) and isinstance(v, (int, float)):
            return (datetime(1970, 1, 1, tzinfo=timezone.utc)
                    + timedelta(microseconds=v)).time()
        elif pa.types.is_integer(typ) and isinstance(v, str) and v.lstrip("-").isdigit():
            return int(v)
        elif pa.types.is_floating(typ) and isinstance(v, str):
            return float(v)                      # decimal.handling.mode=string
        elif pa.types.is_string(typ) and isinstance(v, (dict, list)):
            return json.dumps(v)                 # jsonb arrives embedded
    except (ValueError, OverflowError, OSError):
        return str(v)
    return v


def _normalize_fresh_types(tbl: pa.Table) -> pa.Table:
    """Heuristic typing for a table with no existing bronze schema:
    Debezium TIMESTAMP arrives as int64 µs, DATE as int32 days. Only
    fires on columns whose NAME looks temporal and whose values are in a
    plausible range — kobo/units columns are never touched."""
    for i, f in enumerate(tbl.schema):
        col = tbl.column(i)
        if pa.types.is_int64(f.type) and f.name.endswith(_TS_SUFFIXES):
            nn = col.drop_null()
            if len(nn) and all(_US_RANGE[0] <= abs(x.as_py()) < _US_RANGE[1]
                               for x in nn):
                tbl = tbl.set_column(
                    i, pa.field(f.name, pa.timestamp("us", tz="UTC")),
                    col.cast(pa.timestamp("us", tz="UTC"), safe=False))
        elif pa.types.is_int32(f.type) and (
                f.name == "dob" or f.name.endswith(("_date", "_dt"))):
            nn = col.drop_null()
            if len(nn) and all(_DAY_RANGE[0] <= x.as_py() < _DAY_RANGE[1]
                               for x in nn):
                tbl = tbl.set_column(i, pa.field(f.name, pa.date32()),
                                     col.cast(pa.date32(), safe=False))
    return tbl


# ---------------------------------------------------------------- apply


def apply_events(existing: pa.Table | None,
                 events: list[tuple[str, dict]],
                 pk_cols: tuple[str, ...]) -> pa.Table:
    """Apply ordered (kind, row) events to `existing` (or an empty lake).

    Last event per primary key wins within the batch; deletes physically
    remove the row. Result is schema-aligned to `existing` when present.
    """
    final: dict[str, tuple[str, dict]] = {}
    order: list[str] = []
    for kind, row in events:
        k = _row_key(row, pk_cols)
        if k not in final:
            order.append(k)
        final[k] = (kind, row)

    survivors: list[dict] = []
    schema = None
    if existing is not None:
        schema = existing.schema
        survivors = [r for r in existing.to_pylist()
                     if _row_key(r, pk_cols) not in final]

    upserts = []
    for k in order:
        kind, row = final[k]
        if kind != "upsert":
            continue
        if schema is not None:
            # Coerce known columns to the existing bronze types; keep any
            # extra columns (schema evolution adds them via unify_schemas).
            coerced = {f.name: _coerce_value(f.type, row.get(f.name))
                       for f in schema if f.name in row}
            coerced.update({k2: v for k2, v in row.items()
                            if k2 not in schema.names})
            upserts.append(coerced)
        else:
            upserts.append(row)

    if not upserts:
        if existing is None:
            return pa.table({c: pa.array([], type=pa.string())
                             for c in pk_cols})
        return _rows_to_table(survivors, schema)
    new_tbl = _rows_to_table(upserts, schema)
    if not survivors and existing is None:
        return _normalize_fresh_types(new_tbl)
    base = _rows_to_table(survivors, schema) if survivors else (
        existing.slice(0, 0) if existing is not None else None)
    if base is None:
        return _normalize_fresh_types(new_tbl)
    unified = pa.unify_schemas([base.schema, new_tbl.schema],
                               promote_options="permissive")
    return pa.concat_tables(
        [_safe_cast(base, unified), _safe_cast(new_tbl, unified)],
        promote_options="permissive")


def _rows_to_table(rows: list[dict], schema: pa.Schema | None) -> pa.Table:
    if not rows:
        if schema is not None:
            return pa.table({f.name: pa.array([], type=f.type)
                             for f in schema})
        return pa.table({})
    if schema is not None:
        cols = {}
        for f in schema:
            vals = [r.get(f.name) for r in rows]
            try:
                cols[f.name] = pa.array(vals, type=f.type)
            except (pa.ArrowInvalid, pa.ArrowTypeError, TypeError, ValueError):
                cols[f.name] = pa.array(
                    [None if v is None else str(v) for v in vals],
                    type=pa.string())
        extras = sorted({k for r in rows for k in r} - set(cols))
        for k in extras:
            cols[k] = pa.array([r.get(k) for r in rows])
        return pa.table(cols)
    return pa.Table.from_pylist(rows)


def _safe_cast(tbl: pa.Table, schema: pa.Schema) -> pa.Table:
    out = {}
    for f in schema:
        if f.name in tbl.column_names:
            col = tbl.column(f.name)
            if col.type != f.type:
                try:
                    col = col.cast(f.type, safe=False)
                except (pa.ArrowInvalid, pa.ArrowTypeError,
                        pa.ArrowNotImplementedError, ValueError):
                    col = pa.array([None] * len(tbl), type=f.type)
            out[f.name] = col
        else:
            out[f.name] = pa.array([None] * len(tbl), type=f.type)
    return pa.table(out, schema=schema)


# ---------------------------------------------------------------- store


class BronzeStore:
    """Reads/writes the per-table compacted CDC partition."""

    def __init__(self, lake: str | Path):
        self.lake = Path(lake)

    def _cdc_part(self, table: str) -> Path:
        return self.lake / "bronze" / table / CDC_PARTITION / "part-cdc.parquet"

    def load_current(self, table: str, pk_cols: tuple[str, ...],
                     ) -> tuple[pa.Table | None, bool]:
        """Current state for `table`: (table, seeded).

        Reads the compacted partition when present; otherwise merges all
        legacy extract partitions (seed) and reports seeded=True so the
        caller can supersede them after a successful write."""
        f = self._cdc_part(table)
        if f.exists():
            return pq.read_table(f), False
        return self._seed_from_partitions(table, pk_cols), True

    def _seed_from_partitions(self, table: str,
                              pk_cols: tuple[str, ...]) -> pa.Table | None:
        """Merge all legacy extract partitions into the initial current
        state, deduping by PK with the latest dt= partition winning
        (mirrors the silver dedupe contract). Placeholder parquets from
        failed extracts (no PK columns) are skipped. None if no data."""
        tdir = self.lake / "bronze" / table
        files = sorted(p for p in tdir.glob("dt=*/*.parquet")
                       if p.parent.name != CDC_PARTITION)
        if not files:
            return None
        latest: dict[str, tuple[str, dict]] = {}
        schema: pa.Schema | None = None
        for f in files:
            t = pq.read_table(f)
            if not any(c in t.column_names for c in pk_cols):
                continue  # extract placeholder (failed table) — no data
            t = t.drop_columns([c for c in ("__empty",)
                                if c in t.column_names])
            schema = t.schema if schema is None else pa.unify_schemas(
                [schema, t.schema], promote_options="permissive")
            dt = f.parent.name[3:]
            for r in t.to_pylist():
                k = _row_key(r, pk_cols)
                if k not in latest or dt >= latest[k][0]:
                    latest[k] = (dt, r)
        if schema is None:
            return None
        return _rows_to_table([v[1] for v in latest.values()], schema)

    def write_current(self, table: str, tbl: pa.Table) -> Path:
        """Atomic-ish replace: write tmp then rename over the old part."""
        f = self._cdc_part(table)
        f.parent.mkdir(parents=True, exist_ok=True)
        tmp = f.with_suffix(".parquet.tmp")
        pq.write_table(tbl, tmp)
        os.replace(tmp, f)
        return f

    def remove_legacy_partitions(self, table: str) -> int:
        """Remove all non-CDC partitions of `table` (they are a strict
        subset of the just-written compacted partition). Returns the
        number of partition dirs removed."""
        import shutil
        tdir = self.lake / "bronze" / table
        removed = 0
        for pdir in tdir.glob("dt=*"):
            if pdir.name == CDC_PARTITION or not pdir.is_dir():
                continue
            shutil.rmtree(pdir)
            removed += 1
        if removed:
            log.info("cdc %-28s superseded %d legacy partition(s)",
                     table, removed)
        return removed


# ---------------------------------------------------------------- watermark


class Watermark:
    """cdc_watermark.json — per-partition offsets, LSN, per-table stats.

    `seq` is a global monotonically-increasing applied-event counter;
    run.py --incremental compares per-table last_seq against its own
    state file to find dirty tables."""

    def __init__(self, path: str | Path):
        self.path = Path(path)
        self.data = {"version": 1, "seq": 0, "offsets": {}, "lsn": None,
                     "tables": {}, "updated_at": None}
        self.load()

    def load(self) -> None:
        if self.path.exists():
            try:
                self.data.update(json.loads(self.path.read_text()))
            except (ValueError, OSError) as exc:
                log.warning("watermark unreadable (%s) — starting fresh", exc)

    def save(self) -> None:
        self.data["updated_at"] = datetime.now(timezone.utc).isoformat()
        self.path.parent.mkdir(parents=True, exist_ok=True)
        tmp = self.path.with_suffix(".json.tmp")
        tmp.write_text(json.dumps(self.data, indent=2, default=str))
        os.replace(tmp, self.path)

    def record(self, topic: str, partition: int, offset: int,
               table: str, op: str, lsn=None) -> None:
        now = datetime.now(timezone.utc).isoformat()
        self.data["seq"] += 1
        self.data["offsets"].setdefault(topic, {})[str(partition)] = offset + 1
        if lsn is not None:
            self.data["lsn"] = lsn
        t = self.data["tables"].setdefault(
            table, {"events": 0, "last_seq": 0, "last_op": None,
                    "last_applied_at": None})
        t["events"] += 1
        t["last_seq"] = self.data["seq"]
        t["last_op"] = op
        t["last_applied_at"] = now

    def metrics(self) -> dict:
        return {"seq": self.data["seq"], "lsn": self.data["lsn"],
                "tables": self.data["tables"],
                "offsets": self.data["offsets"],
                "updated_at": self.data["updated_at"]}


# ---------------------------------------------------------------- consumer


class CDCApplier:
    """Buffers CDC events per table and flushes them to bronze parquet.

    Flush is triggered every `batch_size` events or `flush_seconds`
    (whichever first) by the consumer loop, or on shutdown."""

    def __init__(self, lake: str | Path, pk_map: dict[str, tuple[str, ...]],
                 watermark: Watermark | None = None,
                 batch_size: int = 500, flush_seconds: float = 10.0):
        self.store = BronzeStore(lake)
        self.pk_map = pk_map
        self.watermark = watermark
        self.batch_size = batch_size
        self.flush_seconds = flush_seconds
        self._pending: dict[str, list[tuple[str, dict]]] = {}
        self._pending_meta: list[tuple[str, int, int, str, str, object]] = []
        self._last_flush = time.monotonic()

    def handle(self, topic: str, partition: int, offset: int,
               value, key, lsn=None) -> str:
        """Buffer one Kafka record. Returns 'upsert'|'delete'|'skip'."""
        table = topic_to_table(topic)
        if table is None:
            return "skip"
        kind, row = classify_event(value, key)
        if kind == "skip" or row is None:
            return "skip"
        self._pending.setdefault(table, []).append((kind, row))
        self._pending_meta.append((topic, partition, offset, table, kind, lsn))
        return kind

    def due(self) -> bool:
        n = len(self._pending_meta)
        return n >= self.batch_size or (
            n > 0 and time.monotonic() - self._last_flush >= self.flush_seconds)

    def flush(self) -> dict[str, int]:
        """Apply all buffered events to bronze. Returns {table: events}."""
        if not self._pending:
            return {}
        applied = {}
        for table, events in sorted(self._pending.items()):
            pk = self.pk_map.get(table, ("id",))
            current, seeded = self.store.load_current(table, pk)
            merged = apply_events(current, events, pk)
            self.store.write_current(table, merged)
            if seeded:
                # The compacted partition is a strict superset of the
                # legacy extract partitions — remove them so deletes stay
                # physical (silver dedupes across all partitions).
                self.store.remove_legacy_partitions(table)
            applied[table] = len(events)
            log.info("cdc %-28s events=%-5d rows=%d", table, len(events),
                     merged.num_rows)
        if self.watermark is not None:
            for topic, part, off, table, kind, lsn in self._pending_meta:
                self.watermark.record(topic, part, off, table, kind, lsn)
            self.watermark.save()
        self._pending = {}
        self._pending_meta = []
        self._last_flush = time.monotonic()
        return applied


def run_consumer(brokers: str, lake: str | Path,
                 migrations_dir: str | Path = DEFAULT_MIGRATIONS,
                 watermark_path: str | Path | None = None,
                 batch_size: int = 500, flush_seconds: float = 10.0,
                 group_id: str = "pfaengine-analytics-cdc") -> int:
    """Blocking consume loop with graceful SIGINT/SIGTERM shutdown."""
    try:
        from kafka import KafkaConsumer
    except ImportError:
        log.error("kafka-python not installed (pip install kafka-python) — "
                  "CDC unavailable; scheduled full refresh still works")
        return 2

    pk_map = parse_primary_keys(migrations_dir)
    wm_path = watermark_path or os.environ.get(
        "CDC_WATERMARK_PATH", str(Path(lake) / "bronze" / "cdc_watermark.json"))
    wm = Watermark(wm_path)
    applier = CDCApplier(lake, pk_map, watermark=wm,
                         batch_size=batch_size, flush_seconds=flush_seconds)

    consumer = KafkaConsumer(
        bootstrap_servers=brokers.split(","),
        group_id=group_id,
        enable_auto_commit=False,
        auto_offset_reset="earliest",
        key_deserializer=lambda b: json.loads(b) if b else None,
        value_deserializer=lambda b: json.loads(b) if b else None,
    )
    consumer.subscribe(pattern=re.escape(TOPIC_PREFIX) + r"public\..*")

    stop = {"flag": False}

    def _sig(signum, _frame):
        log.info("signal %d — flushing and shutting down", signum)
        stop["flag"] = True

    signal.signal(signal.SIGINT, _sig)
    signal.signal(signal.SIGTERM, _sig)

    log.info("consuming %spublic.* from %s (group=%s, batch=%d/%gs)",
             TOPIC_PREFIX, brokers, group_id, batch_size, flush_seconds)
    try:
        while not stop["flag"]:
            for tp, msgs in consumer.poll(timeout_ms=1000).items():
                for msg in msgs:
                    lsn = msg.value.get("__lsn") if isinstance(msg.value, dict) else None
                    applier.handle(tp.topic, tp.partition, msg.offset,
                                   msg.value, msg.key, lsn=lsn)
            if applier.due():
                applier.flush()
                consumer.commit()
    finally:
        applier.flush()
        consumer.commit()
        consumer.close()
    log.info("consumer stopped cleanly (seq=%d)", wm.data["seq"])
    return 0


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(description="Debezium -> bronze parquet CDC consumer")
    p.add_argument("--brokers", default=os.environ.get("KAFKA_BROKERS", "localhost:9092"))
    p.add_argument("--lake", default=os.environ.get("LAKE_DIR", "./lake"))
    p.add_argument("--migrations", default=str(DEFAULT_MIGRATIONS))
    p.add_argument("--watermark", default=os.environ.get("CDC_WATERMARK_PATH", ""),
                   help="default: <lake>/bronze/cdc_watermark.json")
    p.add_argument("--batch-size", type=int, default=500)
    p.add_argument("--flush-seconds", type=float, default=10.0)
    p.add_argument("--group-id", default="pfaengine-analytics-cdc")
    a = p.parse_args(argv)
    return run_consumer(a.brokers, a.lake, a.migrations,
                        watermark_path=a.watermark or None,
                        batch_size=a.batch_size,
                        flush_seconds=a.flush_seconds,
                        group_id=a.group_id)


if __name__ == "__main__":  # pragma: no cover
    logging.basicConfig(level=logging.INFO, format="%(levelname)s %(message)s")
    raise SystemExit(main())
