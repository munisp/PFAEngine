package regfile

import (
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var (
	due   = time.Date(2026, 11, 14, 17, 0, 0, 0, time.UTC)
	now   = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	later = time.Date(2026, 12, 20, 12, 0, 0, 0, time.UTC)
)

func TestLifecycleOnTime(t *testing.T) {
	e := NewEngine()
	f := e.Schedule("t1", FilingMonthlySchedule, "2026-11", due)
	if f.Status != StatusPending {
		t.Fatalf("schedule: %+v", f)
	}
	f, err := e.Generate("t1", f.ID, "<xml>schedule</xml>", due.Add(-time.Hour))
	if err != nil || f.Status != StatusGenerated {
		t.Fatalf("generate: %v %+v", err, f)
	}
	f, err = e.File("t1", f.ID, money.NGN(1_000_000), due.Add(-time.Minute))
	if err != nil || f.Status != StatusFiled || f.Penalty != 0 {
		t.Fatalf("file: %v %+v", err, f)
	}
}

func TestLatePenalty(t *testing.T) {
	e := NewEngine()
	f := e.Schedule("t1", FilingMonthlySchedule, "2026-10", due)
	f, _ = e.Generate("t1", f.ID, "payload", now) // 3 days late
	if f.Status != StatusLate {
		t.Fatalf("late status: %+v", f)
	}
	// Filed 36 days after due on ₦1m outstanding: 2 months late → 4% = ₦40,000.
	f, err := e.File("t1", f.ID, money.NGN(1_000_000), later)
	if err != nil || f.Penalty != money.NGN(40_000) {
		t.Fatalf("penalty: %v %+v", err, f)
	}
}

func TestGuardsAndDueSoon(t *testing.T) {
	e := NewEngine()
	f := e.Schedule("t1", FilingQuarterlyReturn, "2026-Q4", due)
	// Cannot file before generate.
	if _, err := e.File("t1", f.ID, 0, now); err != ErrBadTransition {
		t.Fatalf("early file: %v", err)
	}
	if _, err := e.Generate("t1", "ghost", "x", now); err != ErrNoFiling {
		t.Fatalf("ghost: %v", err)
	}
	soon := e.DueSoon("t1", now, 48*time.Hour)
	if len(soon) != 1 {
		t.Fatalf("due soon: %v", soon)
	}
}
