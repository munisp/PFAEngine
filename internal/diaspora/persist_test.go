package diaspora

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
	e.SetRate("USD", 153000)
	snap, ok := fs.get("diaspora.rate", "_global", "USD")
	if !ok || !json.Valid(snap.Payload) {
		t.Fatalf("rate snapshot: %+v ok=%v", snap, ok)
	}
	var pr rateState
	if err := json.Unmarshal(snap.Payload, &pr); err != nil || pr.KoboPerUnit != 153000 {
		t.Fatalf("rate payload: %v %+v", err, pr)
	}
	m, err := e.Enrol("t1", "RSA-D1", "GB", "P-GB-123", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fs.get("diaspora.member", "t1", m.ID); !ok {
		t.Fatal("member snapshot missing")
	}
	c, err := e.Contribute("t1", "RSA-D1", "USD", 50000, now)
	if err != nil {
		t.Fatal(err)
	}
	snap, ok = fs.get("diaspora.contribution", "t1", c.ID)
	if !ok {
		t.Fatal("contribution snapshot missing")
	}
	var pc Contribution
	if err := json.Unmarshal(snap.Payload, &pc); err != nil || pc.Credited != money.NGN(765_000) {
		t.Fatalf("contribution payload: %v %+v", err, pc)
	}
}

func TestPersistRoundTrip(t *testing.T) {
	fs := newFakeSink()
	a := NewEngine()
	a.AttachSink(fs)
	a.SetRate("USD", 153000)
	a.SetRate("GBP", 190000)
	a.Enrol("t1", "RSA-D1", "GB", "P-GB-123", now)
	a.Contribute("t1", "RSA-D1", "USD", 50000, now)
	a.Contribute("t1", "RSA-D1", "GBP", 10000, now)
	snaps, err := fs.LoadSnapshots(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	b := NewEngine()
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	ca, ta := a.Contributions("t1", "RSA-D1")
	cb, tb := b.Contributions("t1", "RSA-D1")
	if ta != tb || !reflect.DeepEqual(ca, cb) {
		t.Fatalf("contributions mismatch: %+v/%v vs %+v/%v", ca, ta, cb, tb)
	}
	// Rates and membership restored: a fresh contribution books.
	if _, err := b.Contribute("t1", "RSA-D1", "USD", 100, now); err != nil {
		t.Fatalf("contribute after restore: %v", err)
	}
}

func TestPersistSeqContinuation(t *testing.T) {
	fs := newFakeSink()
	a := NewEngine()
	a.AttachSink(fs)
	a.SetRate("USD", 153000)
	a.Enrol("t1", "RSA-D1", "GB", "P-GB-123", now) // dia-000001
	a.Contribute("t1", "RSA-D1", "USD", 100, now)  // dc-000002
	snaps, _ := fs.LoadSnapshots(context.Background(), "")
	b := NewEngine()
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	m, err := b.Enrol("t1", "RSA-D2", "US", "P-US-9", now)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "dia-000003" {
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
	if got, total := e.Contributions("t1", "x"); len(got) != 0 || total != 0 {
		t.Fatalf("foreign snapshot leaked into engine: %+v %v", got, total)
	}
	if err := e.Restore([]persist.Snapshot{
		{Kind: "diaspora.member", TenantID: "t1", EntityID: "x", Payload: json.RawMessage(`not-json`)},
	}); err == nil {
		t.Fatal("expected unmarshal error")
	}
}
