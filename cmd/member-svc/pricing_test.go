package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/munisp/pfaengine/internal/pricing"
)

func newPricingDeps() *deps {
	d := newTestDeps()
	d.pricing = pricing.NewEngine()
	return d
}

func TestPricingCatalog(t *testing.T) {
	d := newPricingDeps()
	rr := do(t, d.handler(), http.MethodGet, "/api/v1/pricing/catalog", "t1", "")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var out struct {
		Tiers []pricing.Tier `json:"tiers"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Tiers) != 4 || out.Tiers[0].Code != "STARTER" {
		t.Fatalf("catalog = %+v", out.Tiers)
	}
}

func TestPricingQuote(t *testing.T) {
	d := newPricingDeps()
	// Assign GROWTH (₦45/member, 2 bps AUM, 5,000 included, ₦50 overage).
	rr := doHdr(t, d.handler(), http.MethodPut, "/api/v1/pricing/plan", "t1",
		`{"tier_code":"GROWTH","effective_from":"2025-01-01"}`, staffHdr)
	if rr.Code != 200 {
		t.Fatalf("plan put status = %d body=%s", rr.Code, rr.Body.String())
	}
	rr = do(t, d.handler(), http.MethodPost, "/api/v1/pricing/quote", "t1",
		`{"active_members":7200,"aum_kobo":12000000000,"period":"2025-06"}`)
	if rr.Code != 200 {
		t.Fatalf("quote status = %d body=%s", rr.Code, rr.Body.String())
	}
	var q pricing.Quote
	if err := json.NewDecoder(rr.Body).Decode(&q); err != nil {
		t.Fatal(err)
	}
	if q.TierCode != "GROWTH" || q.Total != 4500*7200+5000*2200+200_000 {
		t.Fatalf("quote = %+v", q)
	}
	// Bad period rejected.
	rr = do(t, d.handler(), http.MethodPost, "/api/v1/pricing/quote", "t1",
		`{"active_members":1,"aum_kobo":0,"period":"June"}`)
	if rr.Code != 422 {
		t.Fatalf("bad period status = %d, want 422", rr.Code)
	}
	// Unassigned tenant quotes on the assumed default tier.
	rr = do(t, d.handler(), http.MethodPost, "/api/v1/pricing/quote", "t9",
		`{"active_members":10,"aum_kobo":0,"period":"2025-06"}`)
	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	if err := json.NewDecoder(rr.Body).Decode(&q); err != nil {
		t.Fatal(err)
	}
	if !q.Assumed || q.TierCode != "STARTER" {
		t.Fatalf("assumed quote = %+v", q)
	}
}

func TestPricingPlanPutStaffGating(t *testing.T) {
	d := newPricingDeps()
	body := `{"tier_code":"SCALE","effective_from":"2025-01-01"}`
	// No role header → 403.
	rr := do(t, d.handler(), http.MethodPut, "/api/v1/pricing/plan", "t1", body)
	if rr.Code != 403 {
		t.Fatalf("no-role status = %d, want 403", rr.Code)
	}
	// MEMBER role → 403.
	rr = doTenant(t, d.pricingPlanPut, http.MethodPut, "/api/v1/pricing/plan", "t1", body,
		map[string]string{"X-Actor-Role": "MEMBER"})
	if rr.Code != 403 {
		t.Fatalf("member-role status = %d, want 403", rr.Code)
	}
	// STAFF passes; ADMIN passes.
	rr = doTenant(t, d.pricingPlanPut, http.MethodPut, "/api/v1/pricing/plan", "t1", body,
		map[string]string{"X-Actor-Role": "STAFF"})
	if rr.Code != 200 {
		t.Fatalf("staff status = %d body=%s", rr.Code, rr.Body.String())
	}
	// Unknown tier → 404.
	rr = doTenant(t, d.pricingPlanPut, http.MethodPut, "/api/v1/pricing/plan", "t1",
		`{"tier_code":"NOPE","effective_from":"2025-01-01"}`, map[string]string{"X-Actor-Role": "ADMIN"})
	if rr.Code != 404 {
		t.Fatalf("unknown tier status = %d, want 404", rr.Code)
	}
	// Bad effective date → 422.
	rr = doTenant(t, d.pricingPlanPut, http.MethodPut, "/api/v1/pricing/plan", "t1",
		`{"tier_code":"SCALE","effective_from":"01/01/2025"}`, map[string]string{"X-Actor-Role": "ADMIN"})
	if rr.Code != 422 {
		t.Fatalf("bad date status = %d, want 422", rr.Code)
	}
	// GET plan returns the current assignment and history.
	rr = do(t, d.handler(), http.MethodGet, "/api/v1/pricing/plan", "t1", "")
	if rr.Code != 200 {
		t.Fatalf("plan get status = %d", rr.Code)
	}
	var out struct {
		Current     *pricing.Assignment  `json:"current"`
		Assignments []pricing.Assignment `json:"assignments"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Current == nil || out.Current.TierCode != "SCALE" || len(out.Assignments) != 1 {
		t.Fatalf("plan get = %+v", out)
	}
}
