package succession

import (
	"testing"
	"time"
)

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

var year = 365 * 24 * time.Hour

func TestNominateVersionedWORM(t *testing.T) {
	e := NewEngine(year)
	// Shares must total 100%.
	if _, err := e.Nominate("t1", "m1", []Beneficiary{{Name: "A", ShareBps: 5000}}, now); err != ErrShares {
		t.Fatalf("shares: %v", err)
	}
	n1, err := e.Nominate("t1", "m1", []Beneficiary{{Name: "Ada", ShareBps: 6000}, {Name: "Bola", ShareBps: 4000}}, now)
	if err != nil || n1.Version != 1 {
		t.Fatalf("v1: %v %+v", err, n1)
	}
	n2, err := e.Nominate("t1", "m1", []Beneficiary{{Name: "Ada", ShareBps: 10000}}, now.AddDate(0, 3, 0))
	if err != nil || n2.Version != 2 {
		t.Fatalf("v2: %v %+v", err, n2)
	}
	cur, _ := e.Current("t1", "m1")
	if cur.Version != 2 || len(cur.Beneficiaries) != 1 {
		t.Fatalf("current: %+v", cur)
	}
	hist := e.History("t1", "m1")
	if len(hist) != 2 || !hist[0].Superseded || hist[1].Superseded {
		t.Fatalf("history: %+v", hist)
	}
}

func TestReconfirmSchedule(t *testing.T) {
	e := NewEngine(year)
	e.Nominate("t1", "m1", []Beneficiary{{Name: "A", ShareBps: 10000}}, now)
	// Not due immediately.
	if due := e.DueForReconfirm("t1", now.AddDate(0, 6, 0)); len(due) != 0 {
		t.Fatalf("early due: %v", due)
	}
	// Due after a year.
	if due := e.DueForReconfirm("t1", now.AddDate(1, 1, 0)); len(due) != 1 || due[0] != "m1" {
		t.Fatalf("due: %v", due)
	}
	// Reconfirm pushes the deadline out.
	n, err := e.Reconfirm("t1", "m1", now.AddDate(1, 1, 0))
	if err != nil || !n.NextReconfirmDue.After(now.AddDate(2, 0, 0)) {
		t.Fatalf("reconfirm: %v %+v", err, n)
	}
	if due := e.DueForReconfirm("t1", now.AddDate(1, 2, 0)); len(due) != 0 {
		t.Fatalf("still due: %v", due)
	}
}
