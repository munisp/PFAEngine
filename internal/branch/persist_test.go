package branch

import (
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/persist/persisttest"
)

func TestSnapshotRoundTrip(t *testing.T) {
	st := persisttest.New()
	a := New()
	a.AttachSink(st)
	br, err := a.AddBranch(Branch{TenantID: "t1", Name: "Ikeja", State: "Lagos"})
	if err != nil {
		t.Fatal(err)
	}
	date := time.Now().Add(48 * time.Hour).Format("2006-01-02")
	appt, err := a.Book("t1", br.ID, "m1", date, 3, "benefit enquiry")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Transition("t1", appt.ID, CheckedIn); err != nil {
		t.Fatal(err)
	}

	b := New()
	snaps, _ := st.LoadSnapshots(nil, "t1")
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	if brs := b.Branches("t1", "Lagos"); len(brs) != 1 || brs[0].Name != "Ikeja" {
		t.Fatalf("branches: %+v", brs)
	}
	free, err := b.Availability("t1", br.ID, date)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range free {
		if s == 3 {
			t.Fatalf("slot 3 should stay taken after restore: %v", free)
		}
	}
	// Seq continuation on both counters.
	br2, _ := b.AddBranch(Branch{TenantID: "t1", Name: "Yaba", State: "Lagos"})
	if br2.ID == br.ID {
		t.Fatalf("branch ID collision: %s", br2.ID)
	}
	if _, err := b.Book("t1", br.ID, "m2", date, 3, "x"); err == nil {
		t.Fatal("double-book of restored appointment should fail")
	}
}
