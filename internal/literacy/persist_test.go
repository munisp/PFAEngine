package literacy

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
	l := e.AddLesson(LangEN, "AVC", "Save more", "body", "q?", []string{"a", "b"}, 1)
	snap, ok := fs.get("literacy.lesson", "_global", l.ID)
	if !ok || !json.Valid(snap.Payload) {
		t.Fatalf("lesson snapshot: %+v ok=%v", snap, ok)
	}
	// The hidden Answer field must survive in the snapshot payload.
	var pl map[string]any
	if err := json.Unmarshal(snap.Payload, &pl); err != nil || pl["answer"] != float64(1) {
		t.Fatalf("lesson payload must carry answer: %v %v", err, pl)
	}
	p, err := e.Answer("t1", "m1", l.ID, 1, now)
	if err != nil || !p.Passed {
		t.Fatalf("answer: %v %+v", err, p)
	}
	snap, ok = fs.get("literacy.progress", "t1", "t1|m1|"+l.ID)
	if !ok || !json.Valid(snap.Payload) {
		t.Fatalf("progress snapshot: %+v ok=%v", snap, ok)
	}
	var pp Progress
	if err := json.Unmarshal(snap.Payload, &pp); err != nil || !pp.Passed {
		t.Fatalf("progress payload: %v %+v", err, pp)
	}
}

func TestPersistRoundTrip(t *testing.T) {
	fs := newFakeSink()
	a := NewEngine()
	a.AttachSink(fs)
	l1 := a.AddLesson(LangEN, "AVC", "Save more", "body", "q?", []string{"a", "b"}, 1)
	l2 := a.AddLesson(LangPC, "MPG", "Di floor", "bodi", "q?", []string{"x", "y"}, 0)
	a.Answer("t1", "m1", l1.ID, 1, now) // pass
	a.Answer("t1", "m1", l2.ID, 1, now) // fail
	snaps, err := fs.LoadSnapshots(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	b := NewEngine()
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	if ca, cb := a.Catalogue("", ""), b.Catalogue("", ""); len(ca) != len(cb) || len(cb) != 2 {
		t.Fatalf("catalogue mismatch: %d vs %d", len(ca), len(cb))
	}
	if pa, ta := a.Score("t1", "m1"); pa != 1 || ta != 2 {
		t.Fatalf("engine A score: %d/%d", pa, ta)
	}
	if pb, tb := b.Score("t1", "m1"); pb != 1 || tb != 2 {
		t.Fatalf("restored score: %d/%d", pb, tb)
	}
	// The hidden Answer index must be restored: answering correctly passes.
	p, err := b.Answer("t1", "m2", l1.ID, 1, now)
	if err != nil || !p.Passed {
		t.Fatalf("restored lesson answer check: %v %+v", err, p)
	}
}

func TestPersistSeqContinuation(t *testing.T) {
	fs := newFakeSink()
	a := NewEngine()
	a.AttachSink(fs)
	a.AddLesson(LangEN, "AVC", "t", "b", "q", []string{"a"}, 0) // lit-000001
	snaps, _ := fs.LoadSnapshots(context.Background(), "")
	b := NewEngine()
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	l := b.AddLesson(LangEN, "MPG", "t2", "b2", "q2", []string{"a"}, 0)
	if l.ID != "lit-000002" {
		t.Fatalf("post-restore ID collides or regressed: %s", l.ID)
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
	if got := e.Catalogue("", ""); len(got) != 0 {
		t.Fatalf("foreign snapshot leaked into engine: %+v", got)
	}
	if err := e.Restore([]persist.Snapshot{
		{Kind: "literacy.lesson", TenantID: "_global", EntityID: "x", Payload: json.RawMessage(`not-json`)},
	}); err == nil {
		t.Fatal("expected unmarshal error")
	}
}
