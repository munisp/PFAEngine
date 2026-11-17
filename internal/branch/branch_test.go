package branch

import (
	"testing"
	"time"
)

func future() string { return time.Now().Add(48 * time.Hour).Format("2006-01-02") }

func TestBookCapacityAndLifecycle(t *testing.T) {
	e := New()
	b, err := e.AddBranch(Branch{TenantID: "t1", Name: "Yaba", State: "LA", SlotsPerDay: 2})
	if err != nil {
		t.Fatal(err)
	}
	date := future()
	free, err := e.Availability("t1", b.ID, date)
	if err != nil || len(free) != 2 {
		t.Fatalf("availability: %v %v", free, err)
	}
	a1, err := e.Book("t1", b.ID, "m1", date, 0, "RSA registration")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Book("t1", b.ID, "m2", date, 0, "KYC update"); err == nil {
		t.Fatal("double-booking must fail")
	}
	if _, err := e.Book("t1", b.ID, "m2", "2020-01-01", 1, "past"); err == nil {
		t.Fatal("past date must fail")
	}
	if _, err := e.Transition("t1", a1.ID, Done); err == nil {
		t.Fatal("BOOKED → DONE must fail")
	}
	if _, err := e.Transition("t1", a1.ID, CheckedIn); err != nil {
		t.Fatal(err)
	}
	got, err := e.Transition("t1", a1.ID, Done)
	if err != nil || got.Status != Done {
		t.Fatalf("lifecycle: %v %+v", err, got)
	}
	list := e.Appointments("t1", "m1", "", "")
	if len(list) != 1 {
		t.Fatalf("list: %d", len(list))
	}
	if len(e.Branches("t1", "LA")) != 1 || len(e.Branches("t1", "AB")) != 0 {
		t.Fatal("state filter broken")
	}
}
