package esg

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

func TestPersistMutationWrites(t *testing.T) {
	fs := newFakeSink()
	e := NewEngine()
	e.AttachSink(fs)
	m := e.Report("t1", "im-1", "idp-1", 500, 20, 35, 1200, 80, 90, 85, now)
	snap, ok := fs.get("esg.report", "t1", m.ID)
	if !ok || !json.Valid(snap.Payload) {
		t.Fatalf("report snapshot: %+v ok=%v", snap, ok)
	}
	var pm Metrics
	if err := json.Unmarshal(snap.Payload, &pm); err != nil || pm.JobsCreated != 500 || pm.DeploymentID != "idp-1" {
		t.Fatalf("report payload: %v %+v", err, pm)
	}
}

func TestPersistRoundTrip(t *testing.T) {
	fs := newFakeSink()
	a := NewEngine()
	a.AttachSink(fs)
	a.Report("t1", "im-1", "idp-1", 500, 20, 35, 1200, 80, 90, 85, now)
	a.Report("t1", "im-1", "idp-2", 300, 10, 15, 0, 70, 80, 75, now)
	snaps, err := fs.LoadSnapshots(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	b := NewEngine()
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	if ra, rb := a.Reports("t1", "im-1"), b.Reports("t1", "im-1"); !reflect.DeepEqual(ra, rb) {
		t.Fatalf("reports mismatch: %+v vs %+v", ra, rb)
	}
	if ua, ub := a.Rollup("t1", "im-1"), b.Rollup("t1", "im-1"); ua != ub {
		t.Fatalf("rollup mismatch: %+v vs %+v", ua, ub)
	}
}

func TestPersistSeqContinuation(t *testing.T) {
	fs := newFakeSink()
	a := NewEngine()
	a.AttachSink(fs)
	a.Report("t1", "im-1", "idp-1", 1, 1, 1, 1, 50, 50, 50, now) // esg-000001
	snaps, _ := fs.LoadSnapshots(context.Background(), "")
	b := NewEngine()
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	m := b.Report("t1", "im-1", "idp-2", 2, 2, 2, 2, 60, 60, 60, now)
	if m.ID != "esg-000002" {
		t.Fatalf("post-restore ID collides or regressed: %s", m.ID)
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
	if got := e.Reports("t1", "im-1"); len(got) != 0 {
		t.Fatalf("foreign snapshot leaked into engine: %+v", got)
	}
	if err := e.Restore([]persist.Snapshot{
		{Kind: "esg.report", TenantID: "t1", EntityID: "x", Payload: json.RawMessage(`not-json`)},
	}); err == nil {
		t.Fatal("expected unmarshal error")
	}
}
