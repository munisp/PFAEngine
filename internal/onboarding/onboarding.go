// Package onboarding implements FR-520: one unified, end-to-end onboarding
// state machine for every platform stakeholder (member, micro-pension
// member, employer, retiree, staff, tenant/PFA, next-of-kin). A Case walks
// DRAFT -> DOCS_PENDING -> KYC_PENDING -> APPROVAL_PENDING -> ACTIVE (or
// REJECTED / SUSPENDED). Each stakeholder has a pipeline template of
// ordered steps (Pipeline); steps complete strictly in order and only
// required steps gate completion. EMPLOYER/STAFF/TENANT cases require a
// maker-checker approval (ApprovalRequired); the person-facing pipelines
// auto-approve to ACTIVE when the last required step completes. Every
// mutation is appended to the hash-chained audit store (SC-02).
package onboarding

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/audit"
)

var (
	ErrCaseNotFound       = errors.New("onboarding: case not found")
	ErrUnknownStakeholder = errors.New("onboarding: unknown stakeholder")
	ErrUnknownStep        = errors.New("onboarding: step not in pipeline")
	ErrStepNotRequired    = errors.New("onboarding: step is optional; only required steps are advanced")
	ErrStepDone           = errors.New("onboarding: step already completed")
	ErrStepOrder          = errors.New("onboarding: earlier required steps must complete first")
	ErrCaseClosed         = errors.New("onboarding: case is in a terminal status")
	ErrNotApprovable      = errors.New("onboarding: case is not pending approval")
	ErrMakerChecker       = errors.New("onboarding: approver must differ from the submitter (maker-checker)")
	ErrApproverRequired   = errors.New("onboarding: approver identity required")
	ErrReasonRequired     = errors.New("onboarding: reject reason required")
)

// Status is the case lifecycle state.
type Status string

const (
	StatusDraft           Status = "DRAFT"
	StatusDocsPending     Status = "DOCS_PENDING"
	StatusKYCPending      Status = "KYC_PENDING"
	StatusApprovalPending Status = "APPROVAL_PENDING"
	StatusActive          Status = "ACTIVE"
	StatusRejected        Status = "REJECTED"
	StatusSuspended       Status = "SUSPENDED"
)

// Stakeholder identifies which pipeline a case follows.
type Stakeholder string

const (
	StakeholderMember      Stakeholder = "MEMBER"
	StakeholderMicroMember Stakeholder = "MICRO_MEMBER"
	StakeholderEmployer    Stakeholder = "EMPLOYER"
	StakeholderRetiree     Stakeholder = "RETIREE"
	StakeholderStaff       Stakeholder = "STAFF"
	StakeholderTenant      Stakeholder = "TENANT"
	StakeholderNOK         Stakeholder = "NOK" // next-of-kin / beneficiary
)

// Step is one pipeline item. Optional steps (Required=false) are
// informational markers — e.g. the maker-checker "approval" step that
// Approve() stamps — and cannot be advanced directly.
type Step struct {
	Name     string    `json:"name"`
	Required bool      `json:"required"`
	Done     bool      `json:"done"`
	At       time.Time `json:"at,omitempty"`
}

// Document is an uploaded evidence artefact attached to the case.
type Document struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"` // e.g. nin_slip, glip_policy, pencom_licence
	Ref        string    `json:"ref"`  // storage reference
	UploadedAt time.Time `json:"uploaded_at"`
}

// Case is one stakeholder onboarding instance.
type Case struct {
	ID           string      `json:"id"`
	TenantID     string      `json:"tenant_id"`
	Stakeholder  Stakeholder `json:"stakeholder"`
	Status       Status      `json:"status"`
	RefID        string      `json:"ref_id,omitempty"` // linked entity (member, employer, tenant...)
	Steps        []Step      `json:"steps"`
	Docs         []Document  `json:"docs,omitempty"`
	RejectReason string      `json:"reject_reason,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
	SubmittedBy  string      `json:"submitted_by"`
	ApprovedBy   string      `json:"approved_by,omitempty"`
}

// kycSteps are identity-evidence steps: while only these remain, the case
// sits in KYC_PENDING rather than DOCS_PENDING.
var kycSteps = map[string]bool{
	"nin_verify": true, "bvn_validate": true, "consent_record": true,
	"liveness_enroll": true, "identity_verify": true, "id_capture": true,
}

// Pipeline returns the ordered step template for a stakeholder (nil for an
// unknown stakeholder).
func Pipeline(s Stakeholder) []Step {
	req := func(names ...string) []Step {
		out := make([]Step, 0, len(names))
		for _, n := range names {
			out = append(out, Step{Name: n, Required: true})
		}
		return out
	}
	switch s {
	case StakeholderMember:
		return req("nin_verify", "bvn_validate", "consent_record", "liveness_enroll", "rsa_open")
	case StakeholderMicroMember:
		// id_capture accepts NIN OR voter/driver/passport evidence.
		steps := req("id_capture", "pin_request")
		return append(steps, Step{Name: "first_contribution_optional", Required: false})
	case StakeholderEmployer:
		// glip_evidence enforces the FR-515 3x-emolument GLIP rule at the
		// service layer; "approval" is stamped by Approve (maker-checker).
		steps := req("rc_validate", "glip_evidence", "mandate_form", "pssp_link")
		return append(steps, Step{Name: "approval", Required: false})
	case StakeholderRetiree:
		return req("identity_verify", "employment_history", "bank_details", "fund_iv_assignment", "first_payroll")
	case StakeholderStaff:
		steps := req("rbac_role_assign", "totp_enroll")
		return append(steps, Step{Name: "approval", Required: false})
	case StakeholderTenant:
		return req("license_evidence", "capital_check", "branding_config", "admin_provision")
	case StakeholderNOK:
		return req("identity_verify", "relationship_evidence", "member_link")
	default:
		return nil
	}
}

// ApprovalRequired reports whether the stakeholder's case needs a
// maker-checker approval after the required steps complete; person-facing
// pipelines auto-approve straight to ACTIVE.
func ApprovalRequired(s Stakeholder) bool {
	switch s {
	case StakeholderEmployer, StakeholderStaff, StakeholderTenant:
		return true
	default:
		return false
	}
}

// Store is a thread-safe in-memory case store. Every mutation appends to
// the audit chain (SC-02).
type Store struct {
	mu    sync.Mutex
	audit *audit.Store
	cases map[string]*Case
	seq   int
}

// NewStore returns a Store writing audit records to a (must be non-nil).
func NewStore(a *audit.Store) *Store {
	return &Store{audit: a, cases: map[string]*Case{}}
}

// Create opens a case in DRAFT with the stakeholder's pipeline.
func (s *Store) Create(tenantID string, sh Stakeholder, submittedBy string, docs []Document) (*Case, error) {
	steps := Pipeline(sh)
	if steps == nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownStakeholder, sh)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	now := time.Now().UTC()
	c := &Case{
		ID: fmt.Sprintf("onb-%06d", s.seq), TenantID: tenantID, Stakeholder: sh,
		Status: StatusDraft, Steps: steps, Docs: append([]Document{}, docs...),
		CreatedAt: now, UpdatedAt: now, SubmittedBy: submittedBy,
	}
	s.cases[c.ID] = c
	s.appendLocked(audit.Record{TenantID: tenantID, Actor: submittedBy,
		Action: "onboarding.created", EntityType: "onboarding_case", EntityID: c.ID,
		After: map[string]any{"stakeholder": string(sh), "docs": len(c.Docs)}})
	return copyCase(c), nil
}

// Get returns a copy of the case.
func (s *Store) Get(id string) (*Case, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[id]
	if !ok {
		return nil, false
	}
	return copyCase(c), true
}

// List returns the tenant's cases, optionally filtered by stakeholder
// and/or status (empty value = wildcard).
func (s *Store) List(tenantID string, sh Stakeholder, st Status) []*Case {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []*Case{}
	for _, c := range s.cases {
		if c.TenantID != tenantID {
			continue
		}
		if sh != "" && c.Stakeholder != sh {
			continue
		}
		if st != "" && c.Status != st {
			continue
		}
		out = append(out, copyCase(c))
	}
	return out
}

// Advance completes a required pipeline step. Steps complete strictly in
// pipeline order: an earlier incomplete required step rejects the advance
// (ErrStepOrder). Completing the last required step moves the case to
// APPROVAL_PENDING (maker-checker stakeholders) or ACTIVE (auto-approve).
func (s *Store) Advance(caseID, step string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[caseID]
	if !ok {
		return ErrCaseNotFound
	}
	if terminal(c.Status) {
		return ErrCaseClosed
	}
	idx := -1
	for i := range c.Steps {
		if c.Steps[i].Name == step {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("%w: %q", ErrUnknownStep, step)
	}
	if !c.Steps[idx].Required {
		return fmt.Errorf("%w: %q", ErrStepNotRequired, step)
	}
	if c.Steps[idx].Done {
		return fmt.Errorf("%w: %q", ErrStepDone, step)
	}
	for i := 0; i < idx; i++ {
		if c.Steps[i].Required && !c.Steps[i].Done {
			return fmt.Errorf("%w: %q before %q", ErrStepOrder, c.Steps[i].Name, step)
		}
	}
	now := time.Now().UTC()
	c.Steps[idx].Done = true
	c.Steps[idx].At = now
	c.UpdatedAt = now
	prev := c.Status
	c.Status = nextStatus(c)
	s.appendLocked(audit.Record{TenantID: c.TenantID, Actor: c.SubmittedBy,
		Action: "onboarding.step.completed", EntityType: "onboarding_case", EntityID: c.ID,
		Before: map[string]any{"status": string(prev)},
		After:  map[string]any{"step": step, "status": string(c.Status)}})
	if c.Status == StatusActive && prev != StatusActive {
		s.appendLocked(audit.Record{TenantID: c.TenantID, Actor: c.SubmittedBy,
			Action: "onboarding.auto_approved", EntityType: "onboarding_case", EntityID: c.ID,
			After: map[string]any{"stakeholder": string(c.Stakeholder)}})
	}
	return nil
}

// Approve moves an APPROVAL_PENDING case to ACTIVE under maker-checker
// control: the approver must differ from the submitter. An "approval"
// marker step, when present in the pipeline, is stamped done.
func (s *Store) Approve(caseID, approver string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[caseID]
	if !ok {
		return ErrCaseNotFound
	}
	if approver == "" {
		return ErrApproverRequired
	}
	if approver == c.SubmittedBy {
		return ErrMakerChecker
	}
	if c.Status != StatusApprovalPending {
		return fmt.Errorf("%w (status %s)", ErrNotApprovable, c.Status)
	}
	now := time.Now().UTC()
	c.Status = StatusActive
	c.ApprovedBy = approver
	c.UpdatedAt = now
	for i := range c.Steps {
		if c.Steps[i].Name == "approval" {
			c.Steps[i].Done = true
			c.Steps[i].At = now
		}
	}
	s.appendLocked(audit.Record{TenantID: c.TenantID, Actor: approver,
		Action: "onboarding.approved", EntityType: "onboarding_case", EntityID: c.ID,
		After: map[string]any{"stakeholder": string(c.Stakeholder), "approved_by": approver}})
	return nil
}

// Reject moves a non-terminal case to REJECTED under maker-checker control
// with a mandatory reason.
func (s *Store) Reject(caseID, approver, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[caseID]
	if !ok {
		return ErrCaseNotFound
	}
	if approver == "" {
		return ErrApproverRequired
	}
	if approver == c.SubmittedBy {
		return ErrMakerChecker
	}
	if reason == "" {
		return ErrReasonRequired
	}
	if terminal(c.Status) {
		return ErrCaseClosed
	}
	c.Status = StatusRejected
	c.RejectReason = reason
	c.UpdatedAt = time.Now().UTC()
	s.appendLocked(audit.Record{TenantID: c.TenantID, Actor: approver,
		Action: "onboarding.rejected", EntityType: "onboarding_case", EntityID: c.ID,
		After: map[string]any{"reason": reason}})
	return nil
}

// Suspend moves an ACTIVE case to SUSPENDED (e.g. compliance breach).
func (s *Store) Suspend(caseID, actor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[caseID]
	if !ok {
		return ErrCaseNotFound
	}
	if c.Status != StatusActive {
		return fmt.Errorf("onboarding: only ACTIVE cases can be suspended (status %s)", c.Status)
	}
	c.Status = StatusSuspended
	c.UpdatedAt = time.Now().UTC()
	s.appendLocked(audit.Record{TenantID: c.TenantID, Actor: actor,
		Action: "onboarding.suspended", EntityType: "onboarding_case", EntityID: c.ID})
	return nil
}

// LinkRef binds the case to the provisioned entity (member ID, employer
// ID, tenant ID...) once activation side-effects run. Idempotent: a
// non-empty RefID is never overwritten.
func (s *Store) LinkRef(caseID, refID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[caseID]
	if !ok {
		return ErrCaseNotFound
	}
	if c.RefID != "" {
		return nil
	}
	c.RefID = refID
	c.UpdatedAt = time.Now().UTC()
	s.appendLocked(audit.Record{TenantID: c.TenantID, Actor: actorOr(c.SubmittedBy),
		Action: "onboarding.linked", EntityType: "onboarding_case", EntityID: c.ID,
		After: map[string]any{"ref_id": refID}})
	return nil
}

func actorOr(fallback string) string {
	if fallback == "" {
		return "system"
	}
	return fallback
}

func terminal(st Status) bool {
	return st == StatusActive || st == StatusRejected || st == StatusSuspended
}

// nextStatus recomputes the in-flight status after a step completes:
// all required steps done -> APPROVAL_PENDING (maker-checker) or ACTIVE
// (auto-approve); any KYC-type step still outstanding -> KYC_PENDING;
// otherwise DOCS_PENDING.
func nextStatus(c *Case) Status {
	allDone := true
	kycLeft := false
	for _, st := range c.Steps {
		if !st.Required || st.Done {
			continue
		}
		allDone = false
		if kycSteps[st.Name] {
			kycLeft = true
		}
	}
	if allDone {
		if ApprovalRequired(c.Stakeholder) {
			return StatusApprovalPending
		}
		return StatusActive
	}
	if kycLeft {
		return StatusKYCPending
	}
	return StatusDocsPending
}

func (s *Store) appendLocked(r audit.Record) {
	if s.audit != nil {
		s.audit.Append(r)
	}
}

func copyCase(c *Case) *Case {
	cp := *c
	cp.Steps = append([]Step{}, c.Steps...)
	cp.Docs = append([]Document{}, c.Docs...)
	return &cp
}
