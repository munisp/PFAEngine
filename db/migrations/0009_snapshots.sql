-- Generic engine-state snapshots (NFR-10 durability).
-- Engines keep in-memory maps and write through to this table; boot
-- rehydration loads every row for the tenant and hands it to the owning
-- engine (see internal/persist and internal/store/snapshot_repo.go).
-- Upsert semantics: snapshots are mutable operational state, not the
-- financial ledger.

CREATE TABLE IF NOT EXISTS state_snapshot (
    kind       text        NOT NULL,           -- e.g. 'docslib.form', 'ekyc.session'
    tenant_id  text        NOT NULL,
    entity_id  text        NOT NULL,
    payload    jsonb       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, tenant_id, entity_id)
);

CREATE INDEX IF NOT EXISTS state_snapshot_tenant_idx ON state_snapshot (tenant_id);
