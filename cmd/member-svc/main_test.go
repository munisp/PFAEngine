package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/authn"
	"github.com/munisp/pfaengine/internal/casemgmt"
	"github.com/munisp/pfaengine/internal/crm"
	"github.com/munisp/pfaengine/internal/fraud"
	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/kyc"
	"github.com/munisp/pfaengine/internal/ledger"
	"github.com/munisp/pfaengine/internal/member"
	"github.com/munisp/pfaengine/internal/micropension"
	"github.com/munisp/pfaengine/internal/onboarding"
	"github.com/munisp/pfaengine/internal/prefs"
	"github.com/munisp/pfaengine/internal/recon"
	"github.com/munisp/pfaengine/internal/retiree"
	"github.com/munisp/pfaengine/internal/statement"
)

func newTestDeps() *deps {
	ce := casemgmt.New(nil)
	d := &deps{
		kyc:         kyc.NewService("test-salt", kyc.MockProvider{ProviderName: "mock-nimc"}),
		members:     member.NewRegistry(),
		ledger:      ledger.New(),
		recon:       recon.NewEngine(),
		audit:       audit.NewStore(),
		authn:       authn.NewService("test-signing-secret-0123456789"),
		retirees:    retiree.NewRegister(),
		prefs:       prefs.NewPersistentStore(prefs.NewStore(), nil),
		alive:       newAliveTracker(),
		onb:         onboarding.NewPersistentStore(onboarding.NewStore(audit.NewStore()), nil),
		onbPayloads: map[string]onboardPayload{},
		micro:       map[string]*micropension.Account{},
		stmtSecret:  []byte("test-statement-secret"),
		cases:       ce,
		fraud:       fraud.NewEngine(),
	}
	d.crm = crm.New(ce, d.memberFacts)
	return d
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

// onboardMember is a helper returning the new member ID.
func onboardMember(t *testing.T, h http.Handler) string {
	t.Helper()
	rr := onboard(t, h, `{"full_name":"Test Member","dob":"1990-01-01","nin":"12345678901"}`)
	if rr.Code != 201 {
		t.Fatalf("onboard status = %d (%s)", rr.Code, rr.Body.String())
	}
	var created struct {
		MemberID string `json:"member_id"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	return created.MemberID
}

func TestStatementRoundTrip(t *testing.T) {
	d := newTestDeps()
	h := d.handler()
	id := onboardMember(t, h)

	// Post a fully remitted schedule so the statement has a balance.
	csv := "rsa_pin,gross_kobo\n" + id + ",80000000\n"
	if rr := do(t, h, http.MethodPost, "/api/v1/contributions/upload?period=2026-11", "t1", csv); rr.Code != 201 {
		t.Fatalf("upload status = %d (%s)", rr.Code, rr.Body.String())
	}

	rr := do(t, h, http.MethodGet, "/api/v1/members/"+id+"/statement?period=2026-11", "t1", "")
	if rr.Code != 200 {
		t.Fatalf("statement status = %d (%s)", rr.Code, rr.Body.String())
	}
	var p statement.Payload
	if err := json.NewDecoder(rr.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	if p.VerifyCode == "" || p.ClosingBalance != 14400000 || p.RSAPIN != id {
		t.Fatalf("payload = %+v", p)
	}
	// The code must match an independent recomputation.
	want := statement.Sign(d.stmtSecret, "t1", id, "2026-11", p.ClosingBalance)
	if p.VerifyCode != want {
		t.Fatalf("verify code = %q, want %q", p.VerifyCode, want)
	}

	// Round-trip through the verify endpoint: good code passes, tampered
	// closing balance fails.
	good := fmt.Sprintf(`{"rsa_id":%q,"period":"2026-11","closing_kobo":14400000,"code":%q}`, id, p.VerifyCode)
	rr = do(t, h, http.MethodPost, "/api/v1/statements/verify", "t1", good)
	assertValid(t, rr, true)
	bad := fmt.Sprintf(`{"rsa_id":%q,"period":"2026-11","closing_kobo":1,"code":%q}`, id, p.VerifyCode)
	rr = do(t, h, http.MethodPost, "/api/v1/statements/verify", "t1", bad)
	assertValid(t, rr, false)

	// Unknown member: 404.
	rr = do(t, h, http.MethodGet, "/api/v1/members/prs-999/statement", "t1", "")
	if rr.Code != 404 {
		t.Fatalf("unknown member statement status = %d, want 404", rr.Code)
	}
}

func assertValid(t *testing.T, rr *httptest.ResponseRecorder, want bool) {
	t.Helper()
	if rr.Code != 200 {
		t.Fatalf("verify status = %d (%s)", rr.Code, rr.Body.String())
	}
	var out struct {
		Valid bool `json:"valid"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Valid != want {
		t.Fatalf("valid = %v, want %v", out.Valid, want)
	}
}

func TestResolveExceptionFlow(t *testing.T) {
	h := newTestDeps().handler()
	id := onboardMember(t, h)

	// Short-paid line forces an AMOUNT_MISMATCH exception.
	csv := "rsa_pin,gross_kobo,voluntary_kobo,is_ppp,received_kobo\n" + id + ",80000000,0,false,14000000\n"
	if rr := do(t, h, http.MethodPost, "/api/v1/contributions/upload?employer=emp-1&period=2026-11", "t1", csv); rr.Code != 201 {
		t.Fatalf("upload status = %d", rr.Code)
	}
	rr := do(t, h, http.MethodGet, "/api/v1/recon/exceptions", "t1", "")
	var excs []recon.Exception
	if err := json.NewDecoder(rr.Body).Decode(&excs); err != nil {
		t.Fatal(err)
	}
	if len(excs) != 1 {
		t.Fatalf("exceptions = %+v, want 1", excs)
	}
	excID := excs[0].ID

	// Invalid resolution value: 400.
	rr = do(t, h, http.MethodPost, "/api/v1/recon/exceptions/"+excID+"/resolve", "t1", `{"resolution":"NOPE"}`)
	if rr.Code != 400 {
		t.Fatalf("bad resolution status = %d, want 400", rr.Code)
	}
	// Unknown exception: 404.
	rr = do(t, h, http.MethodPost, "/api/v1/recon/exceptions/exc-999/resolve", "t1", `{"resolution":"REFUND"}`)
	if rr.Code != 404 {
		t.Fatalf("unknown exception status = %d, want 404", rr.Code)
	}
	// Resolve with maker identity; queue drains; double-resolve: 409.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/recon/exceptions/"+excID+"/resolve", strings.NewReader(`{"resolution":"RECLASSIFY"}`))
	req.Header.Set(httpsrv.TenantHeader, "t1")
	req.Header.Set("X-Actor", "ops-ada")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("resolve status = %d (%s)", rr.Code, rr.Body.String())
	}
	rr = do(t, h, http.MethodGet, "/api/v1/recon/exceptions", "t1", "")
	if err := json.NewDecoder(rr.Body).Decode(&excs); err != nil {
		t.Fatal(err)
	}
	if len(excs) != 0 {
		t.Fatalf("open exceptions after resolve = %+v", excs)
	}
	rr = do(t, h, http.MethodPost, "/api/v1/recon/exceptions/"+excID+"/resolve", "t1", `{"resolution":"REFUND"}`)
	if rr.Code != 409 {
		t.Fatalf("double-resolve status = %d, want 409", rr.Code)
	}
}
