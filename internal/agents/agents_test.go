package agents

import (
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func TestAccreditEnrolCommission(t *testing.T) {
	e := NewEngine()
	a := e.Accredit("t1", "Musa Agent", "0801", "KD", 250, now) // 2.5%
	en, err := e.Enrol("t1", a.ID, "RSA-A1", "Trader Amara", "0802", "trader", now)
	if err != nil || en.Trade != "trader" {
		t.Fatalf("enrol: %v %+v", err, en)
	}
	// Commission: 2.5% of ₦20,000 = ₦500.
	c, err := e.PayCommission("t1", a.ID, money.NGN(20_000), "2026-11", now)
	if err != nil || c.Amount != money.NGN(500) {
		t.Fatalf("commission: %v %+v", err, c)
	}
	if total := e.CommissionTotal("t1", a.ID); total != money.NGN(500) {
		t.Fatalf("total: %v", total)
	}
	if lb := e.Leaderboard("t1"); lb[a.ID] != 1 {
		t.Fatalf("leaderboard: %v", lb)
	}
}

func TestSuspensionAndBatch(t *testing.T) {
	e := NewEngine()
	a := e.Accredit("t1", "Bala Agent", "0803", "KN", 250, now)
	if err := e.Suspend("t1", a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Enrol("t1", a.ID, "RSA-X", "X", "0", "farmer", now); err != ErrSuspended {
		t.Fatalf("suspended: %v", err)
	}
	// Batch against unknown agent fails per-row, not fatally.
	ok, failed := e.EnrolBatch("t1", "ghost", []struct{ RSAID, Name, Phone, Trade string }{
		{RSAID: "RSA-1", Name: "A", Phone: "1", Trade: "rider"},
		{RSAID: "RSA-2", Name: "B", Phone: "2", Trade: "artisan"},
	}, now)
	if len(ok) != 0 || len(failed) != 2 {
		t.Fatalf("batch: %d ok %d failed", len(ok), len(failed))
	}
}

func TestValidation(t *testing.T) {
	e := NewEngine()
	a := e.Accredit("t1", "A", "0", "LA", 250, now)
	if _, err := e.PayCommission("t1", a.ID, 0, "2026-11", now); err != ErrBadAmount {
		t.Fatalf("amount: %v", err)
	}
	if _, err := e.PayCommission("t1", "ghost", money.NGN(1), "2026-11", now); err != ErrNoAgent {
		t.Fatalf("agent: %v", err)
	}
}
