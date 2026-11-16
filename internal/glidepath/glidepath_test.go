package glidepath

import (
	"testing"
	"time"
)

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func TestDefaults(t *testing.T) {
	if DefaultFundFor(30, false) != FundII || DefaultFundFor(52, false) != FundIII || DefaultFundFor(52, true) != FundIV {
		t.Fatal("defaults wrong")
	}
}

func TestLifecycleSwitches(t *testing.T) {
	e := NewEngine()
	m, err := e.Register("t1", "m1", 49, false, true, now)
	if err != nil || m.Fund != FundI {
		t.Fatalf("register aggressive: %v %+v", err, m)
	}
	// 49 -> 50: Fund I election lapses, switch to III.
	sw, err := e.Birthday("t1", "m1", now)
	if err != nil || sw == nil || sw.To != FundIII || sw.Reason != "AGE_BAND" {
		t.Fatalf("birthday: %v %+v", err, sw)
	}
	m, _ = e.Member("t1", "m1")
	if m.Aggressive {
		t.Fatal("aggressive election should lapse at 50")
	}
	// Retirement -> IV.
	sw2, err := e.Retire("t1", "m1", now)
	if err != nil || sw2.To != FundIV {
		t.Fatalf("retire: %v %+v", err, sw2)
	}
	if got := len(e.Switches("t1", "m1")); got != 2 {
		t.Fatalf("history: %d", got)
	}
}

func TestElectionGuards(t *testing.T) {
	e := NewEngine()
	e.Register("t1", "m1", 55, false, false, now)
	if _, err := e.Elect("t1", "m1", FundI, now); err != ErrFundNotAllowed {
		t.Fatalf("50+ into Fund I must fail: %v", err)
	}
	if _, err := e.Elect("t1", "m1", FundIV, now); err != ErrFundNotAllowed {
		t.Fatalf("non-retiree into Fund IV must fail: %v", err)
	}
	if _, err := e.Elect("t1", "ghost", FundII, now); err != ErrNoMember {
		t.Fatalf("ghost: %v", err)
	}
}
