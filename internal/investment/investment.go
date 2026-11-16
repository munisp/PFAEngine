// Package investment implements FR-510 / BR-16: the investment compliance
// engine for pension fund assets (Revised Regulation on Investment of
// Pension Fund Assets, ss.3,6,9). It enforces the 2025 per-fund
// variable-income caps from the rules pack (NFR-22), per-issuer and
// per-issue concentration limits, the minimum debt rating gate ('A') with
// the 20% BBB basket, and screens prohibited transactions (related
// parties, margin trading, borrowing except repo/sec-lending).
// All values are integer kobo and basis points — never floats (DA-03).
package investment

import (
	"errors"
	"fmt"

	"github.com/munisp/pfaengine/internal/money"
)

// Recognised credit ratings, best to worst; NR = not rated.
const (
	RatingAAA = "AAA"
	RatingAA  = "AA"
	RatingA   = "A"
	RatingBBB = "BBB"
	RatingBB  = "BB"
	RatingB   = "B"
	RatingNR  = "NR"
)

// ratingRank orders ratings for the minimum-rating gate (higher is better).
func ratingRank(r string) int {
	switch r {
	case RatingAAA:
		return 6
	case RatingAA:
		return 5
	case RatingA:
		return 4
	case RatingBBB:
		return 3
	case RatingBB:
		return 2
	case RatingB:
		return 1
	default: // NR and unknown
		return 0
	}
}

// Holding is one portfolio position for compliance checking.
type Holding struct {
	AssetClass     string     `json:"asset_class"` // e.g. EQUITY, FGN_BOND, CORPORATE_DEBT, MMF
	IssuerID       string     `json:"issuer_id"`
	IssueID        string     `json:"issue_id"`
	Value          money.Kobo `json:"value_kobo"`
	VariableIncome bool       `json:"variable_income"` // true for equities / variable-income securities
	Rating         string     `json:"rating"`          // one of Rating*; "" = not applicable
}

// Limits are the compliance parameters for one fund. VariableIncomeCapBps
// comes from the rules pack (rules/ng/pencom.json funds.*); the remaining
// defaults follow the Revised Investment Regulation ss.3/6/9.
type Limits struct {
	VariableIncomeCapBps int64  `json:"variable_income_cap_bps"` // per-fund cap (BR-16)
	PerIssuerBps         int64  `json:"per_issuer_bps"`          // max exposure to one issuer
	PerIssueBps          int64  `json:"per_issue_bps"`           // max exposure to one issue
	MinDebtRating        string `json:"min_debt_rating"`         // minimum rating for debt securities
	BBBBasketBps         int64  `json:"bbb_basket_bps"`          // max share of BBB-rated debt
}

// Defaults from the Revised Investment Regulation: 10% per issuer, 5% per
// issue, minimum debt rating 'A', and a 20% BBB basket.
const (
	DefaultPerIssuerBps  = 1000
	DefaultPerIssueBps   = 500
	DefaultMinDebtRating = RatingA
	DefaultBBBBasketBps  = 2000
)

// LimitsForFund builds Limits for a fund: the variable-income cap comes
// from the rules-pack caps map (fund code -> bps); the remaining limits
// use the regulation defaults.
func LimitsForFund(fundCode string, caps map[string]int64) Limits {
	return Limits{
		VariableIncomeCapBps: caps[fundCode],
		PerIssuerBps:         DefaultPerIssuerBps,
		PerIssueBps:          DefaultPerIssueBps,
		MinDebtRating:        DefaultMinDebtRating,
		BBBBasketBps:         DefaultBBBBasketBps,
	}
}

// Breach is one compliance rule violation.
type Breach struct {
	Rule   string `json:"rule"`
	Detail string `json:"detail"`
}

// Compliance rule identifiers.
const (
	RuleVariableIncomeCap = "VARIABLE_INCOME_CAP"
	RulePerIssuer         = "PER_ISSUER_LIMIT"
	RulePerIssue          = "PER_ISSUE_LIMIT"
	RuleRatingGate        = "RATING_GATE"
	RuleBBBBasket         = "BBB_BASKET"
)

// CheckCompliance evaluates a portfolio against the fund's limits and
// returns every breach found (empty slice = compliant). All comparisons
// use integer kobo and basis points.
func CheckCompliance(fundCode string, nav money.Kobo, hs []Holding, lim Limits) []Breach {
	var out []Breach
	if nav <= 0 {
		return out
	}
	var varIncome, bbb money.Kobo
	perIssuer := map[string]money.Kobo{}
	perIssue := map[string]money.Kobo{}
	for _, h := range hs {
		if h.Value < 0 {
			continue
		}
		if h.VariableIncome {
			varIncome += h.Value
		} else if h.Rating != "" && h.Rating != RatingNR {
			// Minimum-rating gate applies to rated debt securities.
			if ratingRank(h.Rating) < ratingRank(lim.MinDebtRating) {
				out = append(out, Breach{Rule: RuleRatingGate,
					Detail: fmt.Sprintf("fund %s: issue %s rated %s below minimum %s", fundCode, h.IssueID, h.Rating, lim.MinDebtRating)})
			}
			if h.Rating == RatingBBB {
				bbb += h.Value
			}
		}
		if h.IssuerID != "" {
			perIssuer[h.IssuerID] += h.Value
		}
		if h.IssueID != "" {
			perIssue[h.IssueID] += h.Value
		}
	}
	if cap := money.MulKobo(nav, lim.VariableIncomeCapBps, 10000); varIncome > cap {
		out = append(out, Breach{Rule: RuleVariableIncomeCap,
			Detail: fmt.Sprintf("fund %s: variable income %s exceeds cap %s (%d bps)", fundCode, varIncome.Naira(), cap.Naira(), lim.VariableIncomeCapBps)})
	}
	for id, v := range perIssuer {
		if cap := money.MulKobo(nav, lim.PerIssuerBps, 10000); v > cap {
			out = append(out, Breach{Rule: RulePerIssuer,
				Detail: fmt.Sprintf("fund %s: issuer %s exposure %s exceeds %s (%d bps)", fundCode, id, v.Naira(), cap.Naira(), lim.PerIssuerBps)})
		}
	}
	for id, v := range perIssue {
		if cap := money.MulKobo(nav, lim.PerIssueBps, 10000); v > cap {
			out = append(out, Breach{Rule: RulePerIssue,
				Detail: fmt.Sprintf("fund %s: issue %s exposure %s exceeds %s (%d bps)", fundCode, id, v.Naira(), cap.Naira(), lim.PerIssueBps)})
		}
	}
	if cap := money.MulKobo(nav, lim.BBBBasketBps, 10000); bbb > cap {
		out = append(out, Breach{Rule: RuleBBBBasket,
			Detail: fmt.Sprintf("fund %s: BBB basket %s exceeds 20%% (%s)", fundCode, bbb.Naira(), cap.Naira())})
	}
	return out
}

// ---------- Prohibited transactions (Inv. Reg. s.9) ----------

var (
	ErrRelatedParty  = errors.New("investment: transaction with a related party is prohibited")
	ErrMarginTrading = errors.New("investment: margin trading is prohibited")
	ErrBorrowing     = errors.New("investment: borrowing is prohibited except repo / securities lending")
)

// Transaction is a proposed investment transaction to screen.
type Transaction struct {
	TxID       string `json:"tx_id"`
	Buyer      string `json:"buyer"`
	Seller     string `json:"seller"`
	AssetClass string `json:"asset_class"`
	Margin     bool   `json:"margin"`      // bought on margin
	Borrowed   bool   `json:"borrowed"`    // funded by borrowing
	Repo       bool   `json:"repo"`        // repurchase agreement (borrowing exception)
	SecLending bool   `json:"sec_lending"` // securities lending (borrowing exception)
}

// PFAContext lists the PFA's related parties for the s.9 screen.
type PFAContext struct {
	PFAID        string   `json:"pfa_id"`
	Shareholders []string `json:"shareholders"`
	Directors    []string `json:"directors"`
	Employees    []string `json:"employees"`
	Spouses      []string `json:"spouses"`
	Affiliates   []string `json:"affiliates"`
	Custodians   []string `json:"custodians"`
}

func in(id string, list []string) bool {
	for _, x := range list {
		if x == id {
			return true
		}
	}
	return false
}

// RelatedParty reports whether id is the PFA itself or any related party.
func (p PFAContext) RelatedParty(id string) bool {
	return id != "" && (id == p.PFAID ||
		in(id, p.Shareholders) || in(id, p.Directors) || in(id, p.Employees) ||
		in(id, p.Spouses) || in(id, p.Affiliates) || in(id, p.Custodians))
}

// Prohibited screens a transaction against the s.9 prohibitions: no
// dealing with the PFA, its shareholders/directors/employees/spouses/
// affiliates or custodian; no margin trading; no borrowing except repo
// and securities lending.
func Prohibited(tx Transaction, pfa PFAContext) error {
	if tx.Margin {
		return fmt.Errorf("%w (tx %s)", ErrMarginTrading, tx.TxID)
	}
	if tx.Borrowed && !tx.Repo && !tx.SecLending {
		return fmt.Errorf("%w (tx %s)", ErrBorrowing, tx.TxID)
	}
	if pfa.RelatedParty(tx.Buyer) || pfa.RelatedParty(tx.Seller) {
		return fmt.Errorf("%w (tx %s)", ErrRelatedParty, tx.TxID)
	}
	return nil
}

// RatingDowngradeExitDays is the window (working days) within which a
// holding that has been downgraded below the minimum rating must be
// exited (Inv. Reg. s.6).
func RatingDowngradeExitDays() int { return 10 }
