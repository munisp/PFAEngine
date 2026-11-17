package glidepath

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
	m, err := e.Register("t1", "m1", 49, false, true, now)
	if err != nil {
		t.Fatal(err)
	}
	snap, ok := fs.get("glidepath.member", "t1", m.ID)
	if !ok || !json.Valid(snap.Payload) {
		t.Fatalf("member snapshot: %+v ok=%v", snap, ok)
	}
	var pm Member
	if err := json.Unmarshal(snap.Payload, &pm); err != nil || pm.Fund != FundI || !pm.Aggressive {
		t.Fatalf("member payload: %v %+v", err, pm)
	}
	// 49→50 crosses the band: member update + switch record persisted.
	sw, err := e.Birthday("t1", "m1", now)
	if err != nil || sw == nil {
		t.Fatalf("birthday: %v %v", err, sw)
	}
	if _, ok := fs.get("glidepath.switch", "t1", sw.ID); !ok {
		t.Fatal("switch snapshot missing")
	}
	snap, _ = fs.get("glidepath.member", "t1", m.ID)
	var pm2 Member
	if err := json.Unmarshal(snap.Payload, &pm2); err != nil || pm2.Fund != FundIII || pm2.AgeYears != 50 {
		t.Fatalf("updated member payload: %v %+v", err, pm2)
	}
}

func TestPersistRoundTrip(t *testing.T) {
	fs := newFakeSink()
	a := NewEngine()
	a.AttachSink(fs)
	a.Register("t1", "m1", 49, false, true, now)
	a.Birthday("t1", "m1", now) // -> Fund III, aggressive lapses
	a.Register("t1", "m2", 30, false, false, now)
	a.Elect("t1", "m2", FundI, now)
	a.Retire("t1", "m2", now) // -> Fund IV
	snaps, err := fs.LoadSnapshots(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	b := NewEngine()
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"m1", "m2"} {
		ma, _ := a.Member("t1", id)
		mb, ok := b.Member("t1", id)
		if !ok || ma != mb {
			t.Fatalf("member %s mismatch: %+v vs %+v", id, ma, mb)
		}
	}
	sa, sb := a.Switches("t1", ""), b.Switches("t1", "")
	// Restore order depends on map iteration / snapshot ordering, so compare
	// as sets keyed by switch ID rather than by slice position.
	byID := func(ss []Switch) map[string]Switch {
		m := make(map[string]Switch, len(ss))
		for _, s := range ss {
			m[s.ID] = s
		}
		return m
	}
	if !reflect.DeepEqual(byID(sa), byID(sb)) {
		t.Fatalf("switches mismatch: %+v vs %+v", sa, sb)
	}
}

func TestPersistSeqContinuation(t *testing.T) {
	fs := newFakeSink()
	a := NewEngine()
	a.AttachSink(fs)
	a.Register("t1", "m1", 49, false, false, now) // gp-000001
	a.Birthday("t1", "m1", now)                   // gs-000002
	snaps, _ := fs.LoadSnapshots(context.Background(), "")
	b := NewEngine()
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	m, err := b.Register("t1", "m2", 30, false, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "gp-000003" {
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
	if _, ok := e.Member("t1", "x"); ok {
		t.Fatal("foreign snapshot leaked into engine")
	}
	if err := e.Restore([]persist.Snapshot{
		{Kind: "glidepath.member", TenantID: "t1", EntityID: "x", Payload: json.RawMessage(`not-json`)},
	}); err == nil {
		t.Fatal("expected unmarshal error")
	}
}
