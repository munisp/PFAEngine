# CDC Pipeline — Debezium → Kafka → Bronze Parquet → Incremental Models

Near-real-time change data capture feeding the analytics lakehouse
(analytics maturity 6 → 8). Additive to the nightly full refresh: if any
part of the CDC path is down, `analytics/run.py` without `--incremental`
still works end-to-end from Postgres.

## Architecture

```
┌───────────────────────────────────────────────────────────────────────┐
│ Postgres 16 (postgis)  wal_level=logical, publication pfaengine_pub   │
│                        replication slot debezium_slot                 │
└───────────────┬───────────────────────────────────────────────────────┘
                │ pgoutput logical decoding (WAL)
                ▼
┌───────────────────────────────┐   Connect REST :8083 (register.sh)
│ Debezium Connect 2.7          │   ExtractNewRecordState SMT (unwrap),
│ connector pfaengine-          │   delete.tombstone.handling.mode=rewrite
│   postgres-source             │   decimal.handling.mode=string
└───────────────┬───────────────┘
                │ JSON events (flat row + __op/__lsn/__source_ts_ms)
                ▼
        Kafka topics  pfaengine.public.<table>
                │
                ▼
┌───────────────────────────────┐  batch commit: 500 events or 10 s
│ analytics/cdc_consumer.py     │  upsert/delete by primary key
│ (kafka-python, pyarrow)       │  compacted current-state partition:
└───────────────┬───────────────┘  bronze/<table>/dt=9999-12-31/
                │                  part-cdc.parquet (atomic rename)
                ▼
        lake/bronze/...  +  bronze/cdc_watermark.json
                │                    (offsets, LSN, per-table seq)
                ▼
┌───────────────────────────────┐  --incremental: rebuild only models
│ analytics/run.py              │  whose upstream tables have new events
│ (DuckDB silver/gold subset)   │  (dependency map in run.py; unknown
└───────────────┬───────────────┘   table ⇒ conservative FULL rebuild)
                │ transactional swap of affected gold tables only
                ▼
        Postgres schema `analytics`
                │
                ▼
        analytics-svc :8097
        GET /v1/analytics/cdc-status  (lag/freshness from the watermark
                                       file via CDC_WATERMARK_PATH)
```

## Why the compacted-partition apply strategy

Silver models dedupe bronze by primary key with
`row_number() OVER (PARTITION BY pk ORDER BY dt DESC)` across **all**
partitions. The consumer therefore maintains one **compacted
current-state partition** per table at `dt=9999-12-31` containing the
complete current state of the table, and — crucially — **removes the
table's legacy extract partitions on first compaction**, because the
compacted partition is a strict superset of them. Deletes are physical
removals: with only one partition per CDC table, no tombstone markers
are needed and the silver/gold SQL is completely unchanged.

Alternatives considered:

- *Keep legacy partitions alongside the compacted one*: deleted rows
  resurrect — a partition that lacks a key cannot mask that key's row in
  an older partition. Rejected (correctness).
- *Append CDC events as new dt-partitions*: same resurrection problem,
  plus every silver model would need tombstone awareness. Rejected.
- *Tombstone marker column (`__deleted`) + silver filters*: works, but
  the column must exist in at least one file or DuckDB errors, so
  full-refresh-only deployments and fixture lakes break unless
  extract_full.py and every fixture also write the column. Too invasive;
  rejected.
- *Rewrite only the affected dt-partition*: events carry no partition
  key, so "affected partition" is ill-defined for updates. Rejected.

The chosen design is whole-table rewrite per flush: O(table size) I/O
per batch, trivially idempotent (upsert/delete by PK), and correct under
replay. Raw per-day extract history for CDC tables is discarded after
compaction — acceptable because Postgres remains the system of record
and bronze is a staging zone, not an archive (full-refresh tables were
already replaced daily). If a table grows beyond comfortable rewrite
cost, switch that table to a keyed store or increase `flush_seconds`.

**Full-refresh interplay:** `run.py` (default mode) deletes
`dt=9999-12-31` partitions for every table the extract succeeded on —
the fresh extract is authoritative — and the consumer re-seeds from the
new partitions on the next event. Tables the extract *failed* on keep
their CDC state (stale CDC beats an empty placeholder).

## Bring-up runbook

```bash
# 1. One-time Postgres prerequisites (see deploy/debezium/README.md):
#    - wal_level=logical (compose command override or ALTER SYSTEM+restart)
#    - migration 0011_cdc.sql: REPLICA IDENTITY FULL + CREATE PUBLICATION

# 2. Start the CDC profile (lead merges deploy/debezium/docker-compose.debezium.yml)
docker compose --profile cdc up -d connect cdc-consumer

# 3. Register the connector (idempotent PUT)
bash deploy/debezium/register.sh
#    First registration takes an initial snapshot (snapshot.mode=when_needed)
#    and emits it as __op=r events — the consumer seeds bronze from them.

# 4. Run incremental model builds (cron/systemd or after consumer flush)
python3 analytics/run.py --lake /lake --skip-extract --incremental \
    --dsn postgres://postgres:pfaengine@postgres:5432/pfaengine
```

## Monitoring

- `GET http://analytics-svc:8097/v1/analytics/cdc-status` — consumer
  watermark: global `seq`, last LSN, per-table event counts and
  `lag_seconds` (wall clock since last apply). Alert when
  `lag_seconds` exceeds the freshness SLO while traffic is flowing.
- `curl -s localhost:8083/connectors/pfaengine-postgres-source/status` —
  connector/task state must be RUNNING.
- Postgres: `SELECT slot_name, active, pg_wal_lsn_diff(pg_current_wal_lsn(),
  confirmed_flush_lsn) AS retained_bytes FROM pg_replication_slots;`

## Failure modes and recovery

| Failure | Symptom | Recovery |
|---|---|---|
| Kafka / Connect down | cdc-status `lag_seconds` grows; WAL retained by slot | Fix infra; consumer resumes from committed group offsets. Analytics unaffected (nightly full refresh still runs). |
| **Slot invalidated / dropped** (`debezium_slot` deleted, or WAL expired past `confirmed_flush_lsn` — slot bloat after long outage) | connector fails with "replication slot ... does not exist" or inconsistent snapshot errors | `register.sh` re-registers (snapshot.mode=when_needed re-snapshots when the slot has no position). A re-snapshot only replays rows that still EXIST — rows deleted from Postgres while the slot was dead would linger in the compacted partitions, so also run one **full refresh** (`run.py` without `--incremental`): it clears the `dt=9999-12-31` partitions and the consumer re-seeds from the snapshot. |
| Poison event | connector logs (errors.tolerance=all) and advances; the row is stale in bronze | Row heals on next update of that row, or on the nightly full refresh. Fix the cause, then optionally re-snapshot the table. |
| Consumer crash mid-batch | offsets not committed ⇒ batch replays | Safe: upsert/delete by PK is idempotent; the compacted partition converges to the same state. |
| Watermark file lost | `--incremental` finds no watermark | Automatic full rebuild fallback (logged). |
| Debezium schema change (new column) | extra keys in events | Consumer keeps unknown columns (schema-evolving union in `apply_events`). **No automatic migration** — add the column via a migration + full refresh to type it properly. |

**Slot bloat warning:** an inactive logical replication slot pins WAL
forever. If CDC is decommissioned, DROP the slot
(`SELECT pg_drop_replication_slot('debezium_slot')`) or Postgres will
fill its disk. Monitor `pg_replication_slots` retained bytes in
production.

## What this does NOT do

- **No exactly-once into gold.** CDC → bronze is at-least-once with
  idempotent upserts (converges); bronze → gold is a deterministic
  rebuild of affected models with a transactional per-table swap.
  Gold readers may briefly see the previous consistent state — never a
  torn one.
- **No automatic schema evolution.** New columns flow through as
  strings/extras; type changes and drops require a migration + full
  refresh.
- **No CDC for the full table set.** Only the 12 analytics-relevant
  tables are published. Adding a table = extend the publication,
  `table.include.list`, the dependency map in `run.py`, and re-register.
- **No sub-second latency.** Flush granularity is 500 events / 10 s plus
  model rebuild time; "near-real-time" here means tens of seconds.

## Maturity justification: 6 → 8

| Capability | Was (6) | Now (8) |
|---|---|---|
| Ingestion | Scheduled full/watermark extract only | + continuous CDC from WAL (Debezium/pgoutput) with snapshot + streaming phases |
| Freshness | Nightly/hourly batch | Seconds-to-minute bronze freshness; gold freshness bounded by flush + model rebuild |
| Incremental compute | Watermark-based extract, full model rebuild | Model-level incremental rebuild driven by a table→model dependency map; affected gold tables only, transactionally swapped |
| Observability | Run manifest | + durable consumer watermark (offsets/LSN/per-table seq) and an HTTP lag surface on analytics-svc |
| Failure posture | Per-table extract isolation | + idempotent replay-safe apply, poison-event tolerance at the connector, automatic full-refresh fallback everywhere |

Not 9–10: no exactly-once into gold, no schema-evolution automation,
whole-table rewrite cost model, and the dependency map is curated code
(backstopped by a drift-detecting test) rather than derived at runtime.
