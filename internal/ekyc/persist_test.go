package ekyc

import (
	"testing"

	"github.com/munisp/pfaengine/internal/persist/persisttest"
)

func TestSnapshotRoundTrip(t *testing.T) {
	st := persisttest.New()
	a := New(nil, nil, nil)
	a.AttachSink(st)
	s := a.Start("t1", "m1", "member")
	if _, err := a.SubmitDocument("t1", s.ID, "NIN_SLIP", "ref://1", "", false, "member"); err != nil {
		t.Fatal(err)
	}
	if st.Count("ekyc.session") == 0 {
		t.Fatal("no snapshots written")
	}

	b := New(nil, nil, nil)
	snaps, _ := st.LoadSnapshots(nil, "t1")
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	got, err := b.Get("t1", s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateDocAnalysis || got.MemberID != "m1" || len(got.History) == 0 {
		t.Fatalf("restored session wrong: %+v", got)
	}
	if got.RiskScore == 0 {
		t.Fatal("risk flags lost in restore")
	}
	// Seq continuation.
	s2 := b.Start("t1", "m2", "member")
	if s2.ID == s.ID {
		t.Fatalf("ID collision after restore: %s", s2.ID)
	}
	// Foreign kinds ignored.
	if err := b.Restore(nil); err != nil {
		t.Fatal(err)
	}
}
