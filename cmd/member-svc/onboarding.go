// Onboarding endpoints (FR-520): the member-svc slice of the unified
// onboarding framework. Person-facing pipelines (MEMBER, MICRO_MEMBER,
// RETIREE, NOK) are served here; KYC steps run through the existing kyc
// facade and activation side-effects reuse the create-member internals,
// the micro-pension account type and the retiree register.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/kyc"
	"github.com/munisp/pfaengine/internal/micropension"
	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/onboarding"
)

// onboardPayload carries the per-case submission data the step handlers
// (KYC facade calls) and the activation hooks need.
type onboardPayload struct {
	FullName           string     `json:"full_name"`
	DOB                string     `json:"dob"`
	Phone              string     `json:"phone"`
	Email              string     `json:"email"`
	NIN                string     `json:"nin"`
	BVN                string     `json:"bvn,omitempty"`
	RSAID              string     `json:"rsa_id,omitempty"`               // retiree/NOK: linked member RSA
	MonthlyPensionKobo money.Kobo `json:"monthly_pension_kobo,omitempty"` // retiree
	BankRef            string     `json:"bank_ref,omitempty"`             // retiree
}

// personFacing lists the pipelines whose steps members complete here.
// Employer/staff/tenant cases are advanced by their owning services
// (employer-portal, identity).
var personFacing = map[onboarding.Stakeholder]bool{
	onboarding.StakeholderMember:      true,
	onboarding.StakeholderMicroMember: true,
	onboarding.StakeholderRetiree:     true,
	onboarding.StakeholderNOK:         true,
}

// createOnboardingCase handles POST /api/v1/onboarding: opens a DRAFT
// onboarding case for a person-facing stakeholder.
func (d *deps) createOnboardingCase(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Stakeholder string `json:"stakeholder"`
		SubmittedBy string `json:"submitted_by"`
		onboardPayload
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	sh := onboarding.Stakeholder(strings.ToUpper(in.Stakeholder))
	if !personFacing[sh] {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST",
			errors.New("stakeholder must be one of MEMBER, MICRO_MEMBER, RETIREE, NOK"))
		return
	}
	if sh == onboarding.StakeholderMember && (in.FullName == "" || in.NIN == "") {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST",
			errors.New("full_name and nin are required for MEMBER onboarding"))
		return
	}
	submittedBy := strings.TrimSpace(in.SubmittedBy)
	if submittedBy == "" {
		submittedBy = strings.TrimSpace(r.Header.Get("X-Actor"))
	}
	if submittedBy == "" {
		submittedBy = "member-self"
	}
	c, err := d.onb.Create(r.Context(), httpsrv.TenantOf(r), sh, submittedBy, nil)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, onboarding.ErrUnknownStakeholder) {
			status = http.StatusBadRequest
		}
		httpsrv.WriteError(w, status, "CREATE_FAILED", err)
		return
	}
	d.onbMu.Lock()
	d.onbPayloads[c.ID] = in.onboardPayload
	d.onbMu.Unlock()
	httpsrv.WriteJSON(w, http.StatusCreated, c)
}

// getOnboardingCase handles GET /api/v1/onboarding/{id}.
func (d *deps) getOnboardingCase(w http.ResponseWriter, r *http.Request) {
	c, ok := d.onb.Get(r.Context(), httpsrv.TenantOf(r), r.PathValue("id"))
	if !ok {
		httpsrv.WriteError(w, http.StatusNotFound, "NOT_FOUND", errors.New("onboarding case not found"))
		return
	}
	httpsrv.WriteJSON(w, http.StatusOK, c)
}

// completeOnboardingStep handles POST /api/v1/onboarding/{id}/steps/{step}.
// KYC steps (nin_verify, bvn_validate, consent_record, liveness_enroll)
// call the kyc facade before the state machine advances; when the last
// required step completes and the pipeline auto-approves, the activation
// hook provisions the linked entity.
func (d *deps) completeOnboardingStep(w http.ResponseWriter, r *http.Request) {
	id, step := r.PathValue("id"), r.PathValue("step")
	tenant := httpsrv.TenantOf(r)
	ctx := r.Context()
	c, ok := d.onb.Get(ctx, tenant, id)
	if !ok {
		httpsrv.WriteError(w, http.StatusNotFound, "NOT_FOUND", errors.New("onboarding case not found"))
		return
	}
	if !personFacing[c.Stakeholder] {
		httpsrv.WriteError(w, http.StatusForbidden, "WRONG_SERVICE",
			errors.New("this pipeline is advanced by its owning service"))
		return
	}
	var body struct {
		SelfieRef string `json:"selfie_ref"`
		Consent   bool   `json:"consent"`
	}
	if r.Body != nil {
		data, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if len(data) > 0 {
			if err := json.Unmarshal(data, &body); err != nil {
				httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
				return
			}
		}
	}
	payload := d.payload(id)
	if c.Stakeholder == onboarding.StakeholderMember {
		if !d.runKYCStep(w, tenant, c.ID, step, payload, body.SelfieRef, body.Consent) {
			return
		}
	}
	if err := d.onb.Advance(ctx, tenant, id, step); err != nil {
		writeOnboardingError(w, err)
		return
	}
	c, _ = d.onb.Get(ctx, tenant, id)
	d.maybeActivate(ctx, tenant, c)
	c, _ = d.onb.Get(ctx, tenant, id)
	httpsrv.WriteJSON(w, http.StatusOK, c)
}

// runKYCStep executes the KYC-facade side of a member pipeline step,
// writing the error response and returning false on failure.
func (d *deps) runKYCStep(w http.ResponseWriter, tenant, caseID, step string, p onboardPayload, selfieRef string, consent bool) bool {
	switch step {
	case "nin_verify":
		if _, _, err := d.kyc.VerifyNIN(tenant, caseID, p.NIN, p.FullName, p.DOB); err != nil {
			httpsrv.WriteError(w, http.StatusUnprocessableEntity, "KYC_FAILED", err)
			return false
		}
	case "bvn_validate":
		if p.BVN == "" {
			httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST",
				errors.New("bvn is required for the bvn_validate step"))
			return false
		}
		if _, _, err := d.kyc.ValidateBVN(tenant, caseID, p.BVN, p.FullName, p.DOB, p.Phone); err != nil {
			httpsrv.WriteError(w, http.StatusUnprocessableEntity, "KYC_FAILED", err)
			return false
		}
	case "consent_record":
		if !consent {
			httpsrv.WriteError(w, http.StatusBadRequest, "CONSENT_REQUIRED",
				errors.New("explicit consent is required (NDPA, FR-106)"))
			return false
		}
		d.kyc.RecordConsent(tenant, caseID, "ONBOARDING", "onboarding-v1", true)
	case "liveness_enroll":
		if selfieRef == "" {
			httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", errors.New("selfie_ref is required"))
			return false
		}
		chk, err := d.kyc.SubmitLiveness(tenant, caseID, kyc.KindEnroll, selfieRef)
		if err != nil {
			httpsrv.WriteError(w, http.StatusUnprocessableEntity, "LIVENESS_FAILED", err)
			return false
		}
		if chk.Status != kyc.StatusPassed {
			httpsrv.WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error": "liveness not passed", "code": "LIVENESS_FAILED",
				"status": string(chk.Status), "score": chk.Score,
			})
			return false
		}
	}
	return true
}

// approveOnboarding handles POST /api/v1/onboarding/{id}/approve (staff
// only): the approver identity comes from X-Actor and must differ from the
// case submitter (maker-checker, §6.2).
func (d *deps) approveOnboarding(w http.ResponseWriter, r *http.Request) {
	approver := strings.TrimSpace(r.Header.Get("X-Actor"))
	if approver == "" {
		httpsrv.WriteError(w, http.StatusForbidden, "STAFF_REQUIRED",
			errors.New("X-Actor staff identity header required"))
		return
	}
	id := r.PathValue("id")
	tenant := httpsrv.TenantOf(r)
	ctx := r.Context()
	if err := d.onb.Approve(ctx, tenant, id, approver); err != nil {
		writeOnboardingError(w, err)
		return
	}
	c, _ := d.onb.Get(ctx, tenant, id)
	d.maybeActivate(ctx, tenant, c)
	c, _ = d.onb.Get(ctx, tenant, id)
	httpsrv.WriteJSON(w, http.StatusOK, c)
}

// rejectOnboarding handles POST /api/v1/onboarding/{id}/reject (staff
// only, maker-checker) with a mandatory reason.
func (d *deps) rejectOnboarding(w http.ResponseWriter, r *http.Request) {
	approver := strings.TrimSpace(r.Header.Get("X-Actor"))
	if approver == "" {
		httpsrv.WriteError(w, http.StatusForbidden, "STAFF_REQUIRED",
			errors.New("X-Actor staff identity header required"))
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	id := r.PathValue("id")
	tenant := httpsrv.TenantOf(r)
	if err := d.onb.Reject(r.Context(), tenant, id, approver, strings.TrimSpace(in.Reason)); err != nil {
		writeOnboardingError(w, err)
		return
	}
	c, _ := d.onb.Get(r.Context(), tenant, id)
	httpsrv.WriteJSON(w, http.StatusOK, c)
}

// maybeActivate runs the activation hook exactly once: when the case
// reaches ACTIVE and no entity is linked yet, it provisions the linked
// entity and binds it via LinkRef.
func (d *deps) maybeActivate(ctx context.Context, tenant string, c *onboarding.Case) {
	if c.Status != onboarding.StatusActive || c.RefID != "" {
		return
	}
	payload := d.payload(c.ID)
	refID := d.activate(ctx, tenant, c, payload)
	if refID != "" {
		_ = d.onb.LinkRef(ctx, tenant, c.ID, refID)
	}
}

// activate provisions the linked entity for an ACTIVE case and returns
// its ID. MEMBER reuses the create-member internals (registry + ledger
// accounts); MICRO_MEMBER additionally opens the micro-pension account;
// RETIREE registers in the retiree register (FR-403).
func (d *deps) activate(ctx context.Context, tenant string, c *onboarding.Case, p onboardPayload) string {
	switch c.Stakeholder {
	case onboarding.StakeholderMember:
		person := d.members.CreatePerson(tenant, p.FullName, p.DOB, p.Phone, p.Email)
		if err := d.prefs.LinkPhone(ctx, tenant, p.Phone, person.ID); err != nil {
			log.Printf("member-svc: prefs phone link for %s: %v", person.ID, err)
		}
		d.openMemberAccounts(tenant, person.ID)
		d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "member.onboarded",
			EntityType: "person", EntityID: person.ID,
			After: map[string]any{"onboarding_case": c.ID, "via": "onboarding-framework"}})
		return person.ID
	case onboarding.StakeholderMicroMember:
		person := d.members.CreatePerson(tenant, p.FullName, p.DOB, p.Phone, p.Email)
		if err := d.prefs.LinkPhone(ctx, tenant, p.Phone, person.ID); err != nil {
			log.Printf("member-svc: prefs phone link for %s: %v", person.ID, err)
		}
		d.microMu.Lock()
		d.micro[person.ID] = &micropension.Account{}
		d.microMu.Unlock()
		d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "micropension.account.opened",
			EntityType: "micropension_account", EntityID: person.ID,
			After: map[string]any{"onboarding_case": c.ID}})
		return person.ID
	case onboarding.StakeholderRetiree:
		rsaID := p.RSAID
		if rsaID == "" {
			person := d.members.CreatePerson(tenant, p.FullName, p.DOB, p.Phone, p.Email)
			d.openMemberAccounts(tenant, person.ID)
			rsaID = person.ID
		}
		d.retirees.Enroll(tenant, rsaID, p.MonthlyPensionKobo, p.BankRef, time.Now().UTC())
		d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "retiree.enrolled",
			EntityType: "retiree", EntityID: rsaID,
			After: map[string]any{"onboarding_case": c.ID}})
		return rsaID
	case onboarding.StakeholderNOK:
		return p.RSAID // linked member RSA; evidence lives on the case
	default:
		return ""
	}
}

func (d *deps) payload(caseID string) onboardPayload {
	d.onbMu.Lock()
	defer d.onbMu.Unlock()
	return d.onbPayloads[caseID]
}

// writeOnboardingError maps onboarding domain errors onto HTTP statuses.
// Domain conflicts enumerate explicitly; anything else (e.g. a persistence
// failure from the Tranche 14 write-through repo) is a 500.
func writeOnboardingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, onboarding.ErrCaseNotFound):
		httpsrv.WriteError(w, http.StatusNotFound, "NOT_FOUND", err)
	case errors.Is(err, onboarding.ErrMakerChecker):
		httpsrv.WriteError(w, http.StatusForbidden, "MAKER_CHECKER", err)
	case errors.Is(err, onboarding.ErrApproverRequired):
		httpsrv.WriteError(w, http.StatusBadRequest, "APPROVER_REQUIRED", err)
	case errors.Is(err, onboarding.ErrReasonRequired):
		httpsrv.WriteError(w, http.StatusBadRequest, "REASON_REQUIRED", err)
	case errors.Is(err, onboarding.ErrUnknownStep):
		httpsrv.WriteError(w, http.StatusBadRequest, "UNKNOWN_STEP", err)
	case errors.Is(err, onboarding.ErrStepNotRequired), errors.Is(err, onboarding.ErrStepDone),
		errors.Is(err, onboarding.ErrStepOrder), errors.Is(err, onboarding.ErrCaseClosed),
		errors.Is(err, onboarding.ErrNotApprovable):
		httpsrv.WriteError(w, http.StatusConflict, "STEP_CONFLICT", err)
	default: // persistence or other infrastructure failure
		httpsrv.WriteError(w, http.StatusInternalServerError, "ONBOARDING_FAILED", err)
	}
}
