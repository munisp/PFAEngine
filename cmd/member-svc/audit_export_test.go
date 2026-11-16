package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/munisp/pfaengine/internal/httpsrv"
)

// exportReq issues GET /api/v1/audit/export with the given approver headers.
func exportReq(t *testing.T, h http.Handler, tenant, a1, a2 string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/export?tenant="+tenant, nil)
	req.Header.Set(httpsrv.TenantHeader, tenant)
	if a1 != "" {
		req.Header.Set("X-Approver-1", a1)
	}
	if a2 != "" {
		req.Header.Set("X-Approver-2", a2)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestAuditExportDualControl(t *testing.T) {
	h := newTestDeps().handler()
	memberID := onboardMember(t, h) // writes one audit event
	_ = memberID

	// No approver headers: 403.
	if rr := exportReq(t, h, "t1", "", ""); rr.Code != http.StatusForbidden {
		t.Fatalf("no approvers status = %d, want 403", rr.Code)
	}
	// Same approver twice: 403 (dual control requires two distinct staff).
	rr := exportReq(t, h, "t1", "ada", "ada")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("same approver status = %d, want 403", rr.Code)
	}
	var env httpsrv.ErrorBody
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Code != "DUAL_CONTROL_REQUIRED" {
		t.Fatalf("code = %q, want DUAL_CONTROL_REQUIRED", env.Code)
	}

	// Two distinct approvers: 200 with the chain and a clean integrity check.
	rr = exportReq(t, h, "t1", "ada", "grace")
	if rr.Code != http.StatusOK {
		t.Fatalf("export status = %d (%s)", rr.Code, rr.Body.String())
	}
	var out struct {
		Tenant     string   `json:"tenant"`
		Count      int      `json:"count"`
		ChainValid bool     `json:"chain_valid"`
		ApprovedBy []string `json:"approved_by"`
		Events     []struct {
			Action string `json:"action"`
			Hash   string `json:"hash"`
		} `json:"events"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Tenant != "t1" || !out.ChainValid || out.Count < 1 || len(out.ApprovedBy) != 2 {
		t.Fatalf("export = %+v", out)
	}
	if out.Events[0].Action != "member.onboarded" || out.Events[0].Hash == "" {
		t.Fatalf("first event = %+v", out.Events[0])
	}

	// Query tenant disagreeing with the header tenant: 403.
	if rr := exportReq(t, h, "t1", "ada", "grace"); rr.Code != 200 {
		t.Fatalf("re-export status = %d", rr.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/export?tenant=other", nil)
	req.Header.Set(httpsrv.TenantHeader, "t1")
	req.Header.Set("X-Approver-1", "ada")
	req.Header.Set("X-Approver-2", "grace")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("tenant mismatch status = %d, want 403", rr.Code)
	}
}
