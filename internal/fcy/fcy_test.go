package fcy

import (
	"errors"
	"testing"
	"time"
)

func TestContributeSplit(t *testing.T) {
	opened := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := NewAccount(opened)
	a.Contribute(10000, opened) // $100 -> 6000 retirement / 4000 contingent
	if a.Retirement != 6000 || a.Contingent != 4000 {
		t.Fatalf("split: %+v", a)
	}
	a.Contribute(101, opened) // 60 retirement, 41 contingent (remainder to retirement)
	if a.Retirement != 6060 || a.Contingent != 4041 {
		t.Fatalf("odd split: %+v", a)
	}
}

func TestWithdrawSeasoning(t *testing.T) {
	opened := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := NewAccount(opened)
	a.Contribute(100000, opened)
	if err := a.Withdraw(1000, opened.AddDate(0, 5, 29)); !errors.Is(err, ErrSeasoning) {
		t.Fatalf("expected seasoning, got %v", err)
	}
	// Exactly 6 months: allowed.
	if err := a.Withdraw(1000, opened.AddDate(0, 6, 0)); err != nil {
		t.Fatalf("6-month withdrawal: %v", err)
	}
	if a.Contingent != 39000 {
		t.Fatalf("balance %d", a.Contingent)
	}
}

func TestWithdrawYearlyCap(t *testing.T) {
	opened := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := NewAccount(opened)
	a.Contribute(1000000, opened)
	m := opened.AddDate(0, 7, 0)
	if err := a.Withdraw(1000, m); err != nil {
		t.Fatal(err)
	}
	if err := a.Withdraw(1000, m.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	// Third within the same rolling year: blocked.
	if err := a.Withdraw(1000, m.AddDate(0, 2, 0)); !errors.Is(err, ErrWithdrawalCap) {
		t.Fatalf("expected cap, got %v", err)
	}
	// Rolling year elapses (YearStart was the first withdrawal): allowed again.
	if err := a.Withdraw(1000, m.AddDate(1, 0, 0)); err != nil {
		t.Fatalf("new rolling year: %v", err)
	}
	// Contingent-only: cannot exceed contingent balance.
	if err := a.Withdraw(a.Contingent+1, m.AddDate(1, 0, 1)); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("expected insufficient, got %v", err)
	}
	if a.Retirement != 600000 {
		t.Fatal("retirement balance changed")
	}
}

func TestNFIUReportable(t *testing.T) {
	cases := []struct {
		amount Cents
		want   bool
	}{
		{999_999, false},
		{1_000_000, false}, // exactly $10,000: not strictly greater
		{1_000_001, true},
		{50_000_000, true},
	}
	for _, tc := range cases {
		if got := NFIUReportable(tc.amount); got != tc.want {
			t.Fatalf("NFIUReportable(%d) = %v, want %v", tc.amount, got, tc.want)
		}
	}
}
