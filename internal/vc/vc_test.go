package vc

import (
	"errors"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

func TestContributeSplitAndLedger(t *testing.T) {
	opened := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	a := NewAccount(opened)
	a.Contribute(money.NGN(20000), opened)
	if a.Contingent != money.NGN(10000) || a.Fixed != money.NGN(10000) {
		t.Fatalf("split: %+v", a)
	}
	a.Contribute(money.Kobo(101), opened) // 50 contingent, 51 fixed (remainder to fixed)
	if a.Contingent != money.NGN(10000)+50 || a.Fixed != money.NGN(10000)+51 {
		t.Fatalf("odd split: %+v", a)
	}
	if len(a.Ledger) != 2 || a.Ledger[0].Amount != money.NGN(20000) {
		t.Fatalf("ledger: %+v", a.Ledger)
	}
}

func TestWithdrawContingentAccessLock(t *testing.T) {
	opened := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	a := NewAccount(opened)
	a.Contribute(money.NGN(20000), opened)

	if err := a.WithdrawContingent(money.NGN(5000), opened.AddDate(0, 11, 0)); !errors.Is(err, ErrAccessLock) {
		t.Fatalf("expected access lock, got %v", err)
	}
	// Exactly 1 year: allowed.
	if err := a.WithdrawContingent(money.NGN(5000), opened.AddDate(1, 0, 0)); err != nil {
		t.Fatalf("1-year withdrawal: %v", err)
	}
	if a.Contingent != money.NGN(5000) {
		t.Fatalf("balance %s", a.Contingent.Naira())
	}
	// Fixed is never accessible through contingent withdrawal.
	if err := a.WithdrawContingent(a.Contingent+1, opened.AddDate(2, 0, 0)); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("expected insufficient, got %v", err)
	}
	if a.Fixed != money.NGN(10000) {
		t.Fatal("fixed balance changed")
	}
}

func TestTaxableIncome(t *testing.T) {
	cases := []struct {
		years int
		want  money.Kobo
	}{
		{0, money.NGN(50000)},
		{4, money.NGN(50000)},
		{5, 0},
		{10, 0},
	}
	for _, tc := range cases {
		if got := TaxableIncome(money.NGN(50000), tc.years); got != tc.want {
			t.Fatalf("TaxableIncome(_, %d) = %s, want %s", tc.years, got.Naira(), tc.want.Naira())
		}
	}
}

func TestEFCCReportable(t *testing.T) {
	if EFCCReportable(EFCCReportKobo - 1) {
		t.Fatal("below threshold must not report")
	}
	if !EFCCReportable(EFCCReportKobo) {
		t.Fatal("exactly ₦5m must report")
	}
	if !EFCCReportable(money.NGN(6000000)) {
		t.Fatal("above threshold must report")
	}
}
