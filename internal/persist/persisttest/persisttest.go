// Package persisttest provides an in-memory persist.Sink + persist.Source
// for engine round-trip tests (write-through → restore → identical state).
package persisttest

import (
	"context"
	"sync"

	"github.com/munisp/pfaengine/internal/persist"
)

// Store is a map-backed snapshot store keyed by kind|tenant|entity.
type Store struct {
	mu    sync.Mutex
	byKey map[string]persist.Snapshot
}

func New() *Store { return &Store{byKey: map[string]persist.Snapshot{}} }

func key(kind, tenantID, entityID string) string { return kind + "|" + tenantID + "|" + entityID }

func (s *Store) SaveSnapshot(kind, tenantID, entityID string, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := append([]byte(nil), payload...)
	s.byKey[key(kind, tenantID, entityID)] = persist.Snapshot{
		Kind: kind, TenantID: tenantID, EntityID: entityID, Payload: cp,
	}
	return nil
}

func (s *Store) DeleteSnapshot(kind, tenantID, entityID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byKey, key(kind, tenantID, entityID))
	return nil
}

func (s *Store) LoadSnapshots(ctx context.Context, tenantID string) ([]persist.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []persist.Snapshot
	for _, snap := range s.byKey {
		if snap.TenantID == tenantID {
			out = append(out, snap)
		}
	}
	return out, nil
}

// LoadAll returns every snapshot regardless of tenant (multi-tenant tests).
func (s *Store) LoadAll() []persist.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]persist.Snapshot, 0, len(s.byKey))
	for _, snap := range s.byKey {
		out = append(out, snap)
	}
	return out
}

// Count reports how many snapshots of a kind exist.
func (s *Store) Count(kind string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, snap := range s.byKey {
		if snap.Kind == kind {
			n++
		}
	}
	return n
}
