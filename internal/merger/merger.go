// Package merger implements BR-10: M&A book-merge tooling for PFA
// acquisitions — roster re-tenanting with PIN uniqueness enforcement,
// ordered merge planning, and a pre/post balance proof.
package merger

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

// MemberRecord is a member row in a book being merged.
type MemberRecord struct {
	ID       string     `json:"id"`  // re-tenanted on merge
	PIN      string     `json:"pin"` // PenCom RSA PIN — immutable across the merger
	FundCode string     `json:"fund_code"`
	Balance  money.Kobo `json:"balance_kobo"`
}

// ErrPINConflict is returned when two members in the merged book share a
// PIN; the message lists every conflicting PIN.
var ErrPINConflict = fmt.Errorf("merger: PIN conflict")

// MergeRoster re-tenants target members into the acquirer: every ID is
// prefixed with the acquirer ID (preserving the source ID after a "/"),
// while PINs and balances move unchanged. A PIN appearing more than once
// in the merged book aborts the merge with ErrPINConflict listing all
// conflicting PINs.
func MergeRoster(acquirerID, targetID string, members []MemberRecord) ([]MemberRecord, error) {
	seen := map[string]bool{} // PINs already in the merged book
	conflicts := map[string]bool{}
	out := make([]MemberRecord, 0, len(members))
	for _, m := range members {
		if seen[m.PIN] {
			conflicts[m.PIN] = true
			continue
		}
		seen[m.PIN] = true
		m.ID = acquirerID + "/" + m.ID
		out = append(out, m)
	}
	if len(conflicts) > 0 {
		pins := make([]string, 0, len(conflicts))
		for p := range conflicts {
			pins = append(pins, p)
		}
		sort.Strings(pins)
		return nil, fmt.Errorf("%w: %s (target %s)", ErrPINConflict, strings.Join(pins, ", "), targetID)
	}
	return out, nil
}

// MergePlan is the ordered cutover plan for acquiring target's book.
type MergePlan struct {
	Acquirer    string    `json:"acquirer"`
	Target      string    `json:"target"`
	CutoverDate time.Time `json:"cutover_date"`
	Steps       []string  `json:"steps"`
}

// PlanMerge returns the regulator-mandated ordered steps: member data
// recapture, trial migration, balance proof, PenCom approval, cutover,
// and legacy-system decommission.
func PlanMerge(acquirer, target string, cutover time.Time) MergePlan {
	return MergePlan{
		Acquirer:    acquirer,
		Target:      target,
		CutoverDate: cutover,
		Steps: []string{
			"data-recapture",
			"trial-migration",
			"balance-proof",
			"pencom-approval",
			"cutover",
			"decommission",
		},
	}
}

// ErrBalanceProof is returned when the post-merge book does not reproduce
// the pre-merge book; the message lists every per-member mismatch.
var ErrBalanceProof = fmt.Errorf("merger: balance proof failed")

// BalanceProof verifies that the merged book reproduces the pre-merge
// book exactly: the same members (by PIN) with the same balances, and an
// equal total. Mismatches (missing, added, or revalued members) are
// listed in the error.
func BalanceProof(before, after []MemberRecord) error {
	var problems []string
	preByPIN := map[string]money.Kobo{}
	var preTotal money.Kobo
	for _, m := range before {
		preByPIN[m.PIN] = m.Balance
		preTotal += m.Balance
	}
	var postTotal money.Kobo
	for _, m := range after {
		postTotal += m.Balance
		pre, ok := preByPIN[m.PIN]
		if !ok {
			problems = append(problems, fmt.Sprintf("PIN %s added in post-merge book (%s)", m.PIN, m.Balance.Naira()))
			continue
		}
		if pre != m.Balance {
			problems = append(problems, fmt.Sprintf("PIN %s balance %s → %s", m.PIN, pre.Naira(), m.Balance.Naira()))
		}
		delete(preByPIN, m.PIN)
	}
	for pin := range preByPIN {
		problems = append(problems, fmt.Sprintf("PIN %s missing from post-merge book", pin))
	}
	if preTotal != postTotal {
		problems = append(problems, fmt.Sprintf("total %s → %s", preTotal.Naira(), postTotal.Naira()))
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%w: %s", ErrBalanceProof, strings.Join(problems, "; "))
	}
	return nil
}
