package mpg

import (
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

func TestAssessBelowFloorPrivate(t *testing.T) {
	e := NewEngine()
	a := e.Assess("t1", "rsa-1", SectorPrivate, "", money.NGN(30000), "2026-11", time.Now())
	if !a.Eligible || a.Gated {
		t.Fatalf("private-sector retiree below floor must be eligible: %+v", a)
	}
	// ₦52,500 − ₦30,000 = ₦22,500 top-up (integer kobo).
	if a.TopUp != money.NGN(22500) {
		t.Fatalf("top-up = %v, want %v", a.TopUp, money.NGN(22500))
	}
}

func TestAssessAtOrAboveFloor(t *testing.T) {
	e := NewEngine()
	for _, p := range []money.Kobo{DefaultMinimumMonthly, money.NGN(90000)} {
		a := e.Assess("t1", "rsa-2", SectorPrivate, "", p, "2026-11", time.Now())
		if a.Eligible || a.TopUp != 0 {
			t.Fatalf("pension %v meets the guarantee: %+v", p, a)
		}
	}
}

func TestStateGating(t *testing.T) {
	e := NewEngine()
	// State has NOT contributed to the PPF → gated even though below floor.
	a := e.Assess("t1", "rsa-3", SectorState, "LA", money.NGN(20000), "2026-11", time.Now())
	if a.Eligible || !a.Gated || a.TopUp != 0 {
		t.Fatalf("state retiree must be gated until state contributes: %+v", a)
	}
	// State contributes → unlocked.
	if _, err := e.RecordContribution("t1", SourceState, "LA", money.NGN(500000000), "2026-11", time.Now()); err != nil {
		t.Fatal(err)
	}
	if !e.StateFunded("LA") {
		t.Fatal("LA should be PPF-funded after contribution")
	}
	a = e.Assess("t1", "rsa-3", SectorState, "LA", money.NGN(20000), "2026-11", time.Now())
	if !a.Eligible || a.Gated || a.TopUp != money.NGN(32500) {
		t.Fatalf("state retiree must be eligible after state contribution: %+v", a)
	}
}

func TestFederalUnconditional(t *testing.T) {
	e := NewEngine()
	a := e.Assess("t1", "rsa-4", SectorFederal, "", money.NGN(10000), "2026-11", time.Now())
	if !a.Eligible || a.TopUp != money.NGN(42500) {
		t.Fatalf("federal retiree must be unconditionally eligible: %+v", a)
	}
}

func TestContributionValidation(t *testing.T) {
	e := NewEngine()
	if _, err := e.RecordContribution("t1", PPFSource("DONOR"), "x", money.NGN(1), "2026-11", time.Now()); err != ErrUnknownSource {
		t.Fatalf("want ErrUnknownSource, got %v", err)
	}
	if _, err := e.RecordContribution("t1", SourceFG, "FG", 0, "2026-11", time.Now()); err != ErrBadAmount {
		t.Fatalf("want ErrBadAmount, got %v", err)
	}
	if _, err := e.RecordContribution("t1", SourceFG, "", money.NGN(1), "2026-11", time.Now()); err == nil {
		t.Fatal("contributor reference required")
	}
}

func TestPPFAggregates(t *testing.T) {
	e := NewEngine()
	now := time.Now()
	e.RecordContribution("t1", SourceFG, "FG", money.NGN(200_000_000_00), "2026-11", now)       // ₦200bn
	e.RecordContribution("t1", SourceIndustry, "INDUSTRY", money.NGN(200_000_000_00), "2026-11", now) // ₦200bn
	e.RecordContribution("t1", SourceEmployer, "emp-1", money.NGN(50_000_00), "2026-11", now)
	e.RecordContribution("t2", SourceFG, "FG", money.NGN(1), "2026-11", now) // other tenant excluded
	agg := e.PPFAggregates("t1")
	if agg[SourceFG] != money.NGN(200_000_000_00) || agg[SourceIndustry] != money.NGN(200_000_000_00) {
		t.Fatalf("aggregates wrong: %+v", agg)
	}
	var sum money.Kobo
	for _, v := range agg {
		sum += v
	}
	if sum != money.NGN(400_050_000_00) {
		t.Fatalf("total PPF = %v, want ₦400.05bn", sum)
	}
	if got := len(e.Contributions("t1")); got != 3 {
		t.Fatalf("ledger len = %d, want 3", got)
	}
}

func TestSetFloorRequiresActuarialValidation(t *testing.T) {
	e := NewEngine()
	if err := e.SetFloor(money.NGN(60000), false, "", "2027-01", time.Now()); err == nil {
		t.Fatal("unvalidated floor must be rejected")
	}
	next := time.Now().AddDate(0, 6, 0)
	if err := e.SetFloor(money.NGN(60000), true, "actuary-ref-2027", "2027-01", next); err != nil {
		t.Fatal(err)
	}
	if e.Floor().Minimum != money.NGN(60000) || !e.Floor().NextReviewDue.Equal(next) {
		t.Fatalf("floor not updated: %+v", e.Floor())
	}
	a := e.Assess("t1", "rsa-5", SectorPrivate, "", money.NGN(52500), "2027-01", time.Now())
	if !a.Eligible || a.TopUp != money.NGN(7500) {
		t.Fatalf("revised floor must apply: %+v", a)
	}
}

func TestAssessPayroll(t *testing.T) {
	e := NewEngine()
	e.RecordContribution("t1", SourceState, "LA", money.NGN(1000), "2026-11", time.Now())
	rows := []PayrollRow{
		{RSAID: "r1", Sector: SectorPrivate, MonthlyPension: money.NGN(40000)},  // +12,500
		{RSAID: "r2", Sector: SectorState, StateCode: "LA", MonthlyPension: money.NGN(50000)}, // +2,500
		{RSAID: "r3", Sector: SectorState, StateCode: "KD", MonthlyPension: money.NGN(10000)}, // gated
		{RSAID: "r4", Sector: SectorFederal, MonthlyPension: money.NGN(80000)},  // above floor
	}
	assessments, total := e.AssessPayroll("t1", "2026-11", rows, time.Now())
	if len(assessments) != 4 {
		t.Fatalf("want 4 assessments, got %d", len(assessments))
	}
	if total != money.NGN(15000) {
		t.Fatalf("total top-up = %v, want ₦15,000", total)
	}
	if !assessments[2].Gated {
		t.Fatal("KD (not PPF-funded) must be gated")
	}
}
