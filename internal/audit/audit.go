// Package audit implements SC-02 / NFR-05 / FR-306: append-only
// hash-chained WORM event store with nightly chain verification and
// dual-control export.
package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrChainBroken = errors.New("audit: hash chain verification failed")
var ErrDualControl = errors.New("audit: export requires two distinct staff approvers")

// Event is a WORM audit record. Hash chains: H(n) = sha256(H(n-1) | record).
type Event struct {
	Seq        uint64          `json:"seq"`
	TenantID   string          `json:"tenant_id"`
	Actor      string          `json:"actor"`
	Action     string          `json:"action"`
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	Before     json.RawMessage `json:"before,omitempty"`
	After      json.RawMessage `json:"after,omitempty"`
	ConsentRef string          `json:"consent_ref,omitempty"`
	KYCRef     string          `json:"kyc_ref,omitempty"`
	At         time.Time       `json:"at"`
	PrevHash   string          `json:"prev_hash"`
	Hash       string          `json:"hash"`
}

type Record struct {
	TenantID, Actor, Action, EntityType, EntityID string
	Before, After                                 any
	ConsentRef, KYCRef                            string
}

// Store is the append-only chain.
type Store struct {
	mu     sync.Mutex
	events []Event
	tip    string
}

func NewStore() *Store { return &Store{} }

func computeHash(prev string, e *Event) string {
	h := sha256.New()
	h.Write([]byte(prev))
	h.Write([]byte(fmt.Sprintf("%d|%s|%s|%s|%s|%s|%s|%s",
		e.Seq, e.TenantID, e.Actor, e.Action, e.EntityType, e.EntityID,
		e.At.UTC().Format(time.RFC3339Nano), string(e.After))))
	return hex.EncodeToString(h.Sum(nil))
}

// Append writes a record to the chain. No update or delete exists (WORM).
func (s *Store) Append(r Record) Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := Event{
		Seq: uint64(len(s.events)) + 1, TenantID: r.TenantID, Actor: r.Actor,
		Action: r.Action, EntityType: r.EntityType, EntityID: r.EntityID,
		ConsentRef: r.ConsentRef, KYCRef: r.KYCRef, At: time.Now().UTC(), PrevHash: s.tip,
	}
	if r.Before != nil {
		e.Before, _ = json.Marshal(r.Before)
	}
	if r.After != nil {
		e.After, _ = json.Marshal(r.After)
	}
	e.Hash = computeHash(s.tip, &e)
	s.events = append(s.events, e)
	s.tip = e.Hash
	return e
}

// Verify recomputes the whole chain (run nightly, SC-02).
func (s *Store) Verify() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := ""
	for i := range s.events {
		e := &s.events[i]
		if e.PrevHash != prev || e.Hash != computeHash(prev, e) {
			return fmt.Errorf("%w at seq %d", ErrChainBroken, e.Seq)
		}
		prev = e.Hash
	}
	return nil
}

// Events returns the chain (for inspection/replay).
func (s *Store) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event{}, s.events...)
}

// Export produces a PenCom inspection slice under dual control (§6.3 audit export).
func (s *Store) Export(tenantID string, approver1, approver2 string) ([]Event, error) {
	if approver1 == "" || approver2 == "" || approver1 == approver2 {
		return nil, ErrDualControl
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Event
	for _, e := range s.events {
		if e.TenantID == tenantID {
			out = append(out, e)
		}
	}
	return out, nil
}
