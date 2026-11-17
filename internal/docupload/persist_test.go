package docupload

import (
	"testing"

	"github.com/munisp/pfaengine/internal/persist/persisttest"
)

func TestSnapshotRoundTrip(t *testing.T) {
	st := persisttest.New()
	a := New(nil)
	a.AttachSink(st)
	d, err := a.Submit("t1", "m1", DocNINSlip, "nin.png", "image/png", 1024, "s3://x", "sha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.StartReview("t1", d.ID, "rev-1"); err != nil {
		t.Fatal(err)
	}
	if st.Count("docupload.doc") == 0 {
		t.Fatal("no snapshots written")
	}

	b := New(nil)
	snaps, _ := st.LoadSnapshots(nil, "t1")
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	pend := b.Pending("t1")
	if len(pend) != 1 || pend[0].Status != UnderReview || pend[0].Reviewer != "rev-1" {
		t.Fatalf("restored doc wrong: %+v", pend)
	}
	// Seq continuation.
	d2, err := b.Submit("t1", "m2", DocIDCard, "id.png", "image/png", 100, "r", "s")
	if err != nil {
		t.Fatal(err)
	}
	if d2.ID == d.ID {
		t.Fatalf("ID collision after restore: %s", d2.ID)
	}
}
