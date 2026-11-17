package roundup

import (
	"context"
	"encoding/json"
	"reflect"
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
	w, err := e.Open("t1", "m1", money.NGN(100), money.NGN(5000), now)
	if err != nil {
		t.Fatal(err)
	}
	snap, ok := fs.get("roundup.wallet", "t1", w.ID)
	if !ok || !json.Valid(snap.Payload) {
		t.Fatalf("wallet snapshot: %+v ok=%v", snap, ok)
	}
	var pw Wallet
	if err := json.Unmarshal(snap.Payload, &pw); err != nil || pw.Step != money.NGN(100) {
		t.Fatalf("wallet payload: %v %+v", err, pw)
	}
	s, err := e.SweepTx("t1", "m1", money.NGN(950), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fs.get("roundup.sweep", "t1", s.ID); !ok {
		t.Fatal("sweep snapshot missing")
	}
	snap, _ = fs.get("roundup.wallet", "t1", w.ID)
	var pw2 Wallet
	if err := json.Unmarshal(snap.Payload, &pw2); err != nil || pw2.Swept != money.NGN(50) {
		t.Fatalf("updated wallet payload: %v %+v", err, pw2)
	}
}

func TestPersistRoundTrip(t *testing.T) {
	fs := newFakeSink()
	a := NewEngine()
	a.AttachSink(fs)
	if _, err := a.Open("t1", "m1", money.NGN(100), money.NGN(5000), now); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SweepTx("t1", "m1", money.NGN(950), now); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Open("t1", "m2", money.NGN(50), 0, now); err != nil {
		t.Fatal(err)
	}
	snaps, err := fs.LoadSnapshots(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	b := NewEngine()
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"m1", "m2"} {
		wa, _ := a.Wallet("t1", m)
		wb, ok := b.Wallet("t1", m)
		if !ok || wa != wb {
			t.Fatalf("wallet %s mismatch: %+v vs %+v", m, wa, wb)
		}
	}
	if sa, sb := a.Sweeps("t1", "m1"), b.Sweeps("t1", "m1"); !reflect.DeepEqual(sa, sb) {
		t.Fatalf("sweeps mismatch: %+v vs %+v", sa, sb)
	}
}

func TestPersistSeqContinuation(t *testing.T) {
	fs := newFakeSink()
	a := NewEngine()
	a.AttachSink(fs)
	a.Open("t1", "m1", money.NGN(100), 0, now)
	a.SweepTx("t1", "m1", money.NGN(950), now) // seq=2 via rs-
	a.Open("t1", "m2", money.NGN(100), 0, now) // seq=3
	snaps, _ := fs.LoadSnapshots(context.Background(), "")
	b := NewEngine()
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	w, err := b.Open("t1", "m3", money.NGN(100), 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if w.ID != "ru-000004" {
		t.Fatalf("post-restore ID collides or regressed: %s", w.ID)
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
	if _, ok := e.Wallet("t1", "x"); ok {
		t.Fatal("foreign snapshot leaked into engine")
	}
	// Bad payload of an owned kind must error.
	if err := e.Restore([]persist.Snapshot{
		{Kind: "roundup.wallet", TenantID: "t1", EntityID: "x", Payload: json.RawMessage(`not-json`)},
	}); err == nil {
		t.Fatal("expected unmarshal error")
	}
}
