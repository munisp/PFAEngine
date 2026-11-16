package mortgage

import (
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func TestCapAndWorkflow(t *testing.T) {
	e := NewEngine()
	bal := money.NGN(8_000_000)
	if MaxEquity(bal) != money.NGN(2_000_000) {
		t.Fatal("cap")
	}
	// Over-cap rejected.
	if _, err := e.Apply("t1", "m1", bal, money.NGN(2_000_001), "lender-x", true, now); err != ErrOverCap {
		t.Fatalf("over cap: %v", err)
	}
	// Unemployed rejected.
	if _, err := e.Apply("t1", "m1", bal, money.NGN(1_000_000), "lender-x", false, now); err != ErrNotEmployed {
		t.Fatalf("employed: %v", err)
	}
	a, err := e.Apply("t1", "m1", bal, money.NGN(2_000_000), "lender-x", true, now)
	if err != nil || a.Status != AppSubmitted {
		t.Fatalf("apply: %v %+v", err, a)
	}
	// Cannot disburse before approval.
	if _, err := e.Disburse("t1", a.ID, now); err != ErrBadTransition {
		t.Fatalf("early disburse: %v", err)
	}
	a, _ = e.Approve("t1", a.ID, now)
	a, _ = e.Disburse("t1", a.ID, now)
	if a.Status != AppDisbursed || len(a.Events) != 3 {
		t.Fatalf("workflow: %+v", a)
	}
}

func TestReject(t *testing.T) {
	e := NewEngine()
	a, _ := e.Apply("t1", "m1", money.NGN(4_000_000), money.NGN(1_000_000), "l", true, now)
	a, err := e.Reject("t1", a.ID, "incomplete docs", now)
	if err != nil || a.Status != AppRejected || a.Reason != "incomplete docs" {
		t.Fatalf("reject: %v %+v", err, a)
	}
	if _, err := e.Approve("t1", a.ID, now); err != ErrBadTransition {
		t.Fatalf("approve rejected: %v", err)
	}
}
