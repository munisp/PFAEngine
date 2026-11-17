package claimsfast

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/munisp/pfaengine/internal/persist"
)

// fakeSink is a map-backed persist.Sink + persist.Source for tests.
type fakeSink struct {
	mu    sync.Mutex
	snaps map[string]persist.Snapshot
}

func newFakeSink() *fakeSink { return &fakeSink{snaps: map[string]persist.Snapshot{}} }

func fakeKey(kind, tenantID, entityID string) string { return kind + "|" + tenantID + "|" + entityID }

func (f *fakeSink) SaveSnapshot(kind, tenantID, entityID string, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snaps[fakeKey(kind, tenantID, entityID)] = persist.Snapshot{
		Kind: kind, TenantID: tenantID, EntityID: entityID, Payload: append([]byte(nil), payload...)}
	return nil
}

func (f *fakeSink) DeleteSnapshot(kind, tenantID, entityID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.snaps, fakeKey(kind, tenantID, entityID))
	return nil
}

// LoadSnapshots returns all snapshots when tenantID is "" (test convenience).
func (f *fakeSink) LoadSnapshots(_ context.Context, tenantID string) ([]persist.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []persist.Snapshot
	for _, s := range f.snaps {
		if tenantID == "" || s.TenantID == tenantID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeSink) get(kind, tenantID, entityID string) (persist.Snapshot, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.snaps[fakeKey(kind, tenantID, entityID)]
	return s, ok
}

func TestPersistMutationWrites(t *testing.T) {
	fs := newFakeSink()
	e := NewEngine()
	e.AttachSink(fs)
	p := e.Open("t1", "RSA-1", now)
	snap, ok := fs.get("claimsfast.pack", "t1", p.ID)
	if !ok || !json.Valid(snap.Payload) {
		t.Fatalf("pack snapshot: %+v ok=%v", snap, ok)
	}
	var pp Pack
	if err := json.Unmarshal(snap.Payload, &pp); err != nil || pp.Score != 0 || len(pp.Items) != 5 {
		t.Fatalf("pack payload: %v %+v", err, pp)
	}
	if _, err := e.Complete("t1", "RSA-1", "KYC"); err != nil {
		t.Fatal(err)
	}
	snap, _ = fs.get("claimsfast.pack", "t1", p.ID)
	var pp2 Pack
	if err := json.Unmarshal(snap.Payload, &pp2); err != nil || pp2.Score != 20 {
		t.Fatalf("updated pack payload: %v %+v", err, pp2)
	}
}

func TestPersistRoundTrip(t *testing.T) {
	fs := newFakeSink()
	a := NewEngine()
	a.AttachSink(fs)
	a.Open("t1", "RSA-1", now)
	for _, k := range []string{"KYC", "DOCS", "EMPLOYER_CERT", "BANK_VERIFIED", "MPG_SCREEN"} {
		if _, err := a.Complete("t1", "RSA-1", k); err != nil {
			t.Fatal(err)
		}
	}
	a.Open("t1", "RSA-2", now)
	snaps, err := fs.LoadSnapshots(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	b := NewEngine()
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	for _, rsa := range []string{"RSA-1", "RSA-2"} {
		pa, _ := a.Get("t1", rsa)
		pb, ok := b.Get("t1", rsa)
		if !ok || !packsEqual(pa, pb) {
			t.Fatalf("pack %s mismatch: %+v vs %+v", rsa, pa, pb)
		}
	}
	pb, _ := b.Get("t1", "RSA-1")
	if !pb.STP || pb.Score != 100 {
		t.Fatalf("restored STP state: %+v", pb)
	}
}

// packsEqual compares packs including slices (Pack has no time compare
// issues since CreatedAt is UTC, but reflect on items/missing is needed).
func packsEqual(a, b Pack) bool {
	if a.ID != b.ID || a.TenantID != b.TenantID || a.RSAID != b.RSAID ||
		a.Score != b.Score || a.STP != b.STP || !a.CreatedAt.Equal(b.CreatedAt) {
		return false
	}
	if len(a.Items) != len(b.Items) || len(a.Missing) != len(b.Missing) {
		return false
	}
	for i := range a.Items {
		if a.Items[i] != b.Items[i] {
			return false
		}
	}
	for i := range a.Missing {
		if a.Missing[i] != b.Missing[i] {
			return false
		}
	}
	return true
}

func TestPersistSeqContinuation(t *testing.T) {
	fs := newFakeSink()
	a := NewEngine()
	a.AttachSink(fs)
	a.Open("t1", "RSA-1", now) // cf-000001
	snaps, _ := fs.LoadSnapshots(context.Background(), "")
	b := NewEngine()
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	p := b.Open("t1", "RSA-2", now)
	if p.ID != "cf-000002" {
		t.Fatalf("post-restore ID collides or regressed: %s", p.ID)
	}
}

func TestRestoreIgnoresForeignKinds(t *testing.T) {
	e := NewEngine()
	err := e.Restore([]persist.Snapshot{
		{Kind: "other.thing", TenantID: "t1", EntityID: "x", Payload: json.RawMessage(`{"id":"zz"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Get("t1", "x"); ok {
		t.Fatal("foreign snapshot leaked into engine")
	}
	if err := e.Restore([]persist.Snapshot{
		{Kind: "claimsfast.pack", TenantID: "t1", EntityID: "x", Payload: json.RawMessage(`not-json`)},
	}); err == nil {
		t.Fatal("expected unmarshal error")
	}
}
