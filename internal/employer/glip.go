// Package employer implements employer-side compliance artefacts.
// glip.go implements FR-515 / BR-26 (PRA 2014 s.4(5)): every employer must
// maintain a Group Life Insurance Policy (GLIP) with cover of at least 3x
// annual total emolument, and evidence of the GLIP plus three fiscal years
// of remittances is required for the PenCom Pension Clearance Certificate.
package employer

import (
	"errors"
	"fmt"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var (
	ErrCoverMultiple   = errors.New("employer: GLIP cover multiple must be >= 3x emolument")
	ErrCoverAmount     = errors.New("employer: GLIP cover amount below 3x annual total emolument")
	ErrPolicyExpired   = errors.New("employer: GLIP policy expired")
	ErrMissingPolicyNo = errors.New("employer: GLIP insurer/policy number missing")
)

// MinCoverMultipleX100 is the statutory minimum cover multiple (3x,
// expressed x100 to keep integer math).
const MinCoverMultipleX100 = 300

// ClearanceRemittanceYears is the number of fiscal years of remittance
// evidence required for the Pension Clearance Certificate.
const ClearanceRemittanceYears = 3

// GLIP is an employer's group life insurance policy record (FR-515).
type GLIP struct {
	EmployerID    string    `json:"employer_id"`
	Insurer       string    `json:"insurer"`
	PolicyNo      string    `json:"policy_no"`
	CoverMultiple int64     `json:"cover_multiple_x100"` // e.g. 300 = 3.00x
	Expiry        time.Time `json:"expiry"`
}

// Valid checks the policy: identifiable insurer/policy, cover multiple at
// least 3x (CoverMultiple >= 300), the actual cover amount at least 3x
// annual total emolument, and the policy unexpired at `at`.
func (g GLIP) Valid(annualEmolumentTotal, coverAmount money.Kobo, at time.Time) error {
	if g.Insurer == "" || g.PolicyNo == "" {
		return ErrMissingPolicyNo
	}
	if g.CoverMultiple < MinCoverMultipleX100 {
		return fmt.Errorf("%w (got %d.%02dx)", ErrCoverMultiple, g.CoverMultiple/100, g.CoverMultiple%100)
	}
	if coverAmount < annualEmolumentTotal*3 {
		return ErrCoverAmount
	}
	if !at.Before(g.Expiry) {
		return ErrPolicyExpired
	}
	return nil
}

// ClearanceEvidence returns the evidence items still missing for the
// PenCom Pension Clearance Certificate: the GLIP policy document itself
// and remittance evidence for ClearanceRemittanceYears fiscal years
// (remittanceYears is how many years of evidence are already on file).
func ClearanceEvidence(g GLIP, remittanceYears int) []string {
	var missing []string
	if g.Insurer == "" || g.PolicyNo == "" {
		missing = append(missing, "glip_policy_document")
	}
	for y := remittanceYears; y < ClearanceRemittanceYears; y++ {
		missing = append(missing, fmt.Sprintf("remittance_evidence_year_%d", y+1))
	}
	return missing
}
