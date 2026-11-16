package micropension

import (
	"errors"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

func TestContributeSplit(t *testing.T) {
	var a Account
	t0 := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	a.Contribute(money.NGN(10000), t0) // 4,000 / 6,000
	if a.Contingent != money.NGN(4000) || a.Retirement != money.NGN(6000) {
		t.Fatalf("split: %+v", a)
	}
	if !a.FirstContribution.Equal(t0) {
		t.Fatal("first contribution not recorded")
	}
	// Odd kobo: remainder goes to retirement.
	a.Contribute(money.Kobo(101), t0.AddDate(0, 0, 1)) // 40 -> contingent, 61 -> retirement
	if a.Contingent != money.NGN(4000)+40 || a.Retirement != money.NGN(6000)+61 {
		t.Fatalf("odd split: %+v", a)
	}
	if !a.FirstContribution.Equal(t0) {
		t.Fatal("first contribution must not move")
	}
}

func TestWithdrawContingentSeasoning(t *testing.T) {
	var a Account
	t0 := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	a.Contribute(money.NGN(10000), t0)

	// Too early: 2 months in.
	if err := a.WithdrawContingent(money.NGN(1000), t0.AddDate(0, 2, 0)); !errors.Is(err, ErrSeasoning) {
		t.Fatalf("expected seasoning error, got %v", err)
	}
	// Exactly 3 months: allowed.
	if err := a.WithdrawContingent(money.NGN(1000), t0.AddDate(0, 3, 0)); err != nil {
		t.Fatalf("3-month withdrawal: %v", err)
	}
	if a.Contingent != money.NGN(3000) {
		t.Fatalf("balance %s", a.Contingent.Naira())
	}
}

func TestWithdrawContingentWeeklyCadence(t *testing.T) {
	var a Account
	t0 := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	a.Contribute(money.NGN(10000), t0)
	first := t0.AddDate(0, 3, 0)
	if err := a.WithdrawContingent(money.NGN(500), first); err != nil {
		t.Fatal(err)
	}
	// 6 days later: too soon.
	if err := a.WithdrawContingent(money.NGN(500), first.Add(6*24*time.Hour)); !errors.Is(err, ErrWithdrawalCadence) {
		t.Fatalf("expected cadence error, got %v", err)
	}
	// 7 days later: allowed.
	if err := a.WithdrawContingent(money.NGN(500), first.Add(7*24*time.Hour)); err != nil {
		t.Fatalf("weekly withdrawal: %v", err)
	}
	if a.Contingent != money.NGN(3000) {
		t.Fatalf("balance %s", a.Contingent.Naira())
	}
}

func TestWithdrawContingentLimits(t *testing.T) {
	var a Account
	t0 := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	a.Contribute(money.NGN(1000), t0)
	at := t0.AddDate(0, 4, 0)
	if err := a.WithdrawContingent(a.Contingent+1, at); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("expected insufficient, got %v", err)
	}
	if err := a.WithdrawContingent(0, at); !errors.Is(err, ErrNonPositive) {
		t.Fatalf("expected non-positive, got %v", err)
	}
	// Retirement balance is never touchable via contingent withdrawal.
	if a.Retirement != money.NGN(600) {
		t.Fatalf("retirement balance changed: %+v", a)
	}
}

func TestWithdrawBeforeAnyContribution(t *testing.T) {
	var a Account
	if err := a.WithdrawContingent(money.NGN(1), time.Now()); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("expected insufficient on empty account, got %v", err)
	}
}
