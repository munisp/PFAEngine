package kyb

import (
	"testing"

	"github.com/munisp/pfaengine/internal/persist/persisttest"
)

func TestSnapshotRoundTrip(t *testing.T) {
	st := persisttest.New()
	a := New(nil, nil)
	a.AttachSink(st)
	b1, err := a.Submit("t1", "emp-1", "Acme Ltd", "RC123456", "12345678",
		[]Person{{Name: "Ada Lovelace", Role: "Director"}},
		[]UBO{{Person: Person{Name: "Ada Lovelace"}, OwnershipPct: 60}},
		[]string{"s3://cac"}, "ops")
	if err != nil {
		t.Fatal(err)
	}
	if b1.Status != StatusVerified {
		t.Fatalf("status = %s", b1.Status)
	}

	b := New(nil, nil)
	snaps, _ := st.LoadSnapshots(nil, "t1")
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	got, err := b.Get("t1", b1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusVerified || got.RCNumber != "RC123456" || len(got.UBOs) != 1 {
		t.Fatalf("restored business wrong: %+v", got)
	}
	// Seq continuation.
	b2, err := b.Submit("t1", "emp-2", "Beta Ltd", "RC654321", "",
		[]Person{{Name: "X"}}, []UBO{{Person: Person{Name: "X"}, OwnershipPct: 100}},
		[]string{"s3://cac"}, "ops")
	if err != nil {
		t.Fatal(err)
	}
	if b2.ID == b1.ID {
		t.Fatalf("ID collision: %s", b2.ID)
	}
}
