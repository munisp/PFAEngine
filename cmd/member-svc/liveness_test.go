package main

// Gap 9 handler tests. These require the deps.liveness field (integrated in
// main.go by the lead); with the field present they exercise issue/respond,
// the fail-closed 503, and replay rejection end-to-end.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/munisp/pfaengine/internal/docupload"
	"github.com/munisp/pfaengine/internal/liveness"
	"github.com/munisp/pfaengine/internal/tenancy"
)

// withLiveness wires a challenge engine into test deps and returns a session
// id parked in IDENTITY_CHECK.
func withLiveness(t *testing.T, d *deps) string {
	t.Helper()
	d.liveness = liveness.New(0.60)
	d.ekyc.SetLiveness(d.liveness)
	s := d.ekyc.Start("t1", "m1", "tester")
	var err error
	if s, err = d.ekyc.SubmitDocument("t1", s.ID, docupload.DocNINSlip, "r1", "", false, "tester"); err != nil {
		t.Fatal(err)
	}
	if s, err = d.ekyc.VerifyIdentity("t1", s.ID, "", "", "", "", "", "tester"); err != nil {
		t.Fatal(err)
	}
	return s.ID
}

func reqWithID(method, target, id, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.SetPathValue("id", id)
	r.Header.Set("X-Actor", "tester")
	return r.WithContext(tenancy.WithTenant(r.Context(), "t1"))
}

func TestChallengeHandlers503WhenUnconfigured(t *testing.T) {
	d := newTestDeps() // liveness nil
	w := httptest.NewRecorder()
	d.ekycIssueChallenge(w, httptest.NewRequest(http.MethodPost, "/api/v1/ekyc/sessions/ky-1/challenge", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("issue without engine = %d, want 503", w.Code)
	}
	if !strings.Contains(w.Body.String(), "LIVENESS_UNCONFIGURED") {
		t.Fatalf("expected LIVENESS_UNCONFIGURED, got %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	d.ekycRespondChallenge(w, httptest.NewRequest(http.MethodPost, "/api/v1/ekyc/sessions/ky-1/challenge/respond", strings.NewReader(`{}`)))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("respond without engine = %d, want 503", w.Code)
	}
}

func TestChallengeHandlersHappyPathAndReplay(t *testing.T) {
	d := newTestDeps()
	sid := withLiveness(t, d)

	// Issue.
	w := httptest.NewRecorder()
	d.ekycIssueChallenge(w, reqWithID(http.MethodPost, "/api/v1/ekyc/sessions/"+sid+"/challenge", sid, ""))
	if w.Code != http.StatusCreated {
		t.Fatalf("issue = %d: %s", w.Code, w.Body.String())
	}
	var view struct {
		ID      string            `json:"id"`
		Nonce   string            `json:"nonce"`
		Actions []liveness.Action `json:"actions"`
		Used    bool              `json:"used"` // must NOT be present
	}
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.ID == "" || view.Nonce == "" || len(view.Actions) < 2 {
		t.Fatalf("bad challenge view: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"used"`) {
		t.Fatal("internal field 'used' must not leak in the challenge view")
	}

	// Respond: all actions passed.
	results := make([]map[string]any, 0, len(view.Actions))
	for _, a := range view.Actions {
		results = append(results, map[string]any{"action": a, "passed": true, "score": 0.9})
	}
	body, _ := json.Marshal(map[string]any{"challenge_id": view.ID, "nonce": view.Nonce, "results": results})
	w = httptest.NewRecorder()
	d.ekycRespondChallenge(w, reqWithID(http.MethodPost, "/api/v1/ekyc/sessions/"+sid+"/challenge/respond", sid, string(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("respond = %d: %s", w.Code, w.Body.String())
	}
	var sess struct {
		LivenessPassed  bool    `json:"liveness_passed"`
		ChallengePassed bool    `json:"challenge_passed"`
		ChallengeScore  float64 `json:"challenge_score"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &sess); err != nil {
		t.Fatal(err)
	}
	if !sess.LivenessPassed || !sess.ChallengePassed || sess.ChallengeScore != 0.9 {
		t.Fatalf("session should show passed challenge: %s", w.Body.String())
	}

	// Replay the same nonce -> 400 CHALLENGE_INVALID.
	w = httptest.NewRecorder()
	d.ekycRespondChallenge(w, reqWithID(http.MethodPost, "/api/v1/ekyc/sessions/"+sid+"/challenge/respond", sid, string(body)))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "CHALLENGE_INVALID") {
		t.Fatalf("replay = %d: %s", w.Code, w.Body.String())
	}
}

func TestChallengeHandlerNonceMismatch(t *testing.T) {
	d := newTestDeps()
	sid := withLiveness(t, d)
	w := httptest.NewRecorder()
	d.ekycIssueChallenge(w, reqWithID(http.MethodPost, "/", sid, ""))
	var view struct {
		ID      string            `json:"id"`
		Actions []liveness.Action `json:"actions"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &view)
	body, _ := json.Marshal(map[string]any{"challenge_id": view.ID, "nonce": strings.Repeat("0", 32), "results": []any{}})
	w = httptest.NewRecorder()
	d.ekycRespondChallenge(w, reqWithID(http.MethodPost, "/", sid, string(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("nonce mismatch = %d, want 400", w.Code)
	}
}
