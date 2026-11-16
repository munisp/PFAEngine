package advisory

import (
	"testing"

	"github.com/munisp/pfaengine/internal/money"
)

func TestProjectOnTrack(t *testing.T) {
	// ₦5m balance, ₦50k/month, 20y at 12% — healthy earner vs modest target.
	p, err := Project(money.NGN(5_000_000), money.NGN(50_000), 20, 1200,
		money.NGN(300_000), 5000, 240) // target = 50% of salary = ₦150k
	if err != nil {
		t.Fatal(err)
	}
	if p.ProjectedBalance <= p.BalanceNow {
		t.Fatalf("no growth: %+v", p)
	}
	if !p.OnTrack || p.Gap != 0 || p.ExtraMonthly != 0 {
		t.Fatalf("should be on track: %+v", p)
	}
}

func TestProjectGapClosedByExtra(t *testing.T) {
	// Low balance, low contribution -> gap; extra must actually close it.
	p, err := Project(money.NGN(500_000), money.NGN(10_000), 10, 1200,
		money.NGN(200_000), 5000, 240)
	if err != nil {
		t.Fatal(err)
	}
	if p.OnTrack || p.Gap <= 0 || p.ExtraMonthly <= 0 {
		t.Fatalf("expected gap: %+v", p)
	}
	// Verify the recommendation closes the gap under the same assumptions.
	check, _ := Project(money.NGN(500_000), money.NGN(10_000)+p.ExtraMonthly, 10, 1200,
		money.NGN(200_000), 5000, 240)
	if !check.OnTrack {
		t.Fatalf("extra %v did not close gap: %+v", p.ExtraMonthly, check)
	}
	// One kobo less must fail (binary search minimal).
	below, _ := Project(money.NGN(500_000), money.NGN(10_000)+p.ExtraMonthly-1, 10, 1200,
		money.NGN(200_000), 5000, 240)
	if below.OnTrack {
		t.Fatal("extra not minimal")
	}
}

func TestValidation(t *testing.T) {
	if _, err := Project(1, 1, 0, 1200, 1, 5000, 240); err != ErrBadHorizon {
		t.Fatalf("horizon: %v", err)
	}
	if _, err := Project(1, 1, 10, 0, 1, 5000, 240); err != ErrBadRate {
		t.Fatalf("rate: %v", err)
	}
}
