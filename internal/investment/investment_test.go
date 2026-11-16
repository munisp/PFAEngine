package investment

import (
	"errors"
	"testing"

	"github.com/munisp/pfaengine/internal/money"
)

// Fund II: 75% variable-income cap (rules pack).
var fundIILimits = LimitsForFund("II", map[string]int64{"II": 7500})

func hasBreach(bs []Breach, rule string) bool {
	for _, b := range bs {
		if b.Rule == rule {
			return true
		}
	}
	return false
}

func TestCheckComplianceClean(t *testing.T) {
	nav := money.NGN(20_000_000)
	hs := []Holding{
		{AssetClass: "EQUITY", IssuerID: "dangote", IssueID: "dangcem", Value: money.NGN(1_000_000), VariableIncome: true},
		{AssetClass: "EQUITY", IssuerID: "gtco", IssueID: "gtb", Value: money.NGN(1_000_000), VariableIncome: true},
		{AssetClass: "FGN_BOND", IssuerID: "fgn", IssueID: "fgn2033", Value: money.NGN(900_000), Rating: RatingAAA},
		{AssetClass: "FGN_BOND", IssuerID: "fgn", IssueID: "fgn2038", Value: money.NGN(900_000), Rating: RatingAAA},
		{AssetClass: "CORPORATE_DEBT", IssuerID: "mtn", IssueID: "mtn2029", Value: money.NGN(800_000), Rating: RatingAA},
		{AssetClass: "MMF", IssuerID: "stanbic", IssueID: "mmf-1", Value: money.NGN(700_000)},
	}
	if bs := CheckCompliance("II", nav, hs, fundIILimits); len(bs) != 0 {
		t.Fatalf("clean portfolio breached: %+v", bs)
	}
}

func TestCheckComplianceBreaches(t *testing.T) {
	nav := money.NGN(1_000_000)
	tests := []struct {
		name     string
		holdings []Holding
		rule     string
	}{
		{
			name: "variable income above 75% cap",
			holdings: []Holding{
				{AssetClass: "EQUITY", IssuerID: "a", IssueID: "a1", Value: money.NGN(400_000), VariableIncome: true},
				{AssetClass: "EQUITY", IssuerID: "b", IssueID: "b1", Value: money.NGN(360_000), VariableIncome: true},
				{AssetClass: "FGN_BOND", IssuerID: "fgn", IssueID: "f1", Value: money.NGN(240_000), Rating: RatingAAA},
			},
			rule: RuleVariableIncomeCap,
		},
		{
			name: "per-issuer >10%",
			holdings: []Holding{
				{AssetClass: "EQUITY", IssuerID: "dangote", IssueID: "d1", Value: money.NGN(60_000), VariableIncome: true},
				{AssetClass: "EQUITY", IssuerID: "dangote", IssueID: "d2", Value: money.NGN(50_000), VariableIncome: true},
				{AssetClass: "FGN_BOND", IssuerID: "fgn", IssueID: "f1", Value: money.NGN(890_000), Rating: RatingAAA},
			},
			rule: RulePerIssuer,
		},
		{
			name: "per-issue >5%",
			holdings: []Holding{
				{AssetClass: "CORPORATE_DEBT", IssuerID: "mtn", IssueID: "mtn2029", Value: money.NGN(60_000), Rating: RatingAA},
				{AssetClass: "FGN_BOND", IssuerID: "fgn", IssueID: "f1", Value: money.NGN(940_000), Rating: RatingAAA},
			},
			rule: RulePerIssue,
		},
		{
			name: "debt below minimum rating A",
			holdings: []Holding{
				{AssetClass: "CORPORATE_DEBT", IssuerID: "xyz", IssueID: "xyz2030", Value: money.NGN(10_000), Rating: RatingBB},
				{AssetClass: "FGN_BOND", IssuerID: "fgn", IssueID: "f1", Value: money.NGN(990_000), Rating: RatingAAA},
			},
			rule: RuleRatingGate,
		},
		{
			name: "BBB basket above 20%",
			holdings: []Holding{
				{AssetClass: "CORPORATE_DEBT", IssuerID: "c1", IssueID: "i1", Value: money.NGN(45_000), Rating: RatingBBB},
				{AssetClass: "CORPORATE_DEBT", IssuerID: "c2", IssueID: "i2", Value: money.NGN(45_000), Rating: RatingBBB},
				{AssetClass: "CORPORATE_DEBT", IssuerID: "c3", IssueID: "i3", Value: money.NGN(45_000), Rating: RatingBBB},
				{AssetClass: "CORPORATE_DEBT", IssuerID: "c4", IssueID: "i4", Value: money.NGN(45_000), Rating: RatingBBB},
				{AssetClass: "CORPORATE_DEBT", IssuerID: "c5", IssueID: "i5", Value: money.NGN(45_000), Rating: RatingBBB},
				{AssetClass: "FGN_BOND", IssuerID: "fgn", IssueID: "f1", Value: money.NGN(775_000), Rating: RatingAAA},
			},
			rule: RuleBBBBasket,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bs := CheckCompliance("II", nav, tt.holdings, fundIILimits)
			if !hasBreach(bs, tt.rule) {
				t.Fatalf("expected breach %s, got %+v", tt.rule, bs)
			}
		})
	}
}

func TestCheckComplianceBoundaryAtCap(t *testing.T) {
	nav := money.NGN(1_000_000)
	// Exactly 75% variable income is compliant (breach is strictly above).
	lim := fundIILimits
	lim.PerIssuerBps, lim.PerIssueBps = 9000, 9000 // isolate the VI cap
	hs := []Holding{
		{AssetClass: "EQUITY", IssuerID: "a", IssueID: "a1", Value: money.NGN(750_000), VariableIncome: true},
		{AssetClass: "FGN_BOND", IssuerID: "fgn", IssueID: "f1", Value: money.NGN(250_000), Rating: RatingAAA},
	}
	if bs := CheckCompliance("II", nav, hs, lim); len(bs) != 0 {
		t.Fatalf("at-cap portfolio breached: %+v", bs)
	}
}

func TestLimitsForFundUsesRulesPackCaps(t *testing.T) {
	caps := map[string]int64{"I": 9500, "III": 2000, "IV": 1000}
	if got := LimitsForFund("III", caps).VariableIncomeCapBps; got != 2000 {
		t.Fatalf("cap %d", got)
	}
	l := LimitsForFund("I", caps)
	if l.PerIssuerBps != DefaultPerIssuerBps || l.PerIssueBps != DefaultPerIssueBps ||
		l.MinDebtRating != RatingA || l.BBBBasketBps != DefaultBBBBasketBps {
		t.Fatalf("defaults %+v", l)
	}
}

func TestProhibitedRelatedPartyMatrix(t *testing.T) {
	pfa := PFAContext{
		PFAID:        "pfa-1",
		Shareholders: []string{"sh-1"},
		Directors:    []string{"dir-1"},
		Employees:    []string{"emp-1"},
		Spouses:      []string{"sp-1"},
		Affiliates:   []string{"aff-1"},
		Custodians:   []string{"cust-1"},
	}
	parties := []string{"pfa-1", "sh-1", "dir-1", "emp-1", "sp-1", "aff-1", "cust-1"}
	for _, p := range parties {
		if err := Prohibited(Transaction{TxID: "t", Buyer: "arm-1", Seller: p}, pfa); !errors.Is(err, ErrRelatedParty) {
			t.Fatalf("seller %s: %v", p, err)
		}
		if err := Prohibited(Transaction{TxID: "t", Buyer: p, Seller: "arm-1"}, pfa); !errors.Is(err, ErrRelatedParty) {
			t.Fatalf("buyer %s: %v", p, err)
		}
	}
}

func TestProhibitedMarginAndBorrowing(t *testing.T) {
	pfa := PFAContext{PFAID: "pfa-1"}
	if err := Prohibited(Transaction{TxID: "m", Buyer: "a", Seller: "b", Margin: true}, pfa); !errors.Is(err, ErrMarginTrading) {
		t.Fatalf("margin: %v", err)
	}
	if err := Prohibited(Transaction{TxID: "b", Buyer: "a", Seller: "b", Borrowed: true}, pfa); !errors.Is(err, ErrBorrowing) {
		t.Fatalf("borrowing: %v", err)
	}
	// Repo and securities lending are the permitted borrowing exceptions.
	if err := Prohibited(Transaction{TxID: "r", Buyer: "a", Seller: "b", Borrowed: true, Repo: true}, pfa); err != nil {
		t.Fatalf("repo: %v", err)
	}
	if err := Prohibited(Transaction{TxID: "s", Buyer: "a", Seller: "b", Borrowed: true, SecLending: true}, pfa); err != nil {
		t.Fatalf("sec-lending: %v", err)
	}
	// Plain arm's-length trade passes.
	if err := Prohibited(Transaction{TxID: "ok", Buyer: "a", Seller: "b"}, pfa); err != nil {
		t.Fatalf("clean: %v", err)
	}
}

func TestRatingDowngradeExitDays(t *testing.T) {
	if RatingDowngradeExitDays() != 10 {
		t.Fatalf("exit days %d", RatingDowngradeExitDays())
	}
}
