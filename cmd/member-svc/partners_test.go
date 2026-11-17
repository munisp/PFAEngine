package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/partners"
)

var staffHdr = map[string]string{"X-Actor-Role": "STAFF"}

// doHdr exercises the full handler chain (mux path values intact) with
// extra request headers — needed for X-Actor-Role staff gating.
func doHdr(t *testing.T, h http.Handler, method, path, tenant, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if tenant != "" {
		req.Header.Set(httpsrv.TenantHeader, tenant)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func newPartnerDeps(t *testing.T) (*deps, partners.Partner) {
	t.Helper()
	d := newTestDeps()
	d.partners = partners.NewEngine()
	// Onboard + approve one ACTIVE bank partner via the API.
	rr := doHdr(t, d.handler(), http.MethodPost, "/api/v1/partners", "t1",
		`{"name":"Sterling Bank","type":"BANK","referral_code":"sterling-ng","rev_share_bps":250}`, staffHdr)
	if rr.Code != 201 {
		t.Fatalf("onboard status = %d body=%s", rr.Code, rr.Body.String())
	}
	var p partners.Partner
	if err := json.NewDecoder(rr.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	rr = doHdr(t, d.handler(), http.MethodPost, "/api/v1/partners/"+p.ID+"/approve", "t1", "", staffHdr)
	if rr.Code != 200 {
		t.Fatalf("approve status = %d body=%s", rr.Code, rr.Body.String())
	}
	return d, p
}

func TestPartnersOnboardStaffGating(t *testing.T) {
	d := newTestDeps()
	d.partners = partners.NewEngine()
	body := `{"name":"MTN","type":"TELCO","referral_code":"mtn-ng","rev_share_bps":100}`
	// Unauthenticated (no role) and member role are refused.
	if rr := doHdr(t, d.handler(), http.MethodPost, "/api/v1/partners", "t1", body, nil); rr.Code != 403 {
		t.Fatalf("no-role status = %d, want 403", rr.Code)
	}
	if rr := doHdr(t, d.handler(), http.MethodPost, "/api/v1/partners", "t1", body,
		map[string]string{"X-Actor-Role": "MEMBER"}); rr.Code != 403 {
		t.Fatalf("member-role status = %d, want 403", rr.Code)
	}
	// Staff pass; validation errors are 422.
	rr := doHdr(t, d.handler(), http.MethodPost, "/api/v1/partners", "t1",
		`{"name":"MTN","type":"NGO","referral_code":"mtn-ng","rev_share_bps":100}`, staffHdr)
	if rr.Code != 422 {
		t.Fatalf("bad type status = %d, want 422", rr.Code)
	}
	rr = doHdr(t, d.handler(), http.MethodPost, "/api/v1/partners", "t1", body, staffHdr)
	if rr.Code != 201 {
		t.Fatalf("staff onboard status = %d body=%s", rr.Code, rr.Body.String())
	}
	var p partners.Partner
	if err := json.NewDecoder(rr.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	if p.Status != partners.Pending || p.ReferralCode != "MTN-NG" {
		t.Fatalf("partner = %+v", p)
	}
	// Duplicate referral code → 422.
	rr = doHdr(t, d.handler(), http.MethodPost, "/api/v1/partners", "t1", body, staffHdr)
	if rr.Code != 422 {
		t.Fatalf("dup code status = %d, want 422", rr.Code)
	}
}

func TestPartnersLifecycleApproveSuspend(t *testing.T) {
	d, p := newPartnerDeps(t)
	// Approve of an ACTIVE partner is an illegal transition → 422.
	rr := doHdr(t, d.handler(), http.MethodPost, "/api/v1/partners/"+p.ID+"/approve", "t1", "", staffHdr)
	if rr.Code != 422 {
		t.Fatalf("double approve status = %d, want 422", rr.Code)
	}
	// Unknown partner → 404.
	rr = doHdr(t, d.handler(), http.MethodPost, "/api/v1/partners/prt-999999/approve", "t1", "", staffHdr)
	if rr.Code != 404 {
		t.Fatalf("unknown approve status = %d, want 404", rr.Code)
	}
	// Suspend requires staff; reason recorded.
	if rr := doHdr(t, d.handler(), http.MethodPost, "/api/v1/partners/"+p.ID+"/suspend", "t1",
		`{"reason":"AML"}`, nil); rr.Code != 403 {
		t.Fatalf("no-role suspend status = %d, want 403", rr.Code)
	}
	rr = doHdr(t, d.handler(), http.MethodPost, "/api/v1/partners/"+p.ID+"/suspend", "t1",
		`{"reason":"AML review"}`, staffHdr)
	if rr.Code != 200 {
		t.Fatalf("suspend status = %d", rr.Code)
	}
	var sp partners.Partner
	if err := json.NewDecoder(rr.Body).Decode(&sp); err != nil {
		t.Fatal(err)
	}
	if sp.Status != partners.Suspended || sp.SuspendReason != "AML review" {
		t.Fatalf("suspended partner = %+v", sp)
	}
	// List reflects status filter.
	rr = do(t, d.handler(), http.MethodGet, "/api/v1/partners?status=SUSPENDED", "t1", "")
	if rr.Code != 200 {
		t.Fatalf("list status = %d", rr.Code)
	}
	var out struct {
		Partners []partners.Partner `json:"partners"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Partners) != 1 || out.Partners[0].ID != p.ID {
		t.Fatalf("list = %+v", out.Partners)
	}
}

func TestPartnersAttributeAndSettlementFlow(t *testing.T) {
	d, p := newPartnerDeps(t)
	// Member-facing attribution (no role header needed).
	rr := do(t, d.handler(), http.MethodPost, "/api/v1/partners/attribute", "t1",
		`{"member_id":"m1","referral_code":"STERLING-NG"}`)
	if rr.Code != 201 {
		t.Fatalf("attribute status = %d body=%s", rr.Code, rr.Body.String())
	}
	var a partners.Attribution
	if err := json.NewDecoder(rr.Body).Decode(&a); err != nil {
		t.Fatal(err)
	}
	if a.PartnerID != p.ID {
		t.Fatalf("attribution = %+v", a)
	}
	// Idempotent + first-write-wins: repeat with another (unknown) code
	// still returns the existing attribution.
	rr = do(t, d.handler(), http.MethodPost, "/api/v1/partners/attribute", "t1",
		`{"member_id":"m1","referral_code":"WHATEVER"}`)
	if rr.Code != 201 {
		t.Fatalf("idempotent attribute status = %d", rr.Code)
	}
	// Unknown code for a new member → 404.
	rr = do(t, d.handler(), http.MethodPost, "/api/v1/partners/attribute", "t1",
		`{"member_id":"m2","referral_code":"NOPE"}`)
	if rr.Code != 404 {
		t.Fatalf("unknown code status = %d, want 404", rr.Code)
	}
	// Accrue (staff) ₦10,000 at 250 bps → ₦250 = 25,000 kobo.
	rr = doHdr(t, d.handler(), http.MethodPost, "/api/v1/partners/accruals", "t1",
		`{"member_id":"m1","contribution_kobo":1000000,"period":"2025-06"}`, staffHdr)
	if rr.Code != 201 {
		t.Fatalf("accrue status = %d body=%s", rr.Code, rr.Body.String())
	}
	// Unattributed member → 200 attributed:false.
	rr = doHdr(t, d.handler(), http.MethodPost, "/api/v1/partners/accruals", "t1",
		`{"member_id":"m-direct","contribution_kobo":1000000,"period":"2025-06"}`, staffHdr)
	if rr.Code != 200 {
		t.Fatalf("unattributed accrue status = %d", rr.Code)
	}
	// Suspension blocks accrual → 422.
	if rr := doHdr(t, d.handler(), http.MethodPost, "/api/v1/partners/"+p.ID+"/suspend", "t1",
		`{"reason":"review"}`, staffHdr); rr.Code != 200 {
		t.Fatalf("suspend status = %d", rr.Code)
	}
	rr = doHdr(t, d.handler(), http.MethodPost, "/api/v1/partners/accruals", "t1",
		`{"member_id":"m1","contribution_kobo":1000000,"period":"2025-06"}`, staffHdr)
	if rr.Code != 422 {
		t.Fatalf("suspended accrue status = %d, want 422", rr.Code)
	}
	// Settlement report lists the pre-suspension accrual only.
	rr = do(t, d.handler(), http.MethodGet, "/api/v1/partners/"+p.ID+"/settlement?period=2025-06", "t1", "")
	if rr.Code != 200 {
		t.Fatalf("settlement status = %d", rr.Code)
	}
	var out struct {
		Settlement partners.SettlementReport `json:"settlement"`
		Balance    int64                     `json:"balance"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Settlement.Total != 25_000 || len(out.Settlement.Lines) != 1 || out.Balance != 25_000 {
		t.Fatalf("settlement = %+v balance=%d", out.Settlement, out.Balance)
	}
	// Unknown partner settlement → 404.
	rr = do(t, d.handler(), http.MethodGet, "/api/v1/partners/prt-999999/settlement?period=2025-06", "t1", "")
	if rr.Code != 404 {
		t.Fatalf("unknown settlement status = %d, want 404", rr.Code)
	}
}
