// Package annuity implements SCM-11: the annuity marketplace. Retirees
// compare programmed-withdrawal alternatives from NAICOM-licensed life
// insurers; purchases follow maker-checker before the premium instruction
// goes to the custodian. Integer kobo throughout (DA-03).
package annuity

import (
	"errors"
	"sort"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var (
	ErrUnlicensedInsurer    = errors.New("annuity: insurer is not NAICOM-licensed")
	ErrQuoteExpired         = errors.New("annuity: quote has expired")
	ErrPremiumNonPositive   = errors.New("annuity: premium must be > 0")
	ErrPremiumExceedsRSA    = errors.New("annuity: premium exceeds RSA balance")
	ErrBadTransition        = errors.New("annuity: invalid purchase state transition")
	ErrMakerCheckerConflict = errors.New("annuity: approver must differ from submitter (maker-checker)")
)

// Quote is one insurer's annuity offer per ₦100,000 of premium.
type Quote struct {
	InsurerID           string
	InsurerName         string
	NAICOMLicensed      bool
	AnnualPayoutPer100k money.Kobo // annual payout per ₦100k premium
	GuaranteedYears     int
	ExpiresAt           time.Time
}

// RankQuotes filters out unlicensed insurers and expired quotes, then
// sorts the remainder by payout, highest first (stable: ties keep the
// input order).
func RankQuotes(qs []Quote, at time.Time) []Quote {
	var kept []Quote
	for _, q := range qs {
		if !q.NAICOMLicensed || !q.ExpiresAt.After(at) {
			continue
		}
		kept = append(kept, q)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		return kept[i].AnnualPayoutPer100k > kept[j].AnnualPayoutPer100k
	})
	return kept
}

// ValidatePremium checks the premium is positive and within the RSA
// balance available for annuitisation.
func ValidatePremium(premium, rsaBalance money.Kobo) error {
	if premium <= 0 {
		return ErrPremiumNonPositive
	}
	if premium > rsaBalance {
		return ErrPremiumExceedsRSA
	}
	return nil
}

// PurchaseStatus is the purchase state machine state.
type PurchaseStatus int

const (
	Requested PurchaseStatus = iota
	Approved
	Instructed
)

func (s PurchaseStatus) String() string {
	switch s {
	case Requested:
		return "REQUESTED"
	case Approved:
		return "APPROVED"
	case Instructed:
		return "INSTRUCTED"
	}
	return "UNKNOWN"
}

// Purchase is one annuity purchase instruction (SCM-11).
type Purchase struct {
	RSA         string
	InsurerID   string
	Premium     money.Kobo
	Status      PurchaseStatus
	SubmittedBy string
	ApprovedBy  string
}

// NewPurchase creates a purchase in Requested state after premium
// validation; submitter is recorded for maker-checker.
func NewPurchase(rsa, insurerID string, premium, rsaBalance money.Kobo, submitter string) (Purchase, error) {
	if err := ValidatePremium(premium, rsaBalance); err != nil {
		return Purchase{}, err
	}
	return Purchase{
		RSA: rsa, InsurerID: insurerID, Premium: premium,
		Status: Requested, SubmittedBy: submitter,
	}, nil
}

// Approve moves Requested → Approved. The approver must be a different
// officer from the submitter (maker-checker, FR-401 pattern).
func (p *Purchase) Approve(approver string) error {
	if p.Status != Requested {
		return ErrBadTransition
	}
	if approver == "" || approver == p.SubmittedBy {
		return ErrMakerCheckerConflict
	}
	p.Status = Approved
	p.ApprovedBy = approver
	return nil
}

// Instruct moves Approved → Instructed: the premium instruction is
// released to the custodian for payment to the insurer.
func (p *Purchase) Instruct() error {
	if p.Status != Approved {
		return ErrBadTransition
	}
	p.Status = Instructed
	return nil
}
