package store

// Generic snapshot repository (NFR-10 durability for the in-memory engines).
// One JSONB table holds every engine's mutable state as (kind, tenant,
// entity) -> payload; engines rehydrate on boot. See
// db/migrations/0009_snapshots.sql and internal/persist for the contract.

import (
	"context"
	"database/sql"

	"github.com/munisp/pfaengine/internal/persist"
)

// SnapshotRepo persists engine state snapshots, tenant-scoped via WithTenant.
// It implements persist.Sink and persist.Source.
type SnapshotRepo struct{ db *DB }

// NewSnapshotRepo returns the Postgres-backed snapshot repository.
func NewSnapshotRepo(db *DB) *SnapshotRepo { return &SnapshotRepo{db: db} }

// SaveSnapshot upserts one entity snapshot (persist.Sink). A nil receiver
// (typed-nil sink in tests) is a safe no-op.
func (r *SnapshotRepo) SaveSnapshot(kind, tenantID, entityID string, payload []byte) error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.WithTenant(context.Background(), tenantID, func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO state_snapshot (kind, tenant_id, entity_id, payload, updated_at)
			VALUES ($1,$2,$3,$4, now())
			ON CONFLICT (kind, tenant_id, entity_id) DO UPDATE SET
				payload = EXCLUDED.payload, updated_at = now()`,
			kind, tenantID, entityID, string(payload))
		return err
	})
}

// DeleteSnapshot removes one entity snapshot (persist.Sink). Nil-safe.
func (r *SnapshotRepo) DeleteSnapshot(kind, tenantID, entityID string) error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.WithTenant(context.Background(), tenantID, func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`DELETE FROM state_snapshot WHERE kind=$1 AND tenant_id=$2 AND entity_id=$3`,
			kind, tenantID, entityID)
		return err
	})
}

// LoadSnapshots returns every snapshot for a tenant (persist.Source), used
// for boot rehydration. Kinds are not filtered — each engine picks its own.
func (r *SnapshotRepo) LoadSnapshots(ctx context.Context, tenantID string) ([]persist.Snapshot, error) {
	var out []persist.Snapshot
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT kind, tenant_id, entity_id, payload FROM state_snapshot WHERE tenant_id = $1`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s persist.Snapshot
			if err := rows.Scan(&s.Kind, &s.TenantID, &s.EntityID, &s.Payload); err != nil {
				return err
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	return out, err
}
