package readiness

import (
	"context"
	"encoding/json"
	"reflect"
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

var persistInputs = Inputs{RemittancesOnTime: 12, RemittancesTotal: 12, EmployeesCovered: 50, EmployeesTotal: 50, AvgContribBps: 3000}

func TestPersistMutationWrites(t *testing.T) {
	fs := newFakeSink()
	e := NewEngine()
	e.AttachSink(fs)
	r := e.Compute("t1", "emp-1", "2026-11", persistInputs, now)
	snap, ok := fs.get("readiness.report", "t1", r.ID)
	if !ok || !json.Valid(snap.Payload) {
		t.Fatalf("report snapshot: %+v ok=%v", snap, ok)
	}
	var pr Report
	if err := json.Unmarshal(snap.Payload, &pr); err != nil || pr.Index != 100 || pr.Grade != "A" {
		t.Fatalf("report payload: %v %+v", err, pr)
	}
}

func TestPersistRoundTrip(t *testing.T) {
	fs := newFakeSink()
	a := NewEngine()
	a.AttachSink(fs)
	a.Compute("t1", "emp-1", "2026-10", persistInputs, now)
	a.Compute("t1", "emp-1", "2026-11", Inputs{RemittancesOnTime: 6, RemittancesTotal: 12, EmployeesCovered: 25, EmployeesTotal: 50, AvgContribBps: 1800}, now)
	snaps, err := fs.LoadSnapshots(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	b := NewEngine()
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	ra, rb := a.Reports("t1", "emp-1"), b.Reports("t1", "emp-1")
	if len(ra) != len(rb) || len(rb) != 2 {
		t.Fatalf("report counts: %d vs %d", len(ra), len(rb))
	}
	byID := map[string]Report{}
	for _, r := range rb {
		byID[r.ID] = r
	}
	for _, r := range ra {
		if !reflect.DeepEqual(byID[r.ID], r) {
			t.Fatalf("report mismatch: %+v vs %+v", byID[r.ID], r)
		}
	}
}

func TestPersistSeqContinuation(t *testing.T) {
	fs := newFakeSink()
	a := NewEngine()
	a.AttachSink(fs)
	a.Compute("t1", "emp-1", "2026-11", persistInputs, now) // rdy-000001
	snaps, _ := fs.LoadSnapshots(context.Background(), "")
	b := NewEngine()
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	r := b.Compute("t1", "emp-1", "2026-12", persistInputs, now)
	if r.ID != "rdy-000002" {
		t.Fatalf("post-restore ID collides or regressed: %s", r.ID)
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
	if got := e.Reports("t1", "emp-1"); len(got) != 0 {
		t.Fatalf("foreign snapshot leaked into engine: %+v", got)
	}
	if err := e.Restore([]persist.Snapshot{
		{Kind: "readiness.report", TenantID: "t1", EntityID: "x", Payload: json.RawMessage(`not-json`)},
	}); err == nil {
		t.Fatal("expected unmarshal error")
	}
}
