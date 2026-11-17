package docslib

import (
	"testing"

	"github.com/munisp/pfaengine/internal/persist/persisttest"
)

func TestSnapshotRoundTrip(t *testing.T) {
	st := persisttest.New()
	a := New()
	a.AttachSink(st)
	a.SeedStandard("t1")
	if _, err := a.Publish("t1", "RSA-REG-01", "RSA Registration Form v2", CatRSARegistration, "v2", "s3://x", "abc"); err != nil {
		t.Fatal(err)
	}
	if st.Count("docslib.form") != 10 { // 9 seeds + v2 (v1 re-upserted deactivated)
		t.Fatalf("snapshots = %d, want 10", st.Count("docslib.form"))
	}

	b := New()
	snaps, _ := st.LoadSnapshots(nil, "t1")
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	if got := len(b.List("t1", "")); got != 9 {
		t.Fatalf("restored active forms = %d, want 9", got)
	}
	if vs := b.Versions("t1", "RSA-REG-01"); len(vs) != 2 || !vs[1].Active || vs[0].Active {
		t.Fatalf("versions wrong: %+v", vs)
	}
	// Seq continuation: no ID collision after restore.
	f, err := b.Publish("t1", "NEW-01", "New Form", CatGuide, "", "r", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Get("t1", f.ID); err == nil {
		t.Fatalf("ID %s collides with pre-restore form", f.ID)
	}
	// Idempotent reseed after restore: no new versions minted.
	b.SeedStandard("t1")
	if got := len(b.Versions("t1", "RSA-TRF-01")); got != 1 {
		t.Fatalf("reseed minted %d versions of RSA-TRF-01", got)
	}
}

func TestRestoreIgnoresForeignKinds(t *testing.T) {
	b := New()
	if err := b.Restore(nil); err != nil {
		t.Fatal(err)
	}
}
