package main

// Gap 3/4 handler tests: rails fail closed when unconfigured; the PSSP
// import/export path works end-to-end without any external rail.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/munisp/pfaengine/internal/payroll"
)

func TestRailsFailClosedWhenUnconfigured(t *testing.T) {
	h := newTestDeps().handler()

	cases := []struct{ method, path, body string }{
		{"POST", "/api/v1/payroll/erpnext/sync", `{"payroll_entry_id":"PE-1"}`},
		{"POST", "/api/v1/payroll/schedules/x/push", `{}`},
		{"POST", "/api/v1/pencom/recapture", `{"records":[]}`},
		{"GET", "/api/v1/pencom/recapture/b1", ``},
		{"POST", "/api/v1/pencom/transfers", `{"pin":"PEN1","to_pfa":"PFA2"}`},
		{"GET", "/api/v1/pencom/transfers", ``},
		{"POST", "/api/v1/pencom/transfers/t1/confirm", `{}`},
		{"POST", "/api/v1/pencom/pssp/schedules", `{"schedule_id":"s1"}`},
		{"GET", "/api/v1/pencom/pssp/schedules/r1", ``},
	}
	for _, tc := range cases {
		rr := do(t, h, tc.method, tc.path, "t1", tc.body)
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s: code=%d body=%s, want 503", tc.method, tc.path, rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "RAIL_UNCONFIGURED") {
			t.Fatalf("%s %s: body=%s, want RAIL_UNCONFIGURED", tc.method, tc.path, rr.Body.String())
		}
	}
}

func TestPSSPImportExportRoundTrip(t *testing.T) {
	h := newTestDeps().handler()

	csv := "EMPLOYER_CODE,PERIOD,PIN,SURNAME,FIRST_NAME,MONTHLY_SALARY,EMPLOYEE_CONTRIB,EMPLOYER_CONTRIB,TOTAL\n" +
		"EMP-9,2026-10,PEN000000000001,Adaeze,Okeke,250000.00,14000.00,17500.00,31500.00\n"
	rr := do(t, h, "POST", "/api/v1/payroll/pssp/import?format=pensphere", "t1", csv)
	if rr.Code != 200 {
		t.Fatalf("import: %d %s", rr.Code, rr.Body.String())
	}
	var s payroll.Schedule
	if err := json.Unmarshal(rr.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Lines) != 1 || s.Total() != 3_150_000 { // ₦31,500 in kobo
		t.Fatalf("schedule: %+v total=%d", s, s.Total())
	}

	// Export the imported schedule back to Pensphere CSV.
	rr = do(t, h, "GET", "/api/v1/payroll/pssp/export?format=pensphere&id="+s.ID, "t1", "")
	if rr.Code != 200 {
		t.Fatalf("export: %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "PEN000000000001") || !strings.Contains(rr.Body.String(), "EMPLOYER_CODE") {
		t.Fatalf("export body wrong: %s", rr.Body.String())
	}

	// Bad rows get a line-numbered 400.
	rr = do(t, h, "POST", "/api/v1/payroll/pssp/import?format=pensphere", "t1",
		"EMPLOYER_CODE,PERIOD,PIN,SURNAME,FIRST_NAME,MONTHLY_SALARY,EMPLOYEE_CONTRIB,EMPLOYER_CONTRIB,TOTAL\nBAD,ROW\n")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad import: %d %s", rr.Code, rr.Body.String())
	}
	// Missing format → 400.
	rr = do(t, h, "POST", "/api/v1/payroll/pssp/import", "t1", csv)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("format-less import: %d", rr.Code)
	}
}
