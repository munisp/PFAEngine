package roundup

import (
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func TestSweep(t *testing.T) {
	e := NewEngine()
	if _, err := e.Open("t1", "m1", money.NGN(100), money.NGN(5000), now); err != nil {
		t.Fatal(err)
	}
	// ₦950 purchase rounds to ₦1,000 → ₦50 sweep.
	s, err := e.SweepTx("t1", "m1", money.NGN(950), now)
	if err != nil || s.Amount != money.NGN(50) || s.RoundedTo != money.NGN(1000) {
		t.Fatalf("sweep: %v %+v", err, s)
	}
	// Exact boundary sweeps zero, books nothing.
	s, err = e.SweepTx("t1", "m1", money.NGN(500), now)
	if err != nil || s.Amount != 0 || s.ID != "" {
		t.Fatalf("boundary: %v %+v", err, s)
	}
	if got := len(e.Sweeps("t1", "m1")); got != 1 {
		t.Fatalf("sweeps: %d", got)
	}
}

func TestMonthlyCapClamps(t *testing.T) {
	e := NewEngine()
	e.Open("t1", "m1", money.NGN(100), money.NGN(60), now)
	s, _ := e.SweepTx("t1", "m1", money.NGN(910), now) // diff ₦90 → clamped to ₦60
	if s.Amount != money.NGN(60) {
		t.Fatalf("clamp: %+v", s)
	}
	if _, err := e.SweepTx("t1", "m1", money.NGN(910), now); err != ErrCapReached {
		t.Fatalf("cap: %v", err)
	}
	// New month resets the window.
	next := now.AddDate(0, 1, 0)
	s, err := e.SweepTx("t1", "m1", money.NGN(910), next)
	if err != nil || s.Amount != money.NGN(60) {
		t.Fatalf("new month: %v %+v", err, s)
	}
}

func TestValidation(t *testing.T) {
	e := NewEngine()
	if _, err := e.Open("t1", "m1", 0, 0, now); err != ErrBadStep {
		t.Fatalf("step: %v", err)
	}
	if _, err := e.SweepTx("t1", "ghost", money.NGN(1), now); err != ErrNoWallet {
		t.Fatalf("wallet: %v", err)
	}
}
