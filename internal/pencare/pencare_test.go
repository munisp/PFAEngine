package pencare

import (
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func TestEnrollBelowFloor(t *testing.T) {
	e := NewEngine(money.NGN(52500))
	en, err := e.Enroll("t1", "RSA-1", money.NGN(30000), now)
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if en.Status != EnrolleeActive || en.Floor != money.NGN(52500) {
		t.Fatalf("bad enrollee: %+v", en)
	}
	// Idempotent re-enrolment by RSA.
	en2, err := e.Enroll("t1", "RSA-1", money.NGN(30000), now)
	if err != nil || en2.ID != en.ID {
		t.Fatalf("re-enroll: %v %+v", err, en2)
	}
}

func TestEnrollRejectsAtOrAboveFloor(t *testing.T) {
	e := NewEngine(money.NGN(52500))
	if _, err := e.Enroll("t1", "RSA-2", money.NGN(52500), now); err != ErrNotLowIncome {
		t.Fatalf("at floor: got %v", err)
	}
	if _, err := e.Enroll("t1", "RSA-3", money.NGN(90000), now); err != ErrNotLowIncome {
		t.Fatalf("above floor: got %v", err)
	}
}

func TestCardLifecycle(t *testing.T) {
	e := NewEngine(money.NGN(52500))
	en, _ := e.Enroll("t1", "RSA-1", money.NGN(20000), now)

	c, err := e.RequestCard("t1", en.ID, now)
	if err != nil || c.Status != CardRequested {
		t.Fatalf("request: %v %+v", err, c)
	}
	// Second live card rejected.
	if _, err := e.RequestCard("t1", en.ID, now); err != ErrCardExists {
		t.Fatalf("dup card: got %v", err)
	}
	// Cannot activate before issue.
	if _, err := e.ActivateCard("t1", c.ID); err != ErrBadTransition {
		t.Fatalf("premature activate: got %v", err)
	}
	c, err = e.IssueCard("t1", c.ID, "PC-0001-2026", now)
	if err != nil || c.Status != CardIssued || c.CardNumber != "PC-0001-2026" {
		t.Fatalf("issue: %v %+v", err, c)
	}
	c, err = e.ActivateCard("t1", c.ID)
	if err != nil || c.Status != CardActive {
		t.Fatalf("activate: %v %+v", err, c)
	}
	// Still rejected while ACTIVE.
	if _, err := e.RequestCard("t1", en.ID, now); err != ErrCardExists {
		t.Fatalf("dup after active: got %v", err)
	}
}

func TestCardRequiresActiveEnrollee(t *testing.T) {
	e := NewEngine(money.NGN(52500))
	en, _ := e.Enroll("t1", "RSA-1", money.NGN(20000), now)
	if err := e.SetStatus("t1", en.ID, EnrolleeSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RequestCard("t1", en.ID, now); err != ErrNotActive {
		t.Fatalf("suspended card: got %v", err)
	}
	if _, err := e.RecordPremium("t1", en.ID, "HMO-X", money.NGN(5000), "2026-11", now); err != ErrNotActive {
		t.Fatalf("suspended premium: got %v", err)
	}
}

func TestPremiums(t *testing.T) {
	e := NewEngine(money.NGN(52500))
	en, _ := e.Enroll("t1", "RSA-1", money.NGN(20000), now)
	if _, err := e.RecordPremium("t1", en.ID, "HMO-X", 0, "2026-11", now); err != ErrBadAmount {
		t.Fatalf("zero premium: got %v", err)
	}
	p, err := e.RecordPremium("t1", en.ID, "HMO-X", money.NGN(4500), "2026-11", now)
	if err != nil || p.Amount != money.NGN(4500) {
		t.Fatalf("premium: %v %+v", err, p)
	}
	ps := e.Premiums("t1", en.ID)
	if len(ps) != 1 {
		t.Fatalf("premiums list: %d", len(ps))
	}
	if _, err := e.RecordPremium("t1", "nope", "HMO-X", money.NGN(1), "2026-11", now); err != ErrNoEnrollee {
		t.Fatalf("unknown enrollee: got %v", err)
	}
}

func TestFundContributionsAndAggregates(t *testing.T) {
	e := NewEngine(money.NGN(52500))
	// Industry pool ₦7bn + PFA ₦500m + donor ₦250m.
	if _, err := e.RecordFundContribution("t1", SourceIndustry, "penop-pool", money.NGN(7_000_000_000), "2026-11", now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RecordFundContribution("t1", SourcePFA, "pfa-001", money.NGN(500_000_000), "2026-11", now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RecordFundContribution("t1", SourceDonor, "fsd-africa", money.NGN(250_000_000), "2026-11", now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RecordFundContribution("t1", "BOGUS", "x", money.NGN(1), "2026-11", now); err != ErrUnknownSource {
		t.Fatalf("bad source: got %v", err)
	}
	if _, err := e.RecordFundContribution("t1", SourcePFA, "x", -5, "2026-11", now); err != ErrBadAmount {
		t.Fatalf("negative: got %v", err)
	}
	agg := e.FundAggregates("t1")
	if agg[SourceIndustry] != money.NGN(7_000_000_000) || agg[SourcePFA] != money.NGN(500_000_000) || agg[SourceDonor] != money.NGN(250_000_000) {
		t.Fatalf("aggregates: %v", agg)
	}
	// Tenant isolation.
	if got := len(e.FundContributions("t2")); got != 0 {
		t.Fatalf("tenant leak: %d", got)
	}
}

func TestCoverage(t *testing.T) {
	e := NewEngine(money.NGN(52500))
	en1, _ := e.Enroll("t1", "RSA-1", money.NGN(20000), now)
	en2, _ := e.Enroll("t1", "RSA-2", money.NGN(30000), now)
	c, _ := e.RequestCard("t1", en1.ID, now)
	c, _ = e.IssueCard("t1", c.ID, "PC-1", now)
	if _, err := e.ActivateCard("t1", c.ID); err != nil {
		t.Fatal(err)
	}
	_, _ = e.RequestCard("t1", en2.ID, now) // requested, not active
	e.RecordPremium("t1", en1.ID, "HMO-X", money.NGN(4500), "2026-11", now)
	e.RecordPremium("t1", en2.ID, "HMO-X", money.NGN(4500), "2026-11", now)

	n, cards, prem := e.Coverage("t1")
	if n != 2 || cards != 1 || prem != money.NGN(9000) {
		t.Fatalf("coverage: enrollees=%d cards=%d prem=%v", n, cards, prem)
	}
}
