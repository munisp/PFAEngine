# Debezium CDC for the PFAEngine lakehouse

Streams Postgres WAL changes for the analytics-relevant tables into Kafka
(`pfaengine.public.<table>` topics), where `analytics/cdc_consumer.py`
applies them to the bronze parquet zone. See `docs/CDC.md` for the full
architecture, runbook, and failure modes.

## Files

| File | Purpose |
|---|---|
| `docker-compose.debezium.yml` | Compose snippet — `connect` (debezium/connect:2.7) + `cdc-consumer` services, profile-gated (`cdc`). Lead merges into `deploy/docker-compose.yml`. |
| `postgres-source.json` | Debezium Postgres connector config (pgoutput, publication `pfaengine_pub`, slot `debezium_slot`, topic prefix `pfaengine`, unwrap SMT). |
| `register.sh` | Idempotent connector registration (PUT /connectors/.../config). |

## One-time Postgres prerequisites

1. **`wal_level=logical`** — pgoutput logical decoding requires it.
   Either merge the `postgres` service `command:` override from the
   compose snippet, or run once and restart Postgres:

   ```sql
   ALTER SYSTEM SET wal_level = 'logical';
   -- then: docker compose restart postgres
   ```

2. **Migration `0011_cdc.sql`** (content below — lead owns
   `db/migrations/`; do NOT commit from this directory):

   ```sql
   -- 0011_cdc.sql — CDC readiness for Debezium pgoutput (docs/CDC.md).
   -- REPLICA IDENTITY FULL on update-heavy tables so the WAL carries the
   -- full before-image (correct updates + deletes without a PK-change
   -- corner case). Cost: slightly larger WAL for updates on these tables.
   ALTER TABLE person                REPLICA IDENTITY FULL;
   ALTER TABLE member                REPLICA IDENTITY FULL;
   ALTER TABLE rsa_account           REPLICA IDENTITY FULL;
   ALTER TABLE employer              REPLICA IDENTITY FULL;
   ALTER TABLE contribution          REPLICA IDENTITY FULL;
   ALTER TABLE contribution_schedule REPLICA IDENTITY FULL;
   ALTER TABLE fund                  REPLICA IDENTITY FULL;
   ALTER TABLE fund_price            REPLICA IDENTITY FULL;
   ALTER TABLE unit_holding          REPLICA IDENTITY FULL;
   ALTER TABLE ops_case              REPLICA IDENTITY FULL;
   ALTER TABLE state_snapshot        REPLICA IDENTITY FULL;
   ALTER TABLE benefit_claim         REPLICA IDENTITY FULL;

   -- Publication the connector subscribes to (autocreate.mode=filtered
   -- would also create it, but explicit DDL keeps it under migration
   -- governance and survives connector deletion).
   CREATE PUBLICATION pfaengine_pub FOR TABLE
       person, member, rsa_account, employer,
       contribution, contribution_schedule,
       fund, fund_price, unit_holding,
       ops_case, state_snapshot, benefit_claim;
   ```

## Bring-up

```bash
docker compose --profile cdc up -d connect cdc-consumer
bash deploy/debezium/register.sh          # idempotent PUT of postgres-source.json
bash deploy/debezium/register.sh --delete # remove the connector
```

## Connector config notes

- `snapshot.mode=when_needed`: snapshots only when the slot has no valid
  position (first boot, or after the slot is lost/recreated) — never
  re-snapshots on a routine connector restart.
- `decimal.handling.mode=string`: NUMERIC values arrive as plain strings;
  no base64 `Decimal` structs. Money columns are BIGINT kobo anyway (DA-03).
- `ExtractNewRecordState` unwraps the Debezium envelope; the consumer sees
  flat row JSON with `__op` (`c`/`u`/`d`/`r`), `__source_ts_ms`, `__lsn`.
- `delete.tombstone.handling.mode=rewrite`: deletes arrive as a record
  with `__deleted: "true"`; Kafka tombstones are off
  (`tombstones.on.delete=false`). The consumer also tolerates raw
  tombstones (null value) defensively.
- `errors.tolerance=all`: a poison event is logged, not allowed to stall
  the connector (the slot keeps advancing; the gap surfaces in lag
  metrics and is healed by the nightly full refresh).
