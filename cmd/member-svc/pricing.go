package main

// Gap 10 handlers: tenant-facing pricing product surface — the published
// tier catalog, deterministic quotes, and effective-dated plan assignment.
// Plan mutation is staff-gated (X-Actor-Role: STAFF or ADMIN, matching the
// blob.go convention); catalog/quote reads are open to the tenant.

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/pricing"
)

// requireStaff gates staff/admin-only mutations on the X-Actor-Role header
// (the blob.go convention: STAFF or ADMIN pass, everything else is 403).
func requireStaff(w http.ResponseWriter, r *http.Request) bool {
	role := strings.ToUpper(strings.TrimSpace(r.Header.Get("X-Actor-Role")))
	if role != "STAFF" && role != "ADMIN" {
		httpsrv.WriteError(w, http.StatusForbidden, "FORBIDDEN",
			errors.New("staff or admin role required"))
		return false
	}
	return true
}

var errPricingUnconfigured = errors.New("pricing engine not configured")

// pricingCatalog handles GET /api/v1/pricing/catalog — the published tier
// catalog (defaults plus any deployment overrides).
func (d *deps) pricingCatalog(w http.ResponseWriter, r *http.Request) {
	if d.pricing == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "PRICING_UNCONFIGURED", errPricingUnconfigured)
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"tiers": d.pricing.Catalog()})
}

// pricingQuote handles POST /api/v1/pricing/quote — a deterministic,
// kobo-exact quote for the tenant's assigned (or default) tier.
func (d *deps) pricingQuote(w http.ResponseWriter, r *http.Request) {
	if d.pricing == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "PRICING_UNCONFIGURED", errPricingUnconfigured)
		return
	}
	var in struct {
		ActiveMembers int        `json:"active_members"`
		AUMKobo       money.Kobo `json:"aum_kobo"`
		Period        string     `json:"period"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_JSON", err)
		return
	}
	q, err := d.pricing.Quote(httpsrv.TenantOf(r), in.ActiveMembers, in.AUMKobo, in.Period)
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "QUOTE_REJECTED", err)
		return
	}
	httpsrv.WriteJSON(w, 200, q)
}

// pricingPlanGet handles GET /api/v1/pricing/plan — the tenant's plan in
// force today plus its full assignment history.
func (d *deps) pricingPlanGet(w http.ResponseWriter, r *http.Request) {
	if d.pricing == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "PRICING_UNCONFIGURED", errPricingUnconfigured)
		return
	}
	tenant := httpsrv.TenantOf(r)
	current, ok := d.pricing.GetPlan(tenant, time.Now().UTC().Format("2006-01-02"))
	out := map[string]any{
		"current":     nil,
		"assignments": d.pricing.Assignments(tenant),
	}
	if ok {
		out["current"] = current
	}
	httpsrv.WriteJSON(w, 200, out)
}

// pricingPlanPut handles PUT /api/v1/pricing/plan — staff-only assignment
// of a catalog tier, effective-dated and audited.
func (d *deps) pricingPlanPut(w http.ResponseWriter, r *http.Request) {
	if d.pricing == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "PRICING_UNCONFIGURED", errPricingUnconfigured)
		return
	}
	if !requireStaff(w, r) {
		return
	}
	var in struct {
		TierCode      string `json:"tier_code"`
		EffectiveFrom string `json:"effective_from"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_JSON", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	a, err := d.pricing.SetPlan(tenant, in.TierCode, in.EffectiveFrom, actorOf(r), time.Now())
	switch {
	case errors.Is(err, pricing.ErrUnknownTier):
		httpsrv.WriteError(w, http.StatusNotFound, "TIER_NOT_FOUND", err)
		return
	case err != nil:
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "PLAN_REJECTED", err)
		return
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: actorOf(r),
		Action: "pricing.plan_assigned", EntityType: "pricing_assignment", EntityID: a.ID,
		After: map[string]any{"tier_code": a.TierCode, "effective_from": a.EffectiveFrom}})
	httpsrv.WriteJSON(w, 200, a)
}
