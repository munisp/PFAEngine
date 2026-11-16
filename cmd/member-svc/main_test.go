package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/kyc"
	"github.com/munisp/pfaengine/internal/ledger"
	"github.com/munisp/pfaengine/internal/member"
	"github.com/munisp/pfaengine/internal/recon"
)

func newTestDeps() *deps {
	return &deps{
		kyc:     kyc.NewService("test-salt", kyc.MockProvider{ProviderName: "mock-nimc"}),
		members: member.NewRegistry(),
		ledger:  ledger.New(),
		recon:   recon.NewEngine(),
		audit:   audit.NewStore(),
	}
}

func do(t *testing.T, h http.Handler, method, path, tenant, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if tenant != "" {
		req.Header.Set(httpsrv.TenantHeader, tenant)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func onboard(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, h, http.MethodPost, "/api/v1/members", "t1", body)
}

func TestOnboardRequiresTenant(t *testing.T) {
	rr := do(t, newTestDeps().handler(), http.MethodPost, "/api/v1/members", "",
		`{"full_name":"Ada","nin":"12345678901"}`)
	if rr.Code != 400 {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	var env httpsrv.ErrorBody
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Code != "TENANT_REQUIRED" {
		t.Fatalf("code = %q", env.Code)
	}
}

func TestOnboardKYCAndBalanceFlow(t *testing.T) {
	h := newTestDeps().handler()

	// Bad NIN (not 11 digits) is rejected by kyc.VerifyNIN.
	rr := onboard(t, h, `{"full_name":"Ada Lovelace","dob":"1990-01-01","nin":"123"}`)
	if rr.Code != 422 {
		t.Fatalf("bad NIN status = %d, want 422", rr.Code)
	}

	rr = onboard(t, h, `{"full_name":"Ada Lovelace","dob":"1990-01-01","nin":"12345678901","bvn":"10987654321","phone":"+2348000000000"}`)
	if rr.Code != 201 {
		t.Fatalf("onboard status = %d, want 201 (%s)", rr.Code, rr.Body.String())
	}
	var created struct {
		MemberID    string `json:"member_id"`
		NINVerified bool   `json:"nin_verified"`
		BVNVerified bool   `json:"bvn_verified"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.MemberID == "" || !created.NINVerified || !created.BVNVerified {
		t.Fatalf("onboard response = %+v", created)
	}

	// Upload a schedule with an explicit (matching) remittance column.
	csv := "rsa_pin,gross_kobo,voluntary_kobo,is_ppp,received_kobo\n" +
		created.MemberID + ",80000000,0,false,14400000\n"
	rr = do(t, h, http.MethodPost, "/api/v1/contributions/upload?employer=emp-1&period=2026-11", "t1", csv)
	if rr.Code != 201 {
		t.Fatalf("upload status = %d (%s)", rr.Code, rr.Body.String())
	}
	var up struct {
		Lines       int   `json:"lines"`
		AutoMatched int   `json:"auto_matched"`
		Exceptions  int   `json:"exceptions"`
		PostedKobo  int64 `json:"posted_kobo"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&up); err != nil {
		t.Fatal(err)
	}
	if up.Lines != 1 || up.AutoMatched != 1 || up.Exceptions != 0 || up.PostedKobo != 14400000 {
		t.Fatalf("upload response = %+v", up)
	}

	// Balance reflects the posting (8% + 10% of 80000000 = 14400000 kobo).
	rr = do(t, h, http.MethodGet, "/api/v1/members/"+created.MemberID+"/balance", "t1", "")
	if rr.Code != 200 {
		t.Fatalf("balance status = %d", rr.Code)
	}
	var bal struct {
		BalanceKobo  int64  `json:"balance_kobo"`
		BalanceNaira string `json:"balance_naira"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&bal); err != nil {
		t.Fatal(err)
	}
	if bal.BalanceKobo != 14400000 || bal.BalanceNaira != "144000.00" {
		t.Fatalf("balance = %+v", bal)
	}

	// A short remittance raises an exception and does not post.
	csv2 := "rsa_pin,gross_kobo,voluntary_kobo,is_ppp,received_kobo\n" + created.MemberID + ",80000000,0,false,14000000\n"
	rr = do(t, h, http.MethodPost, "/api/v1/contributions/upload?employer=emp-1&period=2026-12", "t1", csv2)
	if rr.Code != 201 {
		t.Fatalf("upload2 status = %d (%s)", rr.Code, rr.Body.String())
	}
	if err := json.NewDecoder(rr.Body).Decode(&up); err != nil {
		t.Fatal(err)
	}
	if up.Exceptions != 1 || up.AutoMatched != 0 {
		t.Fatalf("upload2 response = %+v", up)
	}
	rr = do(t, h, http.MethodGet, "/api/v1/recon/exceptions", "t1", "")
	if rr.Code != 200 {
		t.Fatalf("exceptions status = %d", rr.Code)
	}
	var excs []recon.Exception
	if err := json.NewDecoder(rr.Body).Decode(&excs); err != nil {
		t.Fatal(err)
	}
	if len(excs) != 1 || excs[0].Reason != recon.ReasonAmountMismatch {
		t.Fatalf("exceptions = %+v", excs)
	}
}

func TestUnknownMemberBalance404(t *testing.T) {
	rr := do(t, newTestDeps().handler(), http.MethodGet, "/api/v1/members/prs-999/balance", "t1", "")
	if rr.Code != 404 {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestUploadRejectsUnknownPIN(t *testing.T) {
	rr := do(t, newTestDeps().handler(), http.MethodPost, "/api/v1/contributions/upload", "t1",
		"rsa_pin,gross_kobo\nprs-999,80000000\n")
	if rr.Code != 422 {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
}
