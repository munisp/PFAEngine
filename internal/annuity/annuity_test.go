package annuity

import (
	"errors"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

func TestRankQuotesFiltersAndSorts(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	future := now.Add(72 * time.Hour)
	past := now.Add(-time.Hour)
	quotes := []Quote{
		{InsurerID: "ins-low", InsurerName: "LowLife", NAICOMLicensed: true, AnnualPayoutPer100k: money.NGN(9000), GuaranteedYears: 10, ExpiresAt: future},
		{InsurerID: "ins-unlicensed", InsurerName: "Shady Assurance", NAICOMLicensed: false, AnnualPayoutPer100k: money.NGN(99999), ExpiresAt: future},
		{InsurerID: "ins-expired", InsurerName: "Stale Life", NAICOMLicensed: true, AnnualPayoutPer100k: money.NGN(50000), ExpiresAt: past},
		{InsurerID: "ins-high", InsurerName: "TopLife", NAICOMLicensed: true, AnnualPayoutPer100k: money.NGN(12000), GuaranteedYears: 15, ExpiresAt: future},
		{InsurerID: "ins-tie-a", InsurerName: "TieA", NAICOMLicensed: true, AnnualPayoutPer100k: money.NGN(10000), ExpiresAt: future},
		{InsurerID: "ins-tie-b", InsurerName: "TieB", NAICOMLicensed: true, AnnualPayoutPer100k: money.NGN(10000), ExpiresAt: future},
	}
	ranked := RankQuotes(quotes, now)
	if len(ranked) != 4 {
		t.Fatalf("want 4 quotes after filtering, got %d", len(ranked))
	}
	wantOrder := []string{"ins-high", "ins-tie-a", "ins-tie-b", "ins-low"}
	for i, id := range wantOrder {
		if ranked[i].InsurerID != id {
			t.Fatalf("ranked[%d] = %s, want %s (full: %v)", i, ranked[i].InsurerID, id, ranked)
		}
	}
	// Boundary: a quote expiring exactly "now" is expired.
	edge := []Quote{{InsurerID: "edge", NAICOMLicensed: true, AnnualPayoutPer100k: money.NGN(1), ExpiresAt: now}}
	if got := RankQuotes(edge, now); len(got) != 0 {
		t.Fatalf("quote expiring at evaluation time must be dropped, got %v", got)
	}
}

func TestValidatePremium(t *testing.T) {
	cases := []struct {
		name    string
		premium money.Kobo
		balance money.Kobo
		wantErr error
	}{
		{"ok", money.NGN(5000000), money.NGN(10000000), nil},
		{"equal to balance", money.NGN(10000000), money.NGN(10000000), nil},
		{"zero", 0, money.NGN(10000000), ErrPremiumNonPositive},
		{"negative", money.Kobo(-1), money.NGN(10000000), ErrPremiumNonPositive},
		{"exceeds balance", money.NGN(10000001), money.NGN(10000000), ErrPremiumExceedsRSA},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePremium(tc.premium, tc.balance); !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestPurchaseStateMachine(t *testing.T) {
	p, err := NewPurchase("RSA-1", "ins-high", money.NGN(5000000), money.NGN(10000000), "officer-a")
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != Requested {
		t.Fatalf("new purchase status = %v", p.Status)
	}
	// Instruct before approval is rejected.
	if err := p.Instruct(); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("instruct before approve: %v", err)
	}
	// Maker-checker: submitter cannot approve own purchase.
	if err := p.Approve("officer-a"); !errors.Is(err, ErrMakerCheckerConflict) {
		t.Fatalf("self-approval: %v", err)
	}
	if err := p.Approve(""); !errors.Is(err, ErrMakerCheckerConflict) {
		t.Fatalf("empty approver: %v", err)
	}
	if p.Status != Requested {
		t.Fatal("failed approvals must not change state")
	}
	// Happy path: different approver, then instruct.
	if err := p.Approve("officer-b"); err != nil {
		t.Fatal(err)
	}
	if p.Status != Approved || p.ApprovedBy != "officer-b" {
		t.Fatalf("after approve: %+v", p)
	}
	if err := p.Approve("officer-c"); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("double approve: %v", err)
	}
	if err := p.Instruct(); err != nil {
		t.Fatal(err)
	}
	if p.Status != Instructed {
		t.Fatalf("final status = %v", p.Status)
	}
	if err := p.Instruct(); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("double instruct: %v", err)
	}
}

func TestNewPurchaseValidatesPremium(t *testing.T) {
	if _, err := NewPurchase("RSA-1", "ins", money.NGN(200), money.NGN(100), "a"); !errors.Is(err, ErrPremiumExceedsRSA) {
		t.Fatalf("err = %v", err)
	}
	if _, err := NewPurchase("RSA-1", "ins", 0, money.NGN(100), "a"); !errors.Is(err, ErrPremiumNonPositive) {
		t.Fatalf("err = %v", err)
	}
}
