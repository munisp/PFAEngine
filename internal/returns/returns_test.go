package returns

import (
	"errors"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

func TestDailyValuation(t *testing.T) {
	at := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	r, err := DailyValuation("t1", "II", at, money.NGN(500_000_000), money.NGN(4), money.Unit(125_000_000*money.UnitScale))
	if err != nil {
		t.Fatal(err)
	}
	if r.NAV != money.NGN(500_000_000) || r.FundCode != "II" {
		t.Fatalf("report %+v", r)
	}
	if _, err := DailyValuation("t1", "II", at, -1, money.NGN(4), 0); !errors.Is(err, ErrNegativeValue) {
		t.Fatalf("negative nav: %v", err)
	}
	if _, err := DailyValuation("t1", "II", at, 0, -1, 0); !errors.Is(err, ErrNegativeValue) {
		t.Fatalf("negative price: %v", err)
	}
	if _, err := DailyValuation("t1", "II", at, 0, 0, -1); !errors.Is(err, ErrNegativeValue) {
		t.Fatalf("negative units: %v", err)
	}
}

func TestRollingReturn(t *testing.T) {
	// 365-day series: price 100 -> 110 naira = 10% over the year.
	// Annualization factor 365/365 = 1 -> 1000 bps.
	prices := make([]money.Kobo, 365)
	for i := range prices {
		prices[i] = money.NGN(100 + int64(i)*10/364)
	}
	prices[364] = money.NGN(110)
	if got := RollingReturn(prices); got != 1000 {
		t.Fatalf("rolling return %d bps, want 1000", got)
	}
	// Half-year (182 days) 5% -> ~10.02% annualized (5%*365/182, truncated).
	half := []money.Kobo{money.NGN(100)}
	for len(half) < 182 {
		half = append(half, money.NGN(100))
	}
	half[181] = money.NGN(105)
	want := int64(500) * 365 / 182
	if got := RollingReturn(half); got != want {
		t.Fatalf("half-year rolling %d, want %d", got, want)
	}
	if got := RollingReturn(nil); got != 0 {
		t.Fatalf("empty %d", got)
	}
}

func TestSharpe(t *testing.T) {
	// returns {1100, 900, 1000, 1000} bps, rf 1000: excess {100,-100,0,0},
	// mean 0 -> Sharpe 0.
	if got := Sharpe([]int64{1100, 900, 1000, 1000}, 1000); got != 0 {
		t.Fatalf("zero-mean sharpe %d", got)
	}
	// excess {200, 200, 200, 200}: stddev 0 -> defined as 0.
	if got := Sharpe([]int64{1200, 1200, 1200, 1200}, 1000); got != 0 {
		t.Fatalf("zero-variance sharpe %d", got)
	}
	// excess {200, 0, 200, 0}: mean 100, sd 100 -> 1.00 -> 100.
	if got := Sharpe([]int64{1200, 1000, 1200, 1000}, 1000); got != 100 {
		t.Fatalf("sharpe %d, want 100", got)
	}
	if got := Sharpe(nil, 1000); got != 0 {
		t.Fatalf("empty %d", got)
	}
}

func TestTWR(t *testing.T) {
	// Two sub-periods of +10% each: 1.1*1.1 - 1 = 21% = 2100 bps.
	if got := TWR([]int64{1000, 1000}); got != 2100 {
		t.Fatalf("twr %d, want 2100", got)
	}
	// +10% then -10%: 1.1*0.9 = 0.99 -> -100 bps.
	if got := TWR([]int64{1000, -1000}); got != -100 {
		t.Fatalf("twr %d, want -100", got)
	}
	if got := TWR(nil); got != 0 {
		t.Fatalf("empty %d", got)
	}
}

func TestRMASReturns(t *testing.T) {
	at := time.Date(2026, 12, 10, 9, 0, 0, 0, time.UTC)
	m, err := MonthlyRMAS("2026-11", 1200, 84000, money.NGN(9_500_000_000), money.NGN(12_000_000), at)
	if err != nil {
		t.Fatal(err)
	}
	if m.Period != "2026-11" || m.EmployerCount != 1200 || m.Penalties != money.NGN(12_000_000) {
		t.Fatalf("monthly %+v", m)
	}
	q, err := QuarterlyRMAS(2026, 4, 1200, 84000, money.NGN(28_000_000_000), 0, at)
	if err != nil {
		t.Fatal(err)
	}
	if q.Period != "2026-Q4" {
		t.Fatalf("quarterly period %q", q.Period)
	}
	if _, err := QuarterlyRMAS(2026, 5, 0, 0, 0, 0, at); err == nil {
		t.Fatal("quarter 5 accepted")
	}
	if _, err := MonthlyRMAS("2026-11", -1, 0, 0, 0, at); !errors.Is(err, ErrNegativeValue) {
		t.Fatalf("negative counts: %v", err)
	}
}
