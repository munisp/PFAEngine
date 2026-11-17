package payroll

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/munisp/pfaengine/internal/money"
)

// --- ERPNext / Frappe HR ---

// erpnextServer stubs the Frappe REST endpoints used by ERPNextClient and
// asserts the token auth header on every request.
func erpnextServer(t *testing.T, wantAuth string, handler http.Handler) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != wantAuth {
			http.Error(w, `{"exc":"auth"}`, http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, body string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprint(w, body)
}

func TestFetchPayrollEntryHappyPath(t *testing.T) {
	const auth = "token testkey:testsecret"
	// Single handler: Go 1.22+ ServeMux treats spaces in patterns as method
	// separators, and Frappe doctype paths ("Salary Slip") contain spaces.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/resource/Payroll Entry/PE-0001":
			writeJSON(t, w, 200, `{"data":{"name":"PE-0001","company":"ACME Ltd","posting_date":"2024-05-31"}}`)
		case "/api/resource/Salary Slip":
			if !strings.Contains(r.URL.Query().Get("filters"), "PE-0001") {
				t.Errorf("salary slip list missing payroll_entry filter: %s", r.URL.RawQuery)
			}
			writeJSON(t, w, 200, `{"data":[{"name":"SS-1"},{"name":"SS-2"}]}`)
		case "/api/resource/Salary Slip/SS-1":
			writeJSON(t, w, 200, `{"data":{"name":"SS-1","employee":"HR-EMP-001","employee_name":"Ada Obi","custom_rsa_pin":"PEN111","gross_pay":500000,"earnings":[
				{"salary_component":"Basic","amount":300000},
				{"salary_component":"Housing Allowance","amount":100000},
				{"salary_component":"Transport","amount":50000},
				{"salary_component":"Overtime","amount":50000}]}}`)
		case "/api/resource/Salary Slip/SS-2":
			// No structured earnings: pensionable base falls back to gross_pay.
			writeJSON(t, w, 200, `{"data":{"name":"SS-2","employee":"HR-EMP-002","custom_rsa_pin":"PEN222","gross_pay":200000}}`)
		default:
			t.Errorf("unexpected request path: %s", r.URL.Path)
			writeJSON(t, w, 404, `{"exc":"not found"}`)
		}
	})
	srv := erpnextServer(t, auth, handler)
	defer srv.Close()

	c := ERPNextClient{BaseURL: srv.URL, APIKey: "testkey", APISecret: "testsecret", HTTP: srv.Client()}
	s, err := c.FetchPayrollEntry(context.Background(), "PE-0001")
	if err != nil {
		t.Fatalf("FetchPayrollEntry: %v", err)
	}
	if s.ID != "PE-0001" || s.EmployerID != "ACME Ltd" || s.Period != "2024-05" {
		t.Fatalf("unexpected schedule header: %+v", s)
	}
	if len(s.Lines) != 2 {
		t.Fatalf("want 2 lines, got %d", len(s.Lines))
	}
	// SS-1: base 300000+100000+50000 = 450000 -> EE 8% = 36000.00, ER 10% = 45000.00 (overtime excluded).
	if got, want := s.Lines[0].Employee, money.NGN(36000); got != want {
		t.Errorf("SS-1 employee share: got %v want %v", got, want)
	}
	if got, want := s.Lines[0].Employer, money.NGN(45000); got != want {
		t.Errorf("SS-1 employer share: got %v want %v", got, want)
	}
	if s.Lines[0].RSA_PIN != "PEN111" || s.Lines[0].Period != "2024-05" {
		t.Errorf("SS-1 line fields: %+v", s.Lines[0])
	}
	// SS-2: gross fallback 200000 -> EE 16000.00, ER 20000.00.
	if got, want := s.Lines[1].Employee, money.NGN(16000); got != want {
		t.Errorf("SS-2 employee share: got %v want %v", got, want)
	}
	if got, want := s.Lines[1].Employer, money.NGN(20000); got != want {
		t.Errorf("SS-2 employer share: got %v want %v", got, want)
	}
	if got, want := s.Total(), money.NGN(117000); got != want {
		t.Errorf("schedule total: got %v want %v", got, want)
	}
}

func TestFetchPayrollEntryAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		writeJSON(t, w, 500, `{"exc":"boom"}`)
	}))
	defer srv.Close()
	c := ERPNextClient{BaseURL: srv.URL, APIKey: "k", APISecret: "s", HTTP: srv.Client()}
	if _, err := c.FetchPayrollEntry(context.Background(), "PE-1"); err == nil {
		t.Fatal("want error on HTTP 500")
	}
	if gotAuth != "token k:s" {
		t.Fatalf("Authorization header: got %q want %q", gotAuth, "token k:s")
	}
}

func TestFetchPayrollEntryHTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 500, `{"exc":"internal"}`)
	}))
	defer srv.Close()
	c := ERPNextClient{BaseURL: srv.URL, APIKey: "k", APISecret: "s", HTTP: srv.Client()}
	_, err := c.FetchPayrollEntry(context.Background(), "PE-1")
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("want HTTP 500 error, got %v", err)
	}
}

func TestFetchPayrollEntryMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `this is not json`)
	}))
	defer srv.Close()
	c := ERPNextClient{BaseURL: srv.URL, APIKey: "k", APISecret: "s", HTTP: srv.Client()}
	_, err := c.FetchPayrollEntry(context.Background(), "PE-1")
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("want decode error, got %v", err)
	}
}

func TestPushContributionSchedule(t *testing.T) {
	const auth = "token pk:ps"
	var gotPath, gotBody string
	srv := erpnextServer(t, auth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var v map[string]any
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			t.Errorf("decode push body: %v", err)
		}
		b, _ := json.Marshal(v)
		gotBody = string(b)
		writeJSON(t, w, 200, `{"data":{"name":"CS-0001"}}`)
	}))
	defer srv.Close()

	c := ERPNextClient{BaseURL: srv.URL, APIKey: "pk", APISecret: "ps", HTTP: srv.Client()}
	s := Schedule{
		ID:         "SCH-1",
		TenantID:   "t1",
		EmployerID: "EMP001",
		Period:     "2024-05",
		Lines: []Contribution{
			{EmployeeID: "E1", RSA_PIN: "PEN111", Employee: money.NGN(36000), Employer: money.NGN(45000), Period: "2024-05"},
		},
	}
	name, err := c.PushContributionSchedule(context.Background(), s)
	if err != nil {
		t.Fatalf("PushContributionSchedule: %v", err)
	}
	if name != "CS-0001" {
		t.Fatalf("created doc name: got %q want CS-0001", name)
	}
	if gotPath != "/api/resource/Contribution Schedule" {
		t.Errorf("POST path: got %q", gotPath)
	}
	if !strings.Contains(gotBody, `"total_kobo":8100000`) {
		t.Errorf("push body missing kobo total: %s", gotBody)
	}

	// Fail-closed on invalid schedule (no HTTP call made).
	if _, err := c.PushContributionSchedule(context.Background(), Schedule{}); err == nil {
		t.Fatal("want validation error for empty schedule")
	}
}

func TestPushContributionScheduleDoctypeEnv(t *testing.T) {
	t.Setenv("ERPNEXT_SCHEDULE_DOCTYPE", "Journal Entry")
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		writeJSON(t, w, 200, `{"data":{"name":"JE-0001"}}`)
	}))
	defer srv.Close()
	c := ERPNextClient{BaseURL: srv.URL, APIKey: "k", APISecret: "s", HTTP: srv.Client()}
	s := Schedule{
		ID: "SCH-1", EmployerID: "EMP001", Period: "2024-05",
		Lines: []Contribution{{RSA_PIN: "PEN111", Employee: money.NGN(100), Employer: money.NGN(125), Period: "2024-05"}},
	}
	if _, err := c.PushContributionSchedule(context.Background(), s); err != nil {
		t.Fatalf("PushContributionSchedule: %v", err)
	}
	if gotPath != "/api/resource/Journal%20Entry" && gotPath != "/api/resource/Journal Entry" {
		t.Errorf("POST path: got %q", gotPath)
	}
}

func TestPushContributionScheduleHTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 500, `{"exc":"boom"}`)
	}))
	defer srv.Close()
	c := ERPNextClient{BaseURL: srv.URL, APIKey: "k", APISecret: "s", HTTP: srv.Client()}
	s := Schedule{
		ID: "SCH-1", EmployerID: "EMP001", Period: "2024-05",
		Lines: []Contribution{{RSA_PIN: "PEN111", Employee: money.NGN(100), Employer: money.NGN(125), Period: "2024-05"}},
	}
	name, err := c.PushContributionSchedule(context.Background(), s)
	if err == nil || name != "" {
		t.Fatalf("want error and empty name, got name=%q err=%v", name, err)
	}
}

func TestNewERPNextFromEnv(t *testing.T) {
	if _, ok := NewERPNextFromEnv(); ok {
		t.Fatal("want ok=false with no env set")
	}
	t.Setenv("ERPNEXT_BASE_URL", "https://erp.example.com/")
	t.Setenv("ERPNEXT_API_KEY", "k")
	if _, ok := NewERPNextFromEnv(); ok {
		t.Fatal("want ok=false with missing secret")
	}
	t.Setenv("ERPNEXT_API_SECRET", "s")
	c, ok := NewERPNextFromEnv()
	if !ok {
		t.Fatal("want ok=true with full env")
	}
	if c.BaseURL != "https://erp.example.com" {
		t.Fatalf("BaseURL trailing slash not trimmed: %q", c.BaseURL)
	}
}

// --- PSSP CSV layouts ---

func sampleSchedule() Schedule {
	return Schedule{
		ID:         "SCH-9",
		TenantID:   "t1",
		EmployerID: "EMP001",
		Period:     "2024-05",
		Lines: []Contribution{
			{EmployeeID: "E1", RSA_PIN: "PEN111", Employee: money.NGN(36000), Employer: money.NGN(45000), Period: "2024-05"},
			{EmployeeID: "E2", RSA_PIN: "PEN222", Employee: money.NGN(16000), Employer: money.NGN(20000), Period: "2024-05"},
		},
	}
}

func TestPensphereRoundTrip(t *testing.T) {
	in := sampleSchedule()
	var buf strings.Builder
	if err := FormatPensphere(&buf, in); err != nil {
		t.Fatalf("FormatPensphere: %v", err)
	}
	out, err := ParsePensphere(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("ParsePensphere: %v", err)
	}
	if out.Total() != in.Total() {
		t.Fatalf("total mismatch: got %v want %v", out.Total(), in.Total())
	}
	if out.EmployerID != in.EmployerID || out.Period != in.Period {
		t.Fatalf("header mismatch: %+v", out)
	}
	if out.ID != "EMP001/2024-05" {
		t.Fatalf("synthesised ID: got %q", out.ID)
	}
	if len(out.Lines) != len(in.Lines) {
		t.Fatalf("lines: got %d want %d", len(out.Lines), len(in.Lines))
	}
	for i := range in.Lines {
		// Pensphere has no STAFF_ID column: EmployeeID round-trips as the PIN.
		want := in.Lines[i]
		want.EmployeeID = want.RSA_PIN
		if out.Lines[i] != want {
			t.Errorf("line %d: got %+v want %+v", i, out.Lines[i], want)
		}
	}
}

func TestPensionCentralRoundTrip(t *testing.T) {
	in := sampleSchedule()
	var buf strings.Builder
	if err := FormatPensionCentral(&buf, in); err != nil {
		t.Fatalf("FormatPensionCentral: %v", err)
	}
	out, err := ParsePensionCentral(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("ParsePensionCentral: %v", err)
	}
	if out.Total() != in.Total() {
		t.Fatalf("total mismatch: got %v want %v", out.Total(), in.Total())
	}
	if out.ID != in.ID || out.EmployerID != in.EmployerID || out.Period != in.Period {
		t.Fatalf("header mismatch: %+v", out)
	}
	if len(out.Lines) != len(in.Lines) {
		t.Fatalf("lines: got %d want %d", len(out.Lines), len(in.Lines))
	}
	for i := range in.Lines {
		if out.Lines[i] != in.Lines[i] {
			t.Errorf("line %d: got %+v want %+v", i, out.Lines[i], in.Lines[i])
		}
	}
}

func TestPensphereMalformedRowLineNumber(t *testing.T) {
	csvData := "EMPLOYER_CODE,PERIOD,PIN,SURNAME,FIRST_NAME,MONTHLY_SALARY,EMPLOYEE_CONTRIB,EMPLOYER_CONTRIB,TOTAL\n" +
		"EMP001,2024-05,PEN111,Obi,Ada,450000.00,36000.00,45000.00,81000.00\n" +
		"EMP001,2024-05,PEN222,Eze,Bola,not-a-number,16000.00,20000.00,36000.00\n"
	_, err := ParsePensphere(strings.NewReader(csvData))
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("want line-numbered error for line 3, got %v", err)
	}
}

func TestPensphereTotalMismatch(t *testing.T) {
	csvData := "EMPLOYER_CODE,PERIOD,PIN,SURNAME,FIRST_NAME,MONTHLY_SALARY,EMPLOYEE_CONTRIB,EMPLOYER_CONTRIB,TOTAL\n" +
		"EMP001,2024-05,PEN111,Obi,Ada,450000.00,36000.00,45000.00,99999.00\n"
	_, err := ParsePensphere(strings.NewReader(csvData))
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("want TOTAL mismatch error on line 2, got %v", err)
	}
}

func TestPensphereZeroTotalRejected(t *testing.T) {
	csvData := "EMPLOYER_CODE,PERIOD,PIN,SURNAME,FIRST_NAME,MONTHLY_SALARY,EMPLOYEE_CONTRIB,EMPLOYER_CONTRIB,TOTAL\n" +
		"EMP001,2024-05,PEN111,Obi,Ada,0.00,0.00,0.00,0.00\n"
	_, err := ParsePensphere(strings.NewReader(csvData))
	if err == nil || !errors.Is(err, ErrNonPositiveTotal) {
		t.Fatalf("want ErrNonPositiveTotal, got %v", err)
	}
}

func TestPensphereNegativeRejected(t *testing.T) {
	csvData := "EMPLOYER_CODE,PERIOD,PIN,SURNAME,FIRST_NAME,MONTHLY_SALARY,EMPLOYEE_CONTRIB,EMPLOYER_CONTRIB,TOTAL\n" +
		"EMP001,2024-05,PEN111,Obi,Ada,450000.00,-36000.00,45000.00,9000.00\n"
	_, err := ParsePensphere(strings.NewReader(csvData))
	if err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("want negative-amount rejection, got %v", err)
	}
}

func TestPensionCentralMalformedRowLineNumber(t *testing.T) {
	csvData := "SCHEDULE_ID,EMPLOYER,PERIOD,STAFF_ID,RSA_PIN,NAME,BASIC,HOUSING,TRANSPORT,PENSION_EE,PENSION_ER\n" +
		"SCH-9,EMP001,2024-05,E1,PEN111,Ada Obi,450000.00,0.00,0.00,36000.00,45000.00\n" +
		"SCH-9,EMP001,2024-05,E2,PEN222,Bola Eze,200000.00,0.00,0.00,bogus,20000.00\n"
	_, err := ParsePensionCentral(strings.NewReader(csvData))
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("want line-numbered error for line 3, got %v", err)
	}
}

func TestPensionCentralZeroTotalRejected(t *testing.T) {
	csvData := "SCHEDULE_ID,EMPLOYER,PERIOD,STAFF_ID,RSA_PIN,NAME,BASIC,HOUSING,TRANSPORT,PENSION_EE,PENSION_ER\n" +
		"SCH-9,EMP001,2024-05,E1,PEN111,Ada Obi,0.00,0.00,0.00,0.00,0.00\n"
	_, err := ParsePensionCentral(strings.NewReader(csvData))
	if err == nil || !errors.Is(err, ErrNonPositiveTotal) {
		t.Fatalf("want ErrNonPositiveTotal, got %v", err)
	}
}

func TestBadHeaderRejected(t *testing.T) {
	csvData := "WRONG,COLUMNS\n1,2\n"
	if _, err := ParsePensphere(strings.NewReader(csvData)); err == nil {
		t.Fatal("want header mismatch error (pensphere)")
	}
	if _, err := ParsePensionCentral(strings.NewReader(csvData)); err == nil {
		t.Fatal("want header mismatch error (pension-central)")
	}
}

func TestValidatePeriod(t *testing.T) {
	for _, ok := range []string{"2024-05", "1999-12", "2030-01"} {
		if err := ValidatePeriod(ok); err != nil {
			t.Errorf("ValidatePeriod(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "2024-5", "2024/05", "2024-00", "2024-13", "202405", "abcd-ef"} {
		if err := ValidatePeriod(bad); err == nil {
			t.Errorf("ValidatePeriod(%q): want error", bad)
		}
	}
}

func TestPensionSplit(t *testing.T) {
	ee, er := PensionSplit(money.NGN(450000))
	if ee != money.NGN(36000) || er != money.NGN(45000) {
		t.Fatalf("PensionSplit(450000): got %v/%v", ee, er)
	}
	// Rounding: base 100.01 -> EE 8.0008 -> 8.00 (round half up to kobo).
	ee, er = PensionSplit(money.Kobo(10001))
	if ee != money.Kobo(800) || er != money.Kobo(1000) {
		t.Fatalf("PensionSplit(10001k): got %v/%v", ee, er)
	}
}
