package pencomrails

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func validRecord() RecaptureRecord {
	return RecaptureRecord{
		PIN:          "PEN123456789012",
		NIN:          "12345678901",
		Surname:      "Adebayo",
		FirstName:    "Chiamaka",
		MiddleName:   "Olu",
		DOB:          "1988-04-17",
		Gender:       "F",
		Phone:        "+2348012345678",
		Email:        "c.adebayo@example.ng",
		EmployerCode: "EMP001",
	}
}

func TestRecaptureRecordValidate(t *testing.T) {
	if err := validRecord().Validate(); err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*RecaptureRecord)
	}{
		{"bad PIN short", func(r *RecaptureRecord) { r.PIN = "PEN123" }},
		{"bad PIN prefix", func(r *RecaptureRecord) { r.PIN = "RSA123456789012" }},
		{"bad NIN length", func(r *RecaptureRecord) { r.NIN = "1234567890" }},
		{"bad NIN alpha", func(r *RecaptureRecord) { r.NIN = "1234567890A" }},
		{"missing surname", func(r *RecaptureRecord) { r.Surname = "" }},
		{"missing first name", func(r *RecaptureRecord) { r.FirstName = " " }},
		{"bad DOB", func(r *RecaptureRecord) { r.DOB = "17/04/1988" }},
		{"impossible DOB", func(r *RecaptureRecord) { r.DOB = "1988-02-30" }},
		{"bad gender", func(r *RecaptureRecord) { r.Gender = "X" }},
		{"bad phone", func(r *RecaptureRecord) { r.Phone = "08012345678" }},
		{"bad phone cc", func(r *RecaptureRecord) { r.Phone = "+14085551234" }},
		{"missing employer", func(r *RecaptureRecord) { r.EmployerCode = "" }},
	}
	for _, tc := range cases {
		r := validRecord()
		tc.mutate(&r)
		if err := r.Validate(); err == nil {
			t.Errorf("%s: expected validation error", tc.name)
		}
	}
}

func TestRecaptureFileRoundTrip(t *testing.T) {
	recs := []RecaptureRecord{validRecord(), func() RecaptureRecord {
		r := validRecord()
		r.PIN = "PEN999999999999"
		r.NIN = "98765432109"
		r.MiddleName = ""
		r.Email = ""
		return r
	}()}
	var buf bytes.Buffer
	if err := BuildRecaptureFile(&buf, recs); err != nil {
		t.Fatalf("build: %v", err)
	}
	got, err := ParseRecaptureFile(&buf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != len(recs) {
		t.Fatalf("got %d records want %d", len(got), len(recs))
	}
	for i := range recs {
		if got[i] != recs[i] {
			t.Errorf("record %d mismatch:\n got %+v\nwant %+v", i, got[i], recs[i])
		}
	}
}

func TestRecaptureFileTamperDetection(t *testing.T) {
	var buf bytes.Buffer
	if err := BuildRecaptureFile(&buf, []RecaptureRecord{validRecord()}); err != nil {
		t.Fatalf("build: %v", err)
	}
	tampered := strings.Replace(buf.String(), "Adebayo", "AdebayX", 1)
	if _, err := ParseRecaptureFile(strings.NewReader(tampered)); err == nil {
		t.Fatal("tampered file accepted — checksum not enforced")
	}
	// Count mismatch must also fail.
	countTampered := strings.Replace(buf.String(), "TRAILER|1|", "TRAILER|2|", 1)
	if _, err := ParseRecaptureFile(strings.NewReader(countTampered)); err == nil {
		t.Fatal("count-tampered file accepted")
	}
}

func TestBuildRecaptureFileRejectsBadRecord(t *testing.T) {
	bad := validRecord()
	bad.NIN = "short"
	var buf bytes.Buffer
	if err := BuildRecaptureFile(&buf, []RecaptureRecord{bad}); err == nil {
		t.Fatal("expected build to fail on invalid record")
	}
}

func TestParseRecaptureResponse(t *testing.T) {
	body := "# PENCAP results\nPEN123456789012|ACCEPTED|\nPEN999999999999|REJECTED|NIN mismatch\n\n"
	res, err := ParseRecaptureResponse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("got %d results want 2", len(res))
	}
	if res[0].Status != RecaptureAccepted || res[1].Status != RecaptureRejected || res[1].Reason != "NIN mismatch" {
		t.Fatalf("unexpected results %+v", res)
	}
	if _, err := ParseRecaptureResponse(strings.NewReader("PEN123|MAYBE|x")); err == nil {
		t.Fatal("unknown status accepted")
	}
	if _, err := ParseRecaptureResponse(strings.NewReader("\n\n")); err == nil {
		t.Fatal("empty response accepted")
	}
}

func TestTransferStateMachine(t *testing.T) {
	tr := TransferInstruction{Status: StatusPending}
	if err := tr.Transition(StatusConfirmed); err == nil {
		t.Fatal("PENDING -> CONFIRMED must be illegal")
	}
	if err := tr.Transition(StatusValidated); err != nil {
		t.Fatalf("PENDING -> VALIDATED: %v", err)
	}
	if err := tr.Transition(StatusPending); err == nil {
		t.Fatal("VALIDATED -> PENDING must be illegal")
	}
	if err := tr.Transition(StatusConfirmed); err != nil {
		t.Fatalf("VALIDATED -> CONFIRMED: %v", err)
	}
	for _, to := range []TransferStatus{StatusPending, StatusValidated, StatusRejected, StatusCancelled} {
		if err := tr.Transition(to); err == nil {
			t.Fatalf("CONFIRMED -> %s must be illegal (terminal)", to)
		}
	}
	// Reject and cancel paths.
	r2 := TransferInstruction{Status: StatusPending}
	if err := r2.Transition(StatusCancelled); err != nil {
		t.Fatalf("PENDING -> CANCELLED: %v", err)
	}
	r3 := TransferInstruction{Status: StatusValidated}
	if err := r3.Transition(StatusRejected); err != nil {
		t.Fatalf("VALIDATED -> REJECTED: %v", err)
	}
	if err := r3.Transition(StatusConfirmed); err == nil {
		t.Fatal("REJECTED is terminal")
	}
}

func TestCanTransferEligibility(t *testing.T) {
	last := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	if !CanTransfer(time.Time{}, last) {
		t.Fatal("never-transferred member must be eligible")
	}
	if CanTransfer(last, last.Add(364*24*time.Hour)) {
		t.Fatal("364 days after last transfer must NOT be eligible")
	}
	if !CanTransfer(last, last.Add(365*24*time.Hour)) {
		t.Fatal("365 days after last transfer must be eligible")
	}
	if !CanTransfer(last, last.Add(400*24*time.Hour)) {
		t.Fatal("400 days after last transfer must be eligible")
	}
	if CanTransfer(last, last.Add(-time.Hour)) {
		t.Fatal("now before last transfer must NOT be eligible")
	}
}

func TestTransferFileRoundTrip(t *testing.T) {
	ts := []TransferInstruction{
		{ID: "T1", PIN: "PEN123456789012", FromPFA: "PFA001", ToPFA: "PFA002",
			InitiatedAt: time.Date(2025, 3, 1, 9, 30, 0, 0, time.UTC), Status: StatusPending},
		{ID: "T2", PIN: "PEN999999999999", FromPFA: "PFA002", ToPFA: "PFA001",
			InitiatedAt: time.Date(2025, 3, 2, 12, 0, 0, 0, time.UTC), Status: StatusValidated},
	}
	var buf bytes.Buffer
	if err := BuildTransferFile(&buf, ts); err != nil {
		t.Fatalf("build: %v", err)
	}
	got, err := ParseTransferFile(&buf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != len(ts) {
		t.Fatalf("got %d transfers want %d", len(got), len(ts))
	}
	for i := range ts {
		if got[i].ID != ts[i].ID || got[i].PIN != ts[i].PIN || got[i].Status != ts[i].Status ||
			!got[i].InitiatedAt.Equal(ts[i].InitiatedAt) {
			t.Errorf("transfer %d mismatch: got %+v want %+v", i, got[i], ts[i])
		}
	}
	// Bad header.
	if _, err := ParseTransferFile(strings.NewReader("A,B,C\n1,2,3\n")); err == nil {
		t.Fatal("bad header accepted")
	}
	// Bad status.
	bad := "TRANSFER_ID,PIN,FROM_PFA,TO_PFA,INITIATED_AT,STATUS\n" +
		"T1,PEN123456789012,PFA001,PFA002,2025-03-01T09:30:00Z,BOGUS\n"
	if _, err := ParseTransferFile(strings.NewReader(bad)); err == nil {
		t.Fatal("bad status accepted")
	}
}

// ---------------------------------------------------------------------------
// HTTP client tests (httptest)
// ---------------------------------------------------------------------------

func checkAuth(t *testing.T, r *http.Request, pfaCode string) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Errorf("Authorization = %q", got)
	}
	if got := r.Header.Get("X-PFA-Code"); got != pfaCode {
		t.Errorf("X-PFA-Code = %q want %q", got, pfaCode)
	}
}

func TestPENCAPClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r, "PFA001")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/recapture":
			if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "multipart/form-data") {
				t.Errorf("Content-Type = %q, want multipart", ct)
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("multipart parse: %v", err)
			}
			f, _, err := r.FormFile("file")
			if err != nil {
				t.Errorf("missing file part: %v", err)
			} else {
				defer f.Close()
				data, _ := io.ReadAll(f)
				if !strings.HasPrefix(string(data), recapHDRPrefix) {
					t.Errorf("uploaded file missing HDR")
				}
				if _, err := ParseRecaptureFile(bytes.NewReader(data)); err != nil {
					t.Errorf("uploaded file failed verification: %v", err)
				}
			}
			json.NewEncoder(w).Encode(map[string]string{"batch_id": "BATCH-42"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/recapture/BATCH-42/results":
			w.Write([]byte("PEN123456789012|ACCEPTED|\nPEN999999999999|REJECTED|NIN unverified\n"))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := &PENCAPClient{BaseURL: srv.URL, PFACode: "PFA001", APIKey: "test-key", HTTP: srv.Client()}
	ctx := context.Background()
	batchID, err := c.SubmitRecapture(ctx, []RecaptureRecord{validRecord(), func() RecaptureRecord {
		r := validRecord()
		r.PIN = "PEN999999999999"
		r.NIN = "98765432109"
		return r
	}()})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if batchID != "BATCH-42" {
		t.Fatalf("batchID = %q", batchID)
	}
	res, err := c.FetchResults(ctx, batchID)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(res) != 2 || res[1].Reason != "NIN unverified" {
		t.Fatalf("unexpected results %+v", res)
	}
	if _, err := c.SubmitRecapture(ctx, nil); err == nil {
		t.Fatal("empty submission accepted")
	}
}

func TestRTSHubClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r, "PFA001")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/transfers":
			json.NewEncoder(w).Encode(map[string]string{
				"id": "T-9", "pin": "PEN123456789012", "from_pfa": "PFA001", "to_pfa": "PFA002",
				"initiated_at": "2025-03-01T09:30:00Z", "status": "PENDING",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/transfers/inbound":
			if r.URL.Query().Get("since") == "" {
				t.Error("missing since param")
			}
			json.NewEncoder(w).Encode([]map[string]string{{
				"id": "T-7", "pin": "PEN555555555555", "from_pfa": "PFA009", "to_pfa": "PFA001",
				"initiated_at": "2025-02-15T08:00:00Z", "status": "PENDING",
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/transfers/T-7/confirm":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"ok":true}`))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	h := &RTSHub{BaseURL: srv.URL, PFACode: "PFA001", APIKey: "test-key", HTTP: srv.Client()}
	ctx := context.Background()
	tr, err := h.InitiateTransfer(ctx, "PEN123456789012", "PFA002")
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	if tr.ID != "T-9" || tr.Status != StatusPending {
		t.Fatalf("unexpected transfer %+v", tr)
	}
	if _, err := h.InitiateTransfer(ctx, "BADPIN", "PFA002"); err == nil {
		t.Fatal("invalid PIN accepted")
	}
	in, err := h.PollInbound(ctx, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(in) != 1 || in[0].ID != "T-7" {
		t.Fatalf("unexpected inbound %+v", in)
	}
	if err := h.Confirm(ctx, "T-7"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
}

func TestPSSPClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r, "PFA001")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/schedules":
			body, _ := io.ReadAll(r.Body)
			if !json.Valid(body) {
				t.Error("schedule body not JSON")
			}
			json.NewEncoder(w).Encode(map[string]string{"reference": "REF-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/schedules/REF-1":
			json.NewEncoder(w).Encode(map[string]string{"status": "REJECTED", "reason": "line 4: unknown PIN"})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := &PSSPClient{BaseURL: srv.URL, PFACode: "PFA001", APIKey: "test-key", HTTP: srv.Client()}
	ctx := context.Background()
	ref, err := c.SubmitSchedule(ctx, []byte(`{"id":"S1","lines":[]}`))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if ref != "REF-1" {
		t.Fatalf("ref = %q", ref)
	}
	if _, err := c.SubmitSchedule(ctx, []byte(`{not json`)); err == nil {
		t.Fatal("invalid JSON schedule accepted")
	}
	if _, err := c.SubmitSchedule(ctx, nil); err == nil {
		t.Fatal("empty schedule accepted")
	}
	st, err := c.ScheduleStatus(ctx, ref)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Status != PSSPRejected || st.Reason != "line 4: unknown PIN" {
		t.Fatalf("unexpected state %+v", st)
	}
}

func TestClientsFailClosedOnServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	ctx := context.Background()

	pc := &PENCAPClient{BaseURL: srv.URL, PFACode: "PFA001", APIKey: "k", HTTP: srv.Client()}
	if _, err := pc.SubmitRecapture(ctx, []RecaptureRecord{validRecord()}); err == nil {
		t.Fatal("PENCAP 500 accepted")
	}
	if _, err := pc.FetchResults(ctx, "B1"); err == nil {
		t.Fatal("PENCAP results 500 accepted")
	}
	rh := &RTSHub{BaseURL: srv.URL, PFACode: "PFA001", APIKey: "k", HTTP: srv.Client()}
	if _, err := rh.InitiateTransfer(ctx, "PEN123456789012", "PFA002"); err == nil {
		t.Fatal("RTS 500 accepted")
	}
	if _, err := rh.PollInbound(ctx, time.Now()); err == nil {
		t.Fatal("RTS poll 500 accepted")
	}
	if err := rh.Confirm(ctx, "T-1"); err == nil {
		t.Fatal("RTS confirm 500 accepted")
	}
	ps := &PSSPClient{BaseURL: srv.URL, PFACode: "PFA001", APIKey: "k", HTTP: srv.Client()}
	if _, err := ps.SubmitSchedule(ctx, []byte(`{}`)); err == nil {
		t.Fatal("PSSP 500 accepted")
	}
	if _, err := ps.ScheduleStatus(ctx, "R1"); err == nil {
		t.Fatal("PSSP status 500 accepted")
	}
}

func TestClientsFailClosedOnMalformedResponses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<<<not json>>>"))
	}))
	defer srv.Close()
	ctx := context.Background()

	rh := &RTSHub{BaseURL: srv.URL, PFACode: "PFA001", APIKey: "k", HTTP: srv.Client()}
	if _, err := rh.InitiateTransfer(ctx, "PEN123456789012", "PFA002"); err == nil {
		t.Fatal("malformed RTS response accepted")
	}
	if _, err := rh.PollInbound(ctx, time.Now()); err == nil {
		t.Fatal("malformed RTS poll response accepted")
	}
	ps := &PSSPClient{BaseURL: srv.URL, PFACode: "PFA001", APIKey: "k", HTTP: srv.Client()}
	if _, err := ps.SubmitSchedule(ctx, []byte(`{}`)); err == nil {
		t.Fatal("malformed PSSP response accepted")
	}
	if _, err := ps.ScheduleStatus(ctx, "R1"); err == nil {
		t.Fatal("malformed PSSP status accepted")
	}
}

func TestPSSPUnknownStatusFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "MAYBE"})
	}))
	defer srv.Close()
	c := &PSSPClient{BaseURL: srv.URL, PFACode: "PFA001", APIKey: "k", HTTP: srv.Client()}
	if _, err := c.ScheduleStatus(context.Background(), "R1"); err == nil {
		t.Fatal("unknown PSSP status accepted")
	}
}

func TestFromEnvFailClosed(t *testing.T) {
	for _, k := range []string{
		"PENCAP_BASE_URL", "PENCAP_PFA_CODE", "PENCAP_API_KEY",
		"RTS_BASE_URL", "RTS_PFA_CODE", "RTS_API_KEY",
		"PSSP_BASE_URL", "PSSP_PFA_CODE", "PSSP_API_KEY",
	} {
		t.Setenv(k, "")
	}
	if _, err := NewPENCAPFromEnv(); err == nil {
		t.Fatal("NewPENCAPFromEnv succeeded without env")
	}
	if _, err := NewRTSFromEnv(); err == nil {
		t.Fatal("NewRTSFromEnv succeeded without env")
	}
	if _, err := NewPSSPFromEnv(); err == nil {
		t.Fatal("NewPSSPFromEnv succeeded without env")
	}
	t.Setenv("PSSP_BASE_URL", "https://pssp.example")
	t.Setenv("PSSP_PFA_CODE", "PFA001")
	t.Setenv("PSSP_API_KEY", "k")
	if _, err := NewPSSPFromEnv(); err != nil {
		t.Fatalf("NewPSSPFromEnv with full env: %v", err)
	}
}

func TestUnconfiguredClientsFailClosed(t *testing.T) {
	ctx := context.Background()
	var pc *PENCAPClient
	if _, err := pc.SubmitRecapture(ctx, []RecaptureRecord{validRecord()}); err == nil {
		t.Fatal("nil PENCAP client accepted submission")
	}
	empty := &PSSPClient{}
	if _, err := empty.SubmitSchedule(ctx, []byte(`{}`)); err == nil {
		t.Fatal("unconfigured PSSP client accepted submission")
	}
}
