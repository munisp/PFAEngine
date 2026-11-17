// Package persist defines the generic snapshot-persistence contract used by
// the in-memory engines (NFR-10 durability). Engines keep their fast
// in-memory maps and write through to a Sink on every mutation; on boot the
// main process loads all Snapshots for a tenant from a Source and hands them
// to each engine's Restore method. The Postgres implementation lives in
// internal/store (SnapshotRepo); tests use in-memory fakes.
//
// The contract is deliberately engine-agnostic: a Snapshot is a
// (kind, tenant, entity) triple with a JSON payload. Each engine owns its
// kind namespace (e.g. "docslib.form", "ekyc.session") and the meaning of
// the payload. Upsert semantics apply — snapshots are mutable operational
// state, not the financial ledger (which stays in the core store repos).
package persist

import (
	"context"
	"encoding/json"
)

// Snapshot is one persisted entity.
type Snapshot struct {
	Kind     string          `json:"kind"`
	TenantID string          `json:"tenant_id"`
	EntityID string          `json:"entity_id"`
	Payload  json.RawMessage `json:"payload"`
}

// Sink receives write-through mutations. SaveSnapshot upserts; DeleteSnapshot
// removes (used when an entity is deleted, e.g. archived content purged).
type Sink interface {
	SaveSnapshot(kind, tenantID, entityID string, payload []byte) error
	DeleteSnapshot(kind, tenantID, entityID string) error
}

// Source loads every snapshot for a tenant (boot rehydration). Engines
// ignore kinds they do not own.
type Source interface {
	LoadSnapshots(ctx context.Context, tenantID string) ([]Snapshot, error)
}

// OnError receives persistence failures when a Sink is attached. Engines are
// best-effort write-through: a snapshot failure must not break the hot path,
// but it must never be silent in production. main.go wires this to the
// structured logger; tests replace it to assert on failures.
var OnError = func(error) {}

// Save marshals v and upserts it into s. A nil sink is a no-op (in-memory
// mode). Errors are reported to OnError and swallowed by design.
func Save(s Sink, kind, tenantID, entityID string, v any) {
	if s == nil {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		OnError(err)
		return
	}
	if err := s.SaveSnapshot(kind, tenantID, entityID, b); err != nil {
		OnError(err)
	}
}

// Delete removes an entity from s. A nil sink is a no-op.
func Delete(s Sink, kind, tenantID, entityID string) {
	if s == nil {
		return
	}
	if err := s.DeleteSnapshot(kind, tenantID, entityID); err != nil {
		OnError(err)
	}
}

// OfKind filters snaps to a single kind.
func OfKind(snaps []Snapshot, kind string) []Snapshot {
	var out []Snapshot
	for _, s := range snaps {
		if s.Kind == kind {
			out = append(out, s)
		}
	}
	return out
}
