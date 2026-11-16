package onboarding

import (
	"errors"
	"testing"

	"github.com/munisp/pfaengine/internal/audit"
)

func newStore() (*Store, *audit.Store) {
	a := audit.NewStore()
	return NewStore(a), a
}

// advanceAll completes every required step in pipeline order.
func advanceAll(t *testing.T, s *Store, caseID string) {
	t.Helper()
	c, ok := s.Get(caseID)
	if !ok {
		t.Fatalf("case %s missing", caseID)
	}
	for _, st := range c.Steps {
		if !st.Required {
			continue
		}
		if err := s.Advance(caseID, st.Name); err != nil {
			t.Fatalf("advance %s: %v", st.Name, err)
		}
	}
}

// TestHappyPathEveryStakeholder walks each pipeline to its terminal state.
func TestHappyPathEveryStakeholder(t *testing.T) {
	cases := []struct {
		stakeholder Stakeholder
		wantSteps   []string // required, in order
		wantStatus  Status   // after all required steps complete
	}{
		{StakeholderMember, []string{"nin_verify", "bvn_validate", "consent_record", "liveness_enroll", "rsa_open"}, StatusActive},
		{StakeholderMicroMember, []string{"id_capture", "pin_request"}, StatusActive},
		{StakeholderEmployer, []string{"rc_validate", "glip_evidence", "mandate_form", "pssp_link"}, StatusApprovalPending},
		{StakeholderRetiree, []string{"identity_verify", "employment_history", "bank_details", "fund_iv_assignment", "first_payroll"}, StatusActive},
		{StakeholderStaff, []string{"rbac_role_assign", "totp_enroll"}, StatusApprovalPending},
		{StakeholderTenant, []string{"license_evidence", "capital_check", "branding_config", "admin_provision"}, StatusApprovalPending},
		{StakeholderNOK, []string{"identity_verify", "relationship_evidence", "member_link"}, StatusActive},
	}
	for _, tc := range cases {
		t.Run(string(tc.stakeholder), func(t *testing.T) {
			s, _ := newStore()
			c, err := s.Create("t1", tc.stakeholder, "maker-1", []Document{{ID: "d1", Kind: "form", Ref: "ev://d1"}})
			if err != nil {
				t.Fatal(err)
			}
			if c.Status != StatusDraft {
				t.Fatalf("initial status = %s, want DRAFT", c.Status)
			}
			var got []string
			for _, st := range c.Steps {
				if st.Required {
					got = append(got, st.Name)
				}
			}
			if len(got) != len(tc.wantSteps) {
				t.Fatalf("required steps = %v, want %v", got, tc.wantSteps)
			}
			for i := range got {
				if got[i] != tc.wantSteps[i] {
					t.Fatalf("step %d = %s, want %s", i, got[i], tc.wantSteps[i])
				}
			}
			advanceAll(t, s, c.ID)
			c, _ = s.Get(c.ID)
			if c.Status != tc.wantStatus {
				t.Fatalf("status = %s, want %s", c.Status, tc.wantStatus)
			}
			if ApprovalRequired(tc.stakeholder) {
				if err := s.Approve(c.ID, "checker-1"); err != nil {
					t.Fatalf("approve: %v", err)
				}
				c, _ = s.Get(c.ID)
				if c.Status != StatusActive || c.ApprovedBy != "checker-1" {
					t.Fatalf("after approve: status=%s approved_by=%q", c.Status, c.ApprovedBy)
				}
				// The approval marker step, when present, is stamped.
				for _, st := range c.Steps {
					if st.Name == "approval" && !st.Done {
						t.Fatalf("approval marker step not stamped")
					}
				}
			}
		})
	}
}

// TestMemberStatusProgression asserts DRAFT -> DOCS_PENDING -> KYC_PENDING
// -> ACTIVE for the member pipeline.
func TestMemberStatusProgression(t *testing.T) {
	s, _ := newStore()
	c, _ := s.Create("t1", StakeholderMember, "member-self", nil)
	want := []struct {
		step   string
		status Status
	}{
		{"nin_verify", StatusKYCPending},
		{"bvn_validate", StatusKYCPending},
		{"consent_record", StatusKYCPending},
		{"liveness_enroll", StatusDocsPending}, // only rsa_open (non-KYC) remains
		{"rsa_open", StatusActive},
	}
	for _, w := range want {
		if err := s.Advance(c.ID, w.step); err != nil {
			t.Fatalf("advance %s: %v", w.step, err)
		}
		got, _ := s.Get(c.ID)
		if got.Status != w.status {
			t.Fatalf("after %s status = %s, want %s", w.step, got.Status, w.status)
		}
	}
}

func TestAdvanceErrors(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T, s *Store) string // returns caseID
		step    string
		wantErr error
	}{
		{"unknown step", func(t *testing.T, s *Store) string {
			c, _ := s.Create("t1", StakeholderMember, "m", nil)
			return c.ID
		}, "nonsense", ErrUnknownStep},
		{"out-of-order step", func(t *testing.T, s *Store) string {
			c, _ := s.Create("t1", StakeholderMember, "m", nil)
			return c.ID
		}, "rsa_open", ErrStepOrder},
		{"optional step not advanceable", func(t *testing.T, s *Store) string {
			c, _ := s.Create("t1", StakeholderMicroMember, "m", nil)
			return c.ID
		}, "first_contribution_optional", ErrStepNotRequired},
		{"already-done step", func(t *testing.T, s *Store) string {
			c, _ := s.Create("t1", StakeholderMember, "m", nil)
			if err := s.Advance(c.ID, "nin_verify"); err != nil {
				t.Fatal(err)
			}
			return c.ID
		}, "nin_verify", ErrStepDone},
		{"terminal case rejects advance", func(t *testing.T, s *Store) string {
			c, _ := s.Create("t1", StakeholderNOK, "m", nil)
			advanceAll(t, s, c.ID) // NOK auto-approves to ACTIVE
			return c.ID
		}, "identity_verify", ErrCaseClosed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newStore()
			id := tc.setup(t, s)
			if err := s.Advance(id, tc.step); !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
	if s, _ := newStore(); true {
		if err := s.Advance("onb-999999", "nin_verify"); !errors.Is(err, ErrCaseNotFound) {
			t.Fatalf("unknown case: err = %v", err)
		}
	}
}

func TestUnknownStakeholder(t *testing.T) {
	s, _ := newStore()
	if _, err := s.Create("t1", Stakeholder("ALIEN"), "m", nil); !errors.Is(err, ErrUnknownStakeholder) {
		t.Fatalf("err = %v, want ErrUnknownStakeholder", err)
	}
}

func TestMakerChecker(t *testing.T) {
	s, _ := newStore()
	c, _ := s.Create("t1", StakeholderEmployer, "maker-1", nil)
	advanceAll(t, s, c.ID)

	// Approver cannot be the submitter.
	if err := s.Approve(c.ID, "maker-1"); !errors.Is(err, ErrMakerChecker) {
		t.Fatalf("same-approver: err = %v, want ErrMakerChecker", err)
	}
	if err := s.Approve(c.ID, ""); !errors.Is(err, ErrApproverRequired) {
		t.Fatalf("empty approver: err = %v, want ErrApproverRequired", err)
	}
	if err := s.Reject(c.ID, "maker-1", "bad docs"); !errors.Is(err, ErrMakerChecker) {
		t.Fatalf("same-rejecter: err = %v, want ErrMakerChecker", err)
	}
	if err := s.Reject(c.ID, "checker-1", ""); !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("empty reason: err = %v, want ErrReasonRequired", err)
	}
	if err := s.Approve(c.ID, "checker-1"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	// Double approve rejected: no longer pending.
	if err := s.Approve(c.ID, "checker-2"); !errors.Is(err, ErrNotApprovable) {
		t.Fatalf("re-approve: err = %v, want ErrNotApprovable", err)
	}
}

func TestAutoApproveVsApprovalRequired(t *testing.T) {
	s, _ := newStore()
	auto, _ := s.Create("t1", StakeholderMember, "m", nil)
	gated, _ := s.Create("t1", StakeholderStaff, "m", nil)
	advanceAll(t, s, auto.ID)
	advanceAll(t, s, gated.ID)

	a, _ := s.Get(auto.ID)
	g, _ := s.Get(gated.ID)
	if a.Status != StatusActive {
		t.Fatalf("auto-approve pipeline status = %s, want ACTIVE", a.Status)
	}
	if g.Status != StatusApprovalPending {
		t.Fatalf("gated pipeline status = %s, want APPROVAL_PENDING", g.Status)
	}
	// Approve on an auto-approved (already ACTIVE) case is rejected.
	if err := s.Approve(auto.ID, "anyone"); !errors.Is(err, ErrNotApprovable) {
		t.Fatalf("approve auto case: err = %v, want ErrNotApprovable", err)
	}
}

func TestRejectFlow(t *testing.T) {
	s, _ := newStore()
	c, _ := s.Create("t1", StakeholderTenant, "sales-1", nil)
	if err := s.Advance(c.ID, "license_evidence"); err != nil {
		t.Fatal(err)
	}
	if err := s.Reject(c.ID, "regulator-1", "licence number invalid"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(c.ID)
	if got.Status != StatusRejected || got.RejectReason != "licence number invalid" {
		t.Fatalf("status=%s reason=%q", got.Status, got.RejectReason)
	}
	if err := s.Advance(c.ID, "capital_check"); !errors.Is(err, ErrCaseClosed) {
		t.Fatalf("advance rejected case: err = %v", err)
	}
}

func TestListFilters(t *testing.T) {
	s, _ := newStore()
	m1, _ := s.Create("t1", StakeholderMember, "m", nil)
	_, _ = s.Create("t1", StakeholderEmployer, "m", nil)
	_, _ = s.Create("t2", StakeholderMember, "m", nil)
	advanceAll(t, s, m1.ID) // -> ACTIVE

	if n := len(s.List("t1", "", "")); n != 2 {
		t.Fatalf("tenant list = %d, want 2", n)
	}
	if n := len(s.List("t1", StakeholderMember, "")); n != 1 {
		t.Fatalf("stakeholder filter = %d, want 1", n)
	}
	if n := len(s.List("t1", "", StatusActive)); n != 1 {
		t.Fatalf("status filter = %d, want 1", n)
	}
	if n := len(s.List("t1", StakeholderEmployer, StatusActive)); n != 0 {
		t.Fatalf("combined filter = %d, want 0", n)
	}
}

// TestAuditEntriesWritten asserts every mutation lands in the audit chain
// (SC-02) and the chain verifies.
func TestAuditEntriesWritten(t *testing.T) {
	s, a := newStore()
	c, _ := s.Create("t1", StakeholderEmployer, "maker-1", []Document{{ID: "d1", Kind: "rc", Ref: "ev://rc"}})
	advanceAll(t, s, c.ID)
	if err := s.LinkRef(c.ID, "emp-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Approve(c.ID, "checker-1"); err != nil {
		t.Fatal(err)
	}

	want := map[string]int{
		"onboarding.created":        1,
		"onboarding.step.completed": 4,
		"onboarding.linked":         1,
		"onboarding.approved":       1,
	}
	counts := map[string]int{}
	for _, e := range s.audit.Events() {
		counts[e.Action]++
		if e.EntityType != "onboarding_case" || e.EntityID != c.ID {
			t.Fatalf("audit event targets %s/%s, want onboarding_case/%s", e.EntityType, e.EntityID, c.ID)
		}
		if e.TenantID != "t1" {
			t.Fatalf("audit tenant = %s, want t1", e.TenantID)
		}
	}
	for action, n := range want {
		if counts[action] != n {
			t.Fatalf("audit[%s] = %d, want %d (all: %v)", action, counts[action], n, counts)
		}
	}
	if err := a.Verify(); err != nil {
		t.Fatalf("chain verify: %v", err)
	}

	// Auto-approve pipelines also emit onboarding.auto_approved.
	s2, a2 := newStore()
	m, _ := s2.Create("t1", StakeholderMember, "m", nil)
	advanceAll(t, s2, m.ID)
	found := false
	for _, e := range a2.Events() {
		if e.Action == "onboarding.auto_approved" {
			found = true
		}
	}
	if !found {
		t.Fatalf("auto_approved audit entry missing")
	}
}

func TestLinkRefIdempotent(t *testing.T) {
	s, _ := newStore()
	c, _ := s.Create("t1", StakeholderMember, "m", nil)
	if err := s.LinkRef(c.ID, "prs-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkRef(c.ID, "emp-1"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(c.ID)
	if got.RefID != "prs-1" {
		t.Fatalf("RefID = %s, want prs-1 (first link wins)", got.RefID)
	}
	if err := s.LinkRef("onb-999999", "x"); !errors.Is(err, ErrCaseNotFound) {
		t.Fatalf("err = %v, want ErrCaseNotFound", err)
	}
}

func TestSuspend(t *testing.T) {
	s, _ := newStore()
	c, _ := s.Create("t1", StakeholderNOK, "m", nil)
	if err := s.Suspend(c.ID, "ops"); err == nil {
		t.Fatalf("suspend non-ACTIVE case must fail")
	}
	advanceAll(t, s, c.ID)
	if err := s.Suspend(c.ID, "ops"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(c.ID)
	if got.Status != StatusSuspended {
		t.Fatalf("status = %s, want SUSPENDED", got.Status)
	}
}
