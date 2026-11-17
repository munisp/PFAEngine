package regfile

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/munisp/pfaengine/internal/money"
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
	f := e.Schedule("t1", FilingMonthlySchedule, "2026-11", due)
	snap, ok := fs.get("regfile.filing", "t1", f.ID)
	if !ok || !json.Valid(snap.Payload) {
		t.Fatalf("filing snapshot: %+v ok=%v", snap, ok)
	}
	var pf Filing
	if err := json.Unmarshal(snap.Payload, &pf); err != nil || pf.Status != StatusPending {
		t.Fatalf("filing payload: %v %+v", err, pf)
	}
	// State transition persists the updated filing under the same ID.
	// (now is past the due date, so Generate marks the filing LATE.)
	if _, err := e.Generate("t1", f.ID, "<xml/>", now); err != nil {
		t.Fatal(err)
	}
	snap, _ = fs.get("regfile.filing", "t1", f.ID)
	var pf2 Filing
	if err := json.Unmarshal(snap.Payload, &pf2); err != nil || pf2.Status != StatusLate || pf2.Payload != "<xml/>" {
		t.Fatalf("updated filing payload: %v %+v", err, pf2)
	}
}

func TestPersistRoundTrip(t *testing.T) {
	fs := newFakeSink()
	a := NewEngine()
	a.AttachSink(fs)
	f1 := a.Schedule("t1", FilingMonthlySchedule, "2026-11", due)
	a.Generate("t1", f1.ID, "<xml/>", now)
	f2 := a.Schedule("t1", FilingQuarterlyReturn, "2026-Q4", due)
	a.Generate("t1", f2.ID, "<q/>", later) // late
	a.File("t1", f2.ID, money.NGN(1_000_000), later)
	snaps, err := fs.LoadSnapshots(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	b := NewEngine()
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	la, lb := a.List("t1"), b.List("t1")
	if len(la) != len(lb) || len(lb) != 2 {
		t.Fatalf("filing counts: %d vs %d", len(la), len(lb))
	}
	byID := map[string]Filing{}
	for _, f := range lb {
		byID[f.ID] = f
	}
	for _, f := range la {
		g, ok := byID[f.ID]
		if !ok || g.Status != f.Status || g.Penalty != f.Penalty || g.Payload != f.Payload ||
			(g.FiledAt == nil) != (f.FiledAt == nil) {
			t.Fatalf("filing mismatch: %+v vs %+v", f, g)
		}
	}
	// Restored state behaves: the filed filing must reject Generate.
	if _, err := b.Generate("t1", f2.ID, "x", now); err != ErrBadTransition {
		t.Fatalf("post-restore transition guard: %v", err)
	}
}

func TestPersistSeqContinuation(t *testing.T) {
	fs := newFakeSink()
	a := NewEngine()
	a.AttachSink(fs)
	a.Schedule("t1", FilingMonthlySchedule, "2026-11", due) // rf-000001
	snaps, _ := fs.LoadSnapshots(context.Background(), "")
	b := NewEngine()
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	f := b.Schedule("t1", FilingAnnualStatement, "2026", due)
	if f.ID != "rf-000002" {
		t.Fatalf("post-restore ID collides or regressed: %s", f.ID)
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
	if got := e.List("t1"); len(got) != 0 {
		t.Fatalf("foreign snapshot leaked into engine: %+v", got)
	}
	if err := e.Restore([]persist.Snapshot{
		{Kind: "regfile.filing", TenantID: "t1", EntityID: "x", Payload: json.RawMessage(`not-json`)},
	}); err == nil {
		t.Fatal("expected unmarshal error")
	}
}
