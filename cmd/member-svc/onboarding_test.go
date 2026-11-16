package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/munisp/pfaengine/internal/httpsrv"
)

// TestOnboardingMemberLifecycle walks the MEMBER pipeline over HTTP:
// create -> ordered steps (KYC facade-backed) -> auto-ACTIVE -> the linked
// member exists with a working balance endpoint.
func TestOnboardingMemberLifecycle(t *testing.T) {
	h := newTestDeps().handler()

	rr := do(t, h, http.MethodPost, "/api/v1/onboarding", "t1",
		`{"stakeholder":"MEMBER","full_name":"Ada Onboard","dob":"1990-01-01","phone":"+2348000000099","email":"ada@example.com","nin":"12345678901","bvn":"10987654321"}`)
	if rr.Code != 201 {
		t.Fatalf("create = %d: %s", rr.Code, rr.Body.String())
	}
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	mustJSON(t, rr, &created)
	if created.ID == "" || created.Status != "DRAFT" {
		t.Fatalf("created case: %+v", created)
	}

	// Out-of-order step is rejected with 409.
	rr = do(t, h, http.MethodPost, "/api/v1/onboarding/"+created.ID+"/steps/rsa_open", "t1", `{}`)
	if rr.Code != 409 {
		t.Fatalf("out-of-order step = %d, want 409", rr.Code)
	}

	// Consent step requires explicit consent.
	rr = do(t, h, http.MethodPost, "/api/v1/onboarding/"+created.ID+"/steps/nin_verify", "t1", `{}`)
	if rr.Code != 200 {
		t.Fatalf("nin_verify = %d: %s", rr.Code, rr.Body.String())
	}
	rr = do(t, h, http.MethodPost, "/api/v1/onboarding/"+created.ID+"/steps/bvn_validate", "t1", `{}`)
	if rr.Code != 200 {
		t.Fatalf("bvn_validate = %d: %s", rr.Code, rr.Body.String())
	}
	rr = do(t, h, http.MethodPost, "/api/v1/onboarding/"+created.ID+"/steps/consent_record", "t1", `{}`)
	if rr.Code != 400 {
		t.Fatalf("consent_record without consent = %d, want 400", rr.Code)
	}
	rr = do(t, h, http.MethodPost, "/api/v1/onboarding/"+created.ID+"/steps/consent_record", "t1", `{"consent":true}`)
	if rr.Code != 200 {
		t.Fatalf("consent_record = %d: %s", rr.Code, rr.Body.String())
	}
	// Liveness failure (mock provider: "spoof*") blocks the step.
	rr = do(t, h, http.MethodPost, "/api/v1/onboarding/"+created.ID+"/steps/liveness_enroll", "t1", `{"selfie_ref":"spoof-attempt"}`)
	if rr.Code != 422 {
		t.Fatalf("liveness spoof = %d, want 422", rr.Code)
	}
	rr = do(t, h, http.MethodPost, "/api/v1/onboarding/"+created.ID+"/steps/liveness_enroll", "t1", `{"selfie_ref":"clean-selfie"}`)
	if rr.Code != 200 {
		t.Fatalf("liveness_enroll = %d: %s", rr.Code, rr.Body.String())
	}
	rr = do(t, h, http.MethodPost, "/api/v1/onboarding/"+created.ID+"/steps/rsa_open", "t1", `{}`)
	if rr.Code != 200 {
		t.Fatalf("rsa_open = %d: %s", rr.Code, rr.Body.String())
	}
	var done struct {
		Status string `json:"status"`
		RefID  string `json:"ref_id"`
	}
	mustJSON(t, rr, &done)
	if done.Status != "ACTIVE" || done.RefID == "" {
		t.Fatalf("final case: %+v, want ACTIVE with ref_id", done)
	}

	// The member exists and the balance endpoint works.
	rr = do(t, h, http.MethodGet, "/api/v1/members/"+done.RefID+"/balance", "t1", "")
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"balance_kobo":0`) {
		t.Fatalf("balance = %d: %s", rr.Code, rr.Body.String())
	}

	// Auto-approved case cannot be approved again.
	rr = doActor(t, h, http.MethodPost, "/api/v1/onboarding/"+created.ID+"/approve", "t1", "ops-1", `{}`)
	if rr.Code != 409 {
		t.Fatalf("approve auto-approved case = %d, want 409", rr.Code)
	}
}

// TestOnboardingMicroMember covers the MICRO_MEMBER happy path and the
// optional-step rejection over HTTP.
func TestOnboardingMicroMember(t *testing.T) {
	h := newTestDeps().handler()
	rr := do(t, h, http.MethodPost, "/api/v1/onboarding", "t1",
		`{"stakeholder":"MICRO_MEMBER","full_name":"Micro Ada","phone":"+2348000000098"}`)
	if rr.Code != 201 {
		t.Fatalf("create = %d: %s", rr.Code, rr.Body.String())
	}
	var c struct {
		ID string `json:"id"`
	}
	mustJSON(t, rr, &c)

	// Optional steps cannot be advanced directly.
	rr = do(t, h, http.MethodPost, "/api/v1/onboarding/"+c.ID+"/steps/first_contribution_optional", "t1", `{}`)
	if rr.Code != 409 {
		t.Fatalf("optional step = %d, want 409", rr.Code)
	}
	for _, step := range []string{"id_capture", "pin_request"} {
		rr = do(t, h, http.MethodPost, "/api/v1/onboarding/"+c.ID+"/steps/"+step, "t1", `{}`)
		if rr.Code != 200 {
			t.Fatalf("%s = %d: %s", step, rr.Code, rr.Body.String())
		}
	}
	var done struct {
		Status string `json:"status"`
		RefID  string `json:"ref_id"`
	}
	mustJSON(t, rr, &done)
	if done.Status != "ACTIVE" || done.RefID == "" {
		t.Fatalf("micro case: %+v", done)
	}
}

// TestOnboardingGuards covers create/lookup error paths.
func TestOnboardingGuards(t *testing.T) {
	h := newTestDeps().handler()
	cases := []struct {
		name   string
		method string
		path   string
		actor  string
		body   string
		want   int
	}{
		{"unknown stakeholder", http.MethodPost, "/api/v1/onboarding", "", `{"stakeholder":"ALIEN"}`, 400},
		{"employer pipeline not served here", http.MethodPost, "/api/v1/onboarding", "", `{"stakeholder":"EMPLOYER"}`, 400},
		{"member requires nin", http.MethodPost, "/api/v1/onboarding", "", `{"stakeholder":"MEMBER","full_name":"NoNin"}`, 400},
		{"unknown case", http.MethodGet, "/api/v1/onboarding/onb-999999", "", "", 404},
		{"step on unknown case", http.MethodPost, "/api/v1/onboarding/onb-999999/steps/nin_verify", "", `{}`, 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := doActor(t, h, tc.method, tc.path, "t1", tc.actor, tc.body)
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

// doActor is do() plus an optional X-Actor staff identity header.
func doActor(t *testing.T, h http.Handler, method, path, tenant, actor, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if tenant != "" {
		req.Header.Set(httpsrv.TenantHeader, tenant)
	}
	if actor != "" {
		req.Header.Set("X-Actor", actor)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func mustJSON(t *testing.T, rr *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
}
