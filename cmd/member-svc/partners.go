package main

// Gap 10 handlers: the organizational distribution-partner network (banks,
// telcos, fintechs, aggregators) — onboarding and lifecycle (staff-gated),
// member-facing referral attribution, and revenue-share settlement.
// Distinct from /api/v1/agents (person-level field agents).

import (
	"errors"
	"net/http"
	"time"

	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/partners"
)

var errPartnersUnconfigured = errors.New("partners engine not configured")

// partnersOnboard handles POST /api/v1/partners — staff-only onboarding of
// an institutional partner (starts PENDING until approved).
func (d *deps) partnersOnboard(w http.ResponseWriter, r *http.Request) {
	if d.partners == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "PARTNERS_UNCONFIGURED", errPartnersUnconfigured)
		return
	}
	if !requireStaff(w, r) {
		return
	}
	var in struct {
		Name         string `json:"name"`
		Type         string `json:"type"`
		ReferralCode string `json:"referral_code"`
		RevShareBps  int64  `json:"rev_share_bps"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_JSON", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	p, err := d.partners.Onboard(tenant, in.Name, partners.Type(in.Type), in.ReferralCode, in.RevShareBps, actorOf(r), time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "PARTNER_REJECTED", err)
		return
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: actorOf(r),
		Action: "partners.onboarded", EntityType: "partner", EntityID: p.ID,
		After: map[string]any{"name": p.Name, "type": p.Type, "referral_code": p.ReferralCode, "rev_share_bps": p.RevShareBps}})
	httpsrv.WriteJSON(w, 201, p)
}

// partnersList handles GET /api/v1/partners?status= — tenant partner list.
func (d *deps) partnersList(w http.ResponseWriter, r *http.Request) {
	if d.partners == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "PARTNERS_UNCONFIGURED", errPartnersUnconfigured)
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{
		"partners": d.partners.Partners(httpsrv.TenantOf(r), partners.Status(r.URL.Query().Get("status"))),
	})
}

// partnersApprove handles POST /api/v1/partners/{id}/approve — staff-only
// PENDING → ACTIVE (or reinstatement from SUSPENDED).
func (d *deps) partnersApprove(w http.ResponseWriter, r *http.Request) {
	if d.partners == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "PARTNERS_UNCONFIGURED", errPartnersUnconfigured)
		return
	}
	if !requireStaff(w, r) {
		return
	}
	tenant := httpsrv.TenantOf(r)
	p, err := d.partners.Approve(tenant, r.PathValue("id"), actorOf(r), time.Now())
	switch {
	case errors.Is(err, partners.ErrNoPartner):
		httpsrv.WriteError(w, http.StatusNotFound, "NOT_FOUND", err)
		return
	case err != nil:
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "PARTNER_REJECTED", err)
		return
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: actorOf(r),
		Action: "partners.approved", EntityType: "partner", EntityID: p.ID, After: nil})
	httpsrv.WriteJSON(w, 200, p)
}

// partnersSuspend handles POST /api/v1/partners/{id}/suspend — staff-only;
// blocks new attributions and accrual while the partner is under review.
func (d *deps) partnersSuspend(w http.ResponseWriter, r *http.Request) {
	if d.partners == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "PARTNERS_UNCONFIGURED", errPartnersUnconfigured)
		return
	}
	if !requireStaff(w, r) {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_JSON", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	p, err := d.partners.Suspend(tenant, r.PathValue("id"), actorOf(r), in.Reason, time.Now())
	switch {
	case errors.Is(err, partners.ErrNoPartner):
		httpsrv.WriteError(w, http.StatusNotFound, "NOT_FOUND", err)
		return
	case err != nil:
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "PARTNER_REJECTED", err)
		return
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: actorOf(r),
		Action: "partners.suspended", EntityType: "partner", EntityID: p.ID,
		After: map[string]any{"reason": p.SuspendReason}})
	httpsrv.WriteJSON(w, 200, p)
}

// partnersSettlement handles GET /api/v1/partners/{id}/settlement?period=
// — the itemised revenue-share settlement for one period, with the
// partner's lifetime balance.
func (d *deps) partnersSettlement(w http.ResponseWriter, r *http.Request) {
	if d.partners == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "PARTNERS_UNCONFIGURED", errPartnersUnconfigured)
		return
	}
	tenant := httpsrv.TenantOf(r)
	partnerID := r.PathValue("id")
	rep, err := d.partners.SettlementReport(tenant, partnerID, r.URL.Query().Get("period"), time.Now())
	switch {
	case errors.Is(err, partners.ErrNoPartner):
		httpsrv.WriteError(w, http.StatusNotFound, "NOT_FOUND", err)
		return
	case err != nil:
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "SETTLEMENT_REJECTED", err)
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{
		"settlement": rep,
		"balance":    d.partners.PartnerBalance(tenant, partnerID),
	})
}

// partnersAttribute handles POST /api/v1/partners/attribute — member-facing
// referral attribution. First write wins; repeat submissions are idempotent.
func (d *deps) partnersAttribute(w http.ResponseWriter, r *http.Request) {
	if d.partners == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "PARTNERS_UNCONFIGURED", errPartnersUnconfigured)
		return
	}
	var in struct {
		MemberID     string `json:"member_id"`
		ReferralCode string `json:"referral_code"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_JSON", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	a, err := d.partners.AttributeJoin(tenant, in.MemberID, in.ReferralCode, time.Now())
	switch {
	case errors.Is(err, partners.ErrUnknownCode):
		httpsrv.WriteError(w, http.StatusNotFound, "REFERRAL_CODE_UNKNOWN", err)
		return
	case err != nil:
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "ATTRIBUTION_REJECTED", err)
		return
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: actorOf(r),
		Action: "partners.attributed", EntityType: "partner_attribution", EntityID: a.ID,
		After: map[string]any{"member_id": a.MemberID, "partner_id": a.PartnerID, "referral_code": a.ReferralCode}})
	httpsrv.WriteJSON(w, 201, a)
}

// partnersAccrue handles POST /api/v1/partners/accruals — staff-only
// revenue-share accrual hook for contribution posting. Unattributed
// members return attributed=false (most members join directly).
func (d *deps) partnersAccrue(w http.ResponseWriter, r *http.Request) {
	if d.partners == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "PARTNERS_UNCONFIGURED", errPartnersUnconfigured)
		return
	}
	if !requireStaff(w, r) {
		return
	}
	var in struct {
		MemberID         string     `json:"member_id"`
		ContributionKobo money.Kobo `json:"contribution_kobo"`
		Period           string     `json:"period"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_JSON", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	ac, attributed, err := d.partners.AccrueContribution(tenant, in.MemberID, in.ContributionKobo, in.Period, time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "ACCRUAL_REJECTED", err)
		return
	}
	if !attributed {
		httpsrv.WriteJSON(w, 200, map[string]any{"attributed": false})
		return
	}
	httpsrv.WriteJSON(w, 201, ac)
}
