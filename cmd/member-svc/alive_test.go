package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

// postAlive submits an I-Am-Alive liveness check for id.
func postAlive(t *testing.T, h http.Handler, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, h, http.MethodPost, "/api/v1/members/"+id+"/alive", "t1", body)
}

// TestSubmitAliveOutcomes covers the three regulator-threshold outcomes via
// the deterministic mock provider: plain ref -> 0.95 (verified), "blur*" ->
// 0.70 (manual review), "spoof*" -> 0.30 (failed).
func TestSubmitAliveOutcomes(t *testing.T) {
	cases := []struct {
		name       string
		selfieRef  string
		wantStatus int
		wantState  string
	}{
		{"verified", "clear-selfie", http.StatusOK, "VERIFIED"},
		{"manual review", "blur-selfie", http.StatusAccepted, "MANUAL_REVIEW"},
		{"failed", "spoof-selfie", http.StatusUnprocessableEntity, "FAILED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDeps()
			h := d.handler()
			id := onboardMember(t, h)

			rr := postAlive(t, h, id, `{"selfie_ref":"`+tc.selfieRef+`","consent":true}`)
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rr.Code, tc.wantStatus, rr.Body.String())
			}
			var out struct {
				Status      string    `json:"status"`
				NextDue     time.Time `json:"next_due"`
				RetriesLeft int       `json:"retries_left"`
			}
			if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
			if out.Status != tc.wantState {
				t.Fatalf("state = %q, want %q", out.Status, tc.wantState)
			}
			switch tc.wantState {
			case "VERIFIED":
				// Non-retiree member: next due = now + 12 months.
				if out.NextDue.Before(time.Now().UTC().AddDate(1, 0, -1)) {
					t.Fatalf("next_due = %s, want ~12 months out", out.NextDue)
				}
			case "FAILED":
				if out.RetriesLeft != maxAliveRetries-1 {
					t.Fatalf("retries_left = %d, want %d", out.RetriesLeft, maxAliveRetries-1)
				}
			}
		})
	}
}

// TestSubmitAliveRetiree verifies the retiree register path: a successful
// check resets the 12-month clock via retiree.RecordLiveness semantics.
func TestSubmitAliveRetiree(t *testing.T) {
	d := newTestDeps()
	h := d.handler()
	id := onboardMember(t, h)
	enrolledAt := time.Now().UTC().AddDate(-1, 6, 0)
	d.retirees.Enroll("t1", id, money.Kobo(2500000), "bank-ref-1", enrolledAt)

	before := time.Now().UTC()
	rr := postAlive(t, h, id, `{"selfie_ref":"clear-selfie","consent":true}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rr.Code, rr.Body.String())
	}
	rt, ok := d.retirees.Get(id)
	if !ok {
		t.Fatal("retiree missing")
	}
	if rt.LastLivenessAt.Before(before) {
		t.Fatalf("LastLivenessAt not reset: %s (check submitted after %s)", rt.LastLivenessAt, before)
	}
	if rt.NextLivenessDue.Before(time.Now().UTC().AddDate(1, 0, -1)) {
		t.Fatalf("NextLivenessDue = %s, want ~12 months from now", rt.NextLivenessDue)
	}
	var out struct {
		NextDue time.Time `json:"next_due"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.NextDue.Equal(rt.NextLivenessDue) {
		t.Fatalf("response next_due = %s, register has %s", out.NextDue, rt.NextLivenessDue)
	}
}

func TestSubmitAliveRequiresConsent(t *testing.T) {
	h := newTestDeps().handler()
	id := onboardMember(t, h)

	for _, body := range []string{
		`{"selfie_ref":"clear-selfie","consent":false}`,
		`{"selfie_ref":"clear-selfie"}`, // consent defaults to false
	} {
		rr := postAlive(t, h, id, body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", body, rr.Code)
		}
	}
	// The failed consent attempts must not consume retries.
	rr := postAlive(t, h, id, `{"selfie_ref":"clear-selfie","consent":true}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("post-consent submit status = %d (%s)", rr.Code, rr.Body.String())
	}
}

func TestSubmitAliveUnknownMember(t *testing.T) {
	rr := postAlive(t, newTestDeps().handler(), "prs-999", `{"selfie_ref":"clear-selfie","consent":true}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

// TestSubmitAliveRetryCap enforces 3 failed attempts per 24h: the 4th
// failure is refused with 429.
func TestSubmitAliveRetryCap(t *testing.T) {
	h := newTestDeps().handler()
	id := onboardMember(t, h)

	wantLeft := []int{2, 1, 0}
	for i, want := range wantLeft {
		rr := postAlive(t, h, id, `{"selfie_ref":"spoof-selfie","consent":true}`)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("attempt %d: status = %d, want 422 (%s)", i+1, rr.Code, rr.Body.String())
		}
		var out struct {
			Status      string `json:"status"`
			RetriesLeft int    `json:"retries_left"`
		}
		if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		if out.Status != "FAILED" || out.RetriesLeft != want {
			t.Fatalf("attempt %d: %+v, want FAILED/%d", i+1, out, want)
		}
	}
	// Cap reached: further attempts are refused without burning providers.
	rr := postAlive(t, h, id, `{"selfie_ref":"spoof-selfie","consent":true}`)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("capped attempt: status = %d, want 429 (%s)", rr.Code, rr.Body.String())
	}
}

// TestAliveStatus covers the GET endpoint: 404 before the first successful
// verification, then the recorded status afterwards.
func TestAliveStatus(t *testing.T) {
	h := newTestDeps().handler()
	id := onboardMember(t, h)

	rr := do(t, h, http.MethodGet, "/api/v1/members/"+id+"/alive", "t1", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("never verified: status = %d, want 404", rr.Code)
	}

	// A manual-review outcome does not count as verified.
	if rr := postAlive(t, h, id, `{"selfie_ref":"blur-selfie","consent":true}`); rr.Code != http.StatusAccepted {
		t.Fatalf("manual review: status = %d", rr.Code)
	}
	rr = do(t, h, http.MethodGet, "/api/v1/members/"+id+"/alive", "t1", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("manual review must not satisfy status: %d", rr.Code)
	}

	before := time.Now().UTC()
	if rr := postAlive(t, h, id, `{"selfie_ref":"clear-selfie","consent":true}`); rr.Code != http.StatusOK {
		t.Fatalf("verify: status = %d", rr.Code)
	}
	rr = do(t, h, http.MethodGet, "/api/v1/members/"+id+"/alive", "t1", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status endpoint: %d (%s)", rr.Code, rr.Body.String())
	}
	var out struct {
		LastVerifiedAt time.Time `json:"last_verified_at"`
		NextDue        time.Time `json:"next_due"`
		Status         string    `json:"status"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "VERIFIED" {
		t.Fatalf("status = %q, want VERIFIED", out.Status)
	}
	if out.LastVerifiedAt.Before(before) {
		t.Fatalf("last_verified_at = %s, want >= %s", out.LastVerifiedAt, before)
	}
	if out.NextDue.Before(before.AddDate(1, 0, -1)) {
		t.Fatalf("next_due = %s, want ~12 months out", out.NextDue)
	}

	// Unknown member: 404.
	rr = do(t, h, http.MethodGet, "/api/v1/members/prs-999/alive", "t1", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown member: status = %d, want 404", rr.Code)
	}
}
