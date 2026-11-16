// Package legacy implements BR-23 / BR-24 / BR-28: support for legacy
// scheme types under the PRA 2014 transitional provisions — Approved
// Existing Schemes (AES, pre-June-2004), Closed Pension Fund
// Administrators (CPFA), Additional Benefit Schemes (ABS), and
// recognition of pre-2004 NSITF contributions as a distinct RSA benefit
// type.
package legacy

import (
	"errors"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

// SchemeKind identifies a legacy scheme type.
type SchemeKind int

const (
	// AES — Approved Existing Scheme (pre-June-2004, PRA s.51).
	AES SchemeKind = iota
	// CPFA — Closed Pension Fund Administrator scheme.
	CPFA
	// ABS — Additional Benefit Scheme (employer top-up).
	ABS
	// NSITF — pre-2004 NSITF contributions (BR-28).
	NSITF
)

func (k SchemeKind) String() string {
	switch k {
	case AES:
		return "AES"
	case CPFA:
		return "CPFA"
	case ABS:
		return "ABS"
	case NSITF:
		return "NSITF"
	}
	return "UNKNOWN"
}

// Scheme captures the compliance attributes of a legacy scheme.
type Scheme struct {
	Kind              SchemeKind `json:"kind"`
	FullyFunded       bool       `json:"fully_funded"`
	CustodianLicensed bool       `json:"custodian_licensed"`
	PFALicensed       bool       `json:"pfa_licensed"`
	Segregated        bool       `json:"segregated"`          // assets segregated from employer's
	TrustDeedApproved bool       `json:"trust_deed_approved"` // PenCom-approved Trust Deed (ABS)
}

// Compliant returns the list of missing statutory conditions (empty =
// compliant). AES/CPFA require full funding, segregated assets, and
// licensed custodian + PFA (PRA s.51); ABS additionally requires a
// PenCom-approved Trust Deed (BR-24).
func (s Scheme) Compliant() []string {
	var missing []string
	if !s.FullyFunded {
		missing = append(missing, "scheme not fully funded")
	}
	if !s.Segregated {
		missing = append(missing, "assets not segregated from employer")
	}
	if !s.CustodianLicensed {
		missing = append(missing, "custodian not licensed")
	}
	if !s.PFALicensed {
		missing = append(missing, "PFA not licensed")
	}
	if s.Kind == ABS && !s.TrustDeedApproved {
		missing = append(missing, "trust deed not approved by PenCom")
	}
	return missing
}

// FeeModel returns the fee model for the scheme kind: AES and ABS charge
// an income-based 7.5% fee, CPFA fees are capped at 1.1%, and RSA funds
// use asset-based fees (BR-13 schedule).
func FeeModel(k SchemeKind) string {
	switch k {
	case AES, ABS:
		return "income-7.5pct"
	case CPFA:
		return "capped-1.1pct"
	default:
		return "asset-based"
	}
}

// PerformanceMethod returns the performance reporting method: non-unitised
// funds (AES) use time-weighted returns; unitised funds use unit-price
// movement (BR-23).
func PerformanceMethod(k SchemeKind) string {
	if k == AES {
		return "TWR"
	}
	return "unit-price"
}

var ErrNegativeCredit = errors.New("legacy: NSITF credit amount must be non-negative")

// NSITFCredit is a recognized pre-2004 NSITF contribution credited to a
// member's RSA as a distinct benefit type (BR-28).
type NSITFCredit struct {
	MemberID  string     `json:"member_id"`
	Amount    money.Kobo `json:"amount_kobo"`
	PeriodEnd time.Time  `json:"period_end"` // end of the contribution period recognized
}

// NewNSITFCredit validates and builds an NSITF credit (non-negative).
func NewNSITFCredit(memberID string, amount money.Kobo, periodEnd time.Time) (NSITFCredit, error) {
	if amount < 0 {
		return NSITFCredit{}, ErrNegativeCredit
	}
	return NSITFCredit{MemberID: memberID, Amount: amount, PeriodEnd: periodEnd}, nil
}
