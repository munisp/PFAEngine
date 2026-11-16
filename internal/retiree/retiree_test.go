package retiree

import (
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

func TestLivenessGraceAndSuspension(t *testing.T) {
	r := NewRegister()
	start := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	r.Enroll("t1", "rsa-1", money.NGN(250000), "bank-1", start)

	// Before due: active.
	due, susp := r.Campaign("t1", start.AddDate(0, 6, 0))
	if len(due)+len(susp) != 0 {
		t.Fatal("should be active mid-cycle")
	}
	// After 12 months: grace.
	due, susp = r.Campaign("t1", start.AddDate(1, 1, 0))
	if len(due) != 1 || len(susp) != 0 {
		t.Fatalf("expected grace, got due=%v susp=%v", due, susp)
	}
	// After 12+3 months: suspended.
	due, susp = r.Campaign("t1", start.AddDate(1, 4, 0))
	if len(susp) != 1 {
		t.Fatal("expected suspension after grace")
	}
	// Suspended excluded from payroll.
	run := r.RunPayroll("t1", "2027-06", start.AddDate(1, 5, 0))
	if run.Count != 0 || run.Total != 0 {
		t.Fatalf("suspended retiree must be gated from payroll: %+v", run)
	}
	// Reactivation.
	if !r.RecordLiveness("rsa-1", start.AddDate(1, 5, 0)) {
		t.Fatal("reactivation failed")
	}
	run = r.RunPayroll("t1", "2027-06", start.AddDate(1, 5, 0))
	if run.Count != 1 || run.Total != money.NGN(250000) {
		t.Fatalf("payroll after reactivation: %+v", run)
	}
}
