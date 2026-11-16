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

// FR-513: NOK notification at suspension trigger + 5-year evidence retention.
func TestNOKNotifyAndEvidenceRetention(t *testing.T) {
	r := NewRegister()
	start := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	r.Enroll("t1", "rsa-1", money.NGN(250000), "bank-1", start)
	r.Enroll("t1", "rsa-2", money.NGN(100000), "bank-2", start)
	if !r.SetNextOfKin("rsa-1", "nok-9") {
		t.Fatal("set NOK failed")
	}

	// Evidence retention = verification date + 5 years.
	if !r.RecordLivenessEvidence("rsa-1", start) {
		t.Fatal("record evidence failed")
	}
	rt, _ := r.Get("rsa-1")
	want := start.AddDate(5, 0, 0)
	if !rt.EvidenceRetainedUntil.Equal(want) {
		t.Fatalf("retention %s, want %s", rt.EvidenceRetainedUntil, want)
	}

	// Before the suspension trigger: no NOK notification.
	if rt.NOKNotifyDue(start.AddDate(1, 3, 0)) {
		t.Fatal("NOK notify must not fire before grace end")
	}
	// After 12mo + 3mo grace: due; only rsa-1 has a NOK recorded.
	past := start.AddDate(1, 3, 1)
	if !rt.NOKNotifyDue(past) {
		t.Fatal("NOK notify must fire after grace end")
	}
	if _, susp := r.Campaign("t1", past); len(susp) != 2 {
		t.Fatalf("expected both suspended, got %v", susp)
	}
	nok := r.NOKNotifications("t1", past)
	if len(nok) != 1 || nok[0] != "rsa-1" {
		t.Fatalf("NOK notifications %v", nok)
	}
}
