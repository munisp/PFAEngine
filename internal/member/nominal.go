package member

// Nominal RSA support (FR-516 / BR-27, PRA 2014 s.11(5)): when an employee
// has not opened an RSA within 6 months of assuming duty, the employer
// requests the PFA to open a nominal RSA; the member later claims/merges
// it on registration.

import (
	"errors"
	"time"
)

// NominalWaitMonths is the employment period after which a nominal RSA is
// due if the employee still has no RSA.
const NominalWaitMonths = 6

var ErrAlreadyClaimed = errors.New("member: nominal RSA already claimed")

// NominalDue reports whether a nominal RSA is due: the employee assumed
// duty more than NominalWaitMonths ago and still has no RSA.
func NominalDue(employedSince, asOf time.Time, hasRSA bool) bool {
	if hasRSA {
		return false
	}
	return !asOf.Before(employedSince.AddDate(0, NominalWaitMonths, 0))
}

// NominalRSA is a PFA-opened account awaiting the member's claim.
type NominalRSA struct {
	PIN        string    `json:"pin"`
	EmployerID string    `json:"employer_id"`
	OpenedAt   time.Time `json:"opened_at"`
	Claimed    bool      `json:"claimed"`
	ClaimedBy  string    `json:"claimed_by,omitempty"` // member/person ID on claim
}

// Claim marks the nominal RSA claimed by a registering member (FR-516
// claim/merge flow). Returns ErrAlreadyClaimed on a second claim.
func (n *NominalRSA) Claim(memberID string) error {
	if n.Claimed {
		return ErrAlreadyClaimed
	}
	n.Claimed = true
	n.ClaimedBy = memberID
	return nil
}
