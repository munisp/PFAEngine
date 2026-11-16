package readiness

import (
	"testing"
	"time"
)

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func TestComputeGrades(t *testing.T) {
	e := NewEngine()
	// Perfect employer: 12/12 on time, 100% coverage, 3000bps average.
	r := e.Compute("t1", "emp-1", "2026-11",
		Inputs{RemittancesOnTime: 12, RemittancesTotal: 12, EmployeesCovered: 50, EmployeesTotal: 50, AvgContribBps: 3000}, now)
	if r.Index != 100 || r.Grade != "A" {
		t.Fatalf("perfect: %+v", r)
	}
	// Statutory-minimum employer, all on time, full coverage: 40+30+18 = 88.
	r = e.Compute("t1", "emp-2", "2026-11",
		Inputs{RemittancesOnTime: 12, RemittancesTotal: 12, EmployeesCovered: 20, EmployeesTotal: 20, AvgContribBps: 1800}, now)
	if r.Adequacy != 60 || r.Index != 88 || r.Grade != "A" {
		t.Fatalf("minimum: %+v", r)
	}
	// Defaulter: half on time, 60% coverage, 1800bps: 20+18+18 = 56 => C.
	r = e.Compute("t1", "emp-3", "2026-11",
		Inputs{RemittancesOnTime: 6, RemittancesTotal: 12, EmployeesCovered: 12, EmployeesTotal: 20, AvgContribBps: 1800}, now)
	if r.Index != 56 || r.Grade != "C" {
		t.Fatalf("defaulter: %+v", r)
	}
	if got := len(e.Reports("t1", "emp-1")); got != 1 {
		t.Fatalf("history: %d", got)
	}
}

func TestZeroDenominators(t *testing.T) {
	e := NewEngine()
	r := e.Compute("t1", "emp-9", "2026-11", Inputs{}, now)
	if r.Index != 0 || r.Grade != "D" {
		t.Fatalf("zero: %+v", r)
	}
}
