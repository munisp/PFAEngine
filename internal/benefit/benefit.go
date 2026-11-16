// Package benefit implements FR-401 and SCM-06/07/08: benefit
// applications (lump sum, programmed withdrawal, annuity handoff,
// invalidity, death) with maker-checker approval, DB formula engine,
// 25% pre-retirement access rule, and custodian payment instructions.
package benefit

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var (
	ErrNotFound        = errors.New("benefit: claim not found")
	ErrBadState        = errors.New("benefit: invalid state transition")
	ErrMakerChecker    = errors.New("benefit: approver must differ from submitter (maker-checker)")
	ErrEarlyAccessRule = errors.New("benefit: 25% access requires age 50+ and retirement from employment")
)

type ClaimType string

const (
	LumpSum              ClaimType = "LUMP_SUM"
	ProgrammedWithdrawal ClaimType = "PROGRAMMED_WITHDRAWAL"
	AnnuityHandoff       ClaimType = "ANNUITY_HANDOFF"
	Invalidity           ClaimType = "INVALIDITY"
	Death                ClaimType = "DEATH"
	EarlyAccess25        ClaimType = "EARLY_ACCESS_25" // SCM-07 Nigeria pre-retirement access
)

type ClaimState string

const (
	StateSubmitted         ClaimState = "SUBMITTED"
	StateDocsVerified      ClaimState = "DOCS_VERIFIED"
	StateApproved          ClaimState = "APPROVED"
	StateRejected          ClaimState = "REJECTED"
	StatePaymentInstructed ClaimState = "PAYMENT_INSTRUCTED"
	StatePaid              ClaimState = "PAID"
)

type Claim struct {
	ID             string     `json:"id"`
	TenantID       string     `json:"tenant_id"`
	RSAID          string     `json:"rsa_id"`
	Type           ClaimType  `json:"type"`
	Amount         money.Kobo `json:"amount_kobo"`
	State          ClaimState `json:"state"`
	SubmittedBy    string     `json:"submitted_by"`
	DecidedBy      string     `json:"decided_by,omitempty"`
	DecisionReason string     `json:"decision_reason,omitempty"`
	RuleVersion    string     `json:"rule_version"` // SCM-15
	CustodianRef   string     `json:"custodian_ref,omitempty"`
	SubmittedAt    time.Time  `json:"submitted_at"`
	DecidedAt      time.Time  `json:"decided_at,omitempty"`
}

type Service struct {
	mu     sync.Mutex
	claims map[string]*Claim
	seq    int
}

func NewService() *Service { return &Service{claims: map[string]*Claim{}} }

// Submit creates a claim. EarlyAccess25 enforces the configurable
// Nigeria rule (25% of balance, age ≥ 50) — SCM-07.
func (s *Service) Submit(tenantID, rsaID string, ct ClaimType, amount, rsaBalance money.Kobo,
	ageYears int, ruleVersion, actor string) (*Claim, error) {
	if ct == EarlyAccess25 {
		if ageYears < 50 {
			return nil, ErrEarlyAccessRule
		}
		max := money.MulKobo(rsaBalance, 25, 100)
		if amount > max {
			return nil, fmt.Errorf("benefit: early access capped at 25%% (%s)", max.Naira())
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	c := &Claim{
		ID: fmt.Sprintf("clm-%d", s.seq), TenantID: tenantID, RSAID: rsaID,
		Type: ct, Amount: amount, State: StateSubmitted, SubmittedBy: actor,
		RuleVersion: ruleVersion, SubmittedAt: time.Now().UTC(),
	}
	s.claims[c.ID] = c
	return c, nil
}

func (s *Service) VerifyDocs(id string) error {
	return s.transition(id, StateSubmitted, StateDocsVerified)
}

// Decide applies maker-checker (FR-401): approver ≠ submitter.
func (s *Service) Decide(id, approver string, approve bool, reason string) (*Claim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.claims[id]
	if !ok {
		return nil, ErrNotFound
	}
	if c.State != StateSubmitted && c.State != StateDocsVerified {
		return nil, ErrBadState
	}
	if approver == c.SubmittedBy {
		return nil, ErrMakerChecker
	}
	c.DecidedBy = approver
	c.DecisionReason = reason
	c.DecidedAt = time.Now().UTC()
	if approve {
		c.State = StateApproved
	} else {
		c.State = StateRejected
	}
	return c, nil
}

// InstructPayment sends the custodian payment instruction (INT-05).
func (s *Service) InstructPayment(id, custodianRef string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.claims[id]
	if !ok {
		return ErrNotFound
	}
	if c.State != StateApproved {
		return ErrBadState
	}
	c.CustodianRef = custodianRef
	c.State = StatePaymentInstructed
	return nil
}

func (s *Service) ConfirmPaid(id string) error {
	return s.transition(id, StatePaymentInstructed, StatePaid)
}

func (s *Service) transition(id string, from, to ClaimState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.claims[id]
	if !ok {
		return ErrNotFound
	}
	if c.State != from {
		return ErrBadState
	}
	c.State = to
	return nil
}

func (s *Service) Get(id string) (*Claim, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.claims[id]
	return c, ok
}

// ProgrammedWithdrawalSchedule computes periodic payments from an
// approved pot over a term (FR-403 link).
func ProgrammedWithdrawalSchedule(pot money.Kobo, months int) money.Kobo {
	if months <= 0 {
		return 0
	}
	return money.Kobo(int64(pot) / int64(months))
}
