// Package death implements FR-512 / BR-20: death-benefit claims. The RSA
// balance of a deceased member is paid to beneficiaries under a valid Will
// admitted to probate or a Letter of Administration (PRA 2014 s.8).
// Claims require death-certificate/burial-warrant documentation and NOK
// KYC, and payout is maker-checker enforced with an audit trail via the
// durable workflow (internal/workflow DeathClaimWorkflow).
package death

import (
	"errors"
	"fmt"
	"time"
)

// DocType is the legal basis for the claim.
type DocType int

const (
	// WillProbate — valid Will admitted to probate.
	WillProbate DocType = iota
	// LetterOfAdmin — Letter of Administration (intestate).
	LetterOfAdmin
)

// ClaimStatus tracks the maker-checker lifecycle.
type ClaimStatus string

const (
	StatusSubmitted    ClaimStatus = "SUBMITTED"
	StatusDocsVerified ClaimStatus = "DOCS_VERIFIED"
	StatusApproved     ClaimStatus = "APPROVED" // ready for payout
)

var (
	ErrDocsNotVerified = errors.New("death: documents and NOK KYC must be verified before approval")
	ErrMakerChecker    = errors.New("death: approver must differ from submitter (maker-checker)")
	ErrAlreadyVerified = errors.New("death: documents already verified")
	ErrBadDocType      = errors.New("death: unknown document type")
)

// Claim is a death-benefit claim against a deceased member's RSA.
type Claim struct {
	RSA          string      `json:"rsa"`
	DeceasedName string      `json:"deceased_name"`
	DocType      DocType     `json:"doc_type"`
	DocsVerified bool        `json:"docs_verified"`
	NOKKYCPassed bool        `json:"nok_kyc_passed"`
	Status       ClaimStatus `json:"status"`
	SubmittedBy  string      `json:"submitted_by"`
	ApprovedBy   string      `json:"approved_by,omitempty"`
	SubmittedAt  time.Time   `json:"submitted_at"`
}

// NewClaim opens a claim in SUBMITTED status.
func NewClaim(rsa, deceasedName string, dt DocType, submittedBy string, at time.Time) (*Claim, error) {
	if dt != WillProbate && dt != LetterOfAdmin {
		return nil, ErrBadDocType
	}
	return &Claim{
		RSA: rsa, DeceasedName: deceasedName, DocType: dt,
		Status: StatusSubmitted, SubmittedBy: submittedBy, SubmittedAt: at,
	}, nil
}

// RequiredDocs lists the documents a claim of type dt must carry
// (BR-20): death certificate, burial warrant and NOK details, plus the
// Will admitted to probate or the Letter of Administration.
func RequiredDocs(dt DocType) []string {
	base := []string{"death_certificate", "burial_warrant", "nok_details"}
	switch dt {
	case WillProbate:
		return append(base, "will_admitted_to_probate")
	case LetterOfAdmin:
		return append(base, "letter_of_administration")
	}
	return base
}

// VerifyDocs marks the claim's documentation (and NOK KYC) verified.
// nokKYCPassed records the NOK identity-check outcome.
func (c *Claim) VerifyDocs(by string, nokKYCPassed bool) error {
	if c.DocsVerified {
		return ErrAlreadyVerified
	}
	c.DocsVerified = true
	c.NOKKYCPassed = nokKYCPassed
	if nokKYCPassed {
		c.Status = StatusDocsVerified
	}
	return nil
}

// Approve applies maker-checker: the approver must differ from the
// submitter, and documents plus NOK KYC must be verified first.
func (c *Claim) Approve(approver string) error {
	if approver == "" || approver == c.SubmittedBy {
		return fmt.Errorf("%w: %q", ErrMakerChecker, approver)
	}
	if !c.DocsVerified || !c.NOKKYCPassed {
		return ErrDocsNotVerified
	}
	c.ApprovedBy = approver
	c.Status = StatusApproved
	return nil
}
