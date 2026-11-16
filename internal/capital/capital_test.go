package capital

import (
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

func TestRequiredCapital(t *testing.T) {
	tests := []struct {
		name    string
		aum     money.Kobo
		special string
		want    money.Kobo
	}{
		{"small PFA", money.NGN(100_000_000_000), "", money.NGN(20_000_000_000)},
		{"NPF licence", money.NGN(100_000_000_000), "NPF", money.NGN(30_000_000_000)},
		{"exactly 500bn AUM boundary", money.NGN(500_000_000_000), "", money.NGN(20_000_000_000)},
		{"600bn AUM surcharge", money.NGN(600_000_000_000), "", money.NGN(21_000_000_000)}, // 20bn + 1% of 100bn
		{"NPF above threshold", money.NGN(600_000_000_000), "NPF", money.NGN(31_000_000_000)},
		{"just below threshold", money.NGN(500_000_000_000) - 1, "", money.NGN(20_000_000_000)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RequiredCapital(tt.aum, tt.special); got != tt.want {
				t.Fatalf("RequiredCapital = %s, want %s", got.Naira(), tt.want.Naira())
			}
		})
	}
}

func TestAssess(t *testing.T) {
	asOf := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	// Compliant: funds exactly meet the requirement.
	s := Assess(money.NGN(20_000_000_000), money.NGN(100_000_000_000), asOf)
	if !s.Compliant || s.Shortfall != 0 || !s.CureDeadline.IsZero() {
		t.Fatalf("compliant %+v", s)
	}
	// Shortfall: 90-day cure deadline.
	s = Assess(money.NGN(15_000_000_000), money.NGN(600_000_000_000), asOf)
	if s.Compliant {
		t.Fatal("expected non-compliant")
	}
	if s.Shortfall != money.NGN(6_000_000_000) { // req 21bn - 15bn
		t.Fatalf("shortfall %s", s.Shortfall.Naira())
	}
	wantDeadline := asOf.AddDate(0, 0, 90)
	if !s.CureDeadline.Equal(wantDeadline) {
		t.Fatalf("cure deadline %v, want %v", s.CureDeadline, wantDeadline)
	}
}
