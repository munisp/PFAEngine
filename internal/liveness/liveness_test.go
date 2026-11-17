package liveness

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func passResults(actions []Action, score float64) []ActionResult {
	out := make([]ActionResult, 0, len(actions))
	for _, a := range actions {
		out = append(out, ActionResult{Action: a, Passed: true, Score: score})
	}
	return out
}

func TestIssueHappyPath(t *testing.T) {
	e := New(0) // default threshold
	c, err := e.Issue("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == "" || !strings.HasPrefix(c.ID, "lc-") {
		t.Fatalf("bad challenge id %q", c.ID)
	}
	if n := len(c.Actions); n < 2 || n > 3 {
		t.Fatalf("expected 2-3 actions, got %d", n)
	}
	seen := map[Action]bool{}
	for _, a := range c.Actions {
		if seen[a] {
			t.Fatalf("duplicate action %v in %v", a, c.Actions)
		}
		seen[a] = true
	}
	if len(c.Nonce) != 32 { // hex of 16 bytes
		t.Fatalf("nonce should be 32 hex chars, got %d", len(c.Nonce))
	}
	if ttl := c.ExpiresAt.Sub(c.CreatedAt); ttl != DefaultTTL {
		t.Fatalf("TTL = %v, want %v", ttl, DefaultTTL)
	}
	if c.Used {
		t.Fatal("fresh challenge must not be used")
	}
}

func TestIssueEmptySession(t *testing.T) {
	e := New(0)
	if _, err := e.Issue(""); !errors.Is(err, ErrEmptySession) {
		t.Fatalf("expected ErrEmptySession, got %v", err)
	}
}

func TestIssueInvalidatesPriorChallenge(t *testing.T) {
	e := New(0)
	c1, err := e.Issue("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := e.Issue("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if c1.ID == c2.ID {
		t.Fatal("re-issue must produce a new challenge id")
	}
	// Prior challenge is gone entirely — not even a nonce-valid response works.
	_, err = e.Respond(c1.ID, c1.Nonce, passResults(c1.Actions, 0.9))
	if !errors.Is(err, ErrChallengeNotFound) {
		t.Fatalf("invalidated challenge should be gone, got %v", err)
	}
	// New challenge still works.
	v, err := e.Respond(c2.ID, c2.Nonce, passResults(c2.Actions, 0.9))
	if err != nil || !v.Passed {
		t.Fatalf("new challenge should pass, got %+v err=%v", v, err)
	}
}

func TestRespondHappyPath(t *testing.T) {
	e := New(0.60)
	c, _ := e.Issue("sess-1")
	v, err := e.Respond(c.ID, c.Nonce, passResults(c.Actions, 0.85))
	if err != nil {
		t.Fatal(err)
	}
	if !v.Passed || v.Score != 0.85 || v.SessionID != "sess-1" || v.ChallengeID != c.ID {
		t.Fatalf("bad verdict: %+v", v)
	}
}

func TestRespondNonceMismatch(t *testing.T) {
	e := New(0)
	c, _ := e.Issue("sess-1")
	_, err := e.Respond(c.ID, strings.Repeat("0", 32), passResults(c.Actions, 0.9))
	if !errors.Is(err, ErrNonceMismatch) {
		t.Fatalf("expected nonce mismatch, got %v", err)
	}
	// Challenge must NOT be consumed by a bad-nonce attempt.
	if _, err := e.Respond(c.ID, c.Nonce, passResults(c.Actions, 0.9)); err != nil {
		t.Fatalf("bad nonce must not consume challenge: %v", err)
	}
}

func TestRespondExpired(t *testing.T) {
	e := New(0)
	now := time.Now()
	e.SetClock(func() time.Time { return now })
	c, _ := e.Issue("sess-1")
	e.SetClock(func() time.Time { return now.Add(DefaultTTL + time.Second) })
	_, err := e.Respond(c.ID, c.Nonce, passResults(c.Actions, 0.9))
	if !errors.Is(err, ErrChallengeExpired) {
		t.Fatalf("expected expiry, got %v", err)
	}
}

func TestRespondReplayRejected(t *testing.T) {
	e := New(0)
	c, _ := e.Issue("sess-1")
	if _, err := e.Respond(c.ID, c.Nonce, passResults(c.Actions, 0.9)); err != nil {
		t.Fatal(err)
	}
	_, err := e.Respond(c.ID, c.Nonce, passResults(c.Actions, 0.9))
	if !errors.Is(err, ErrChallengeUsed) {
		t.Fatalf("replay must fail with ErrChallengeUsed, got %v", err)
	}
}

func TestRespondUnknownChallenge(t *testing.T) {
	e := New(0)
	_, err := e.Respond("lc-nope", strings.Repeat("a", 32), nil)
	if !errors.Is(err, ErrChallengeNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestRespondThresholdBoundary(t *testing.T) {
	e := New(0.60)
	// Exactly at threshold -> pass.
	c, _ := e.Issue("s1")
	v, err := e.Respond(c.ID, c.Nonce, passResults(c.Actions, 0.60))
	if err != nil || !v.Passed {
		t.Fatalf("score exactly at threshold must pass: %+v err=%v", v, err)
	}
	// A hair below -> fail.
	c, _ = e.Issue("s2")
	v, err = e.Respond(c.ID, c.Nonce, passResults(c.Actions, 0.599))
	if err != nil {
		t.Fatal(err)
	}
	if v.Passed {
		t.Fatalf("score %.3f below threshold 0.60 must fail", v.Score)
	}
	// Mean below threshold via one weak action -> fail (0.75/0.75/0.00 or
	// 0.75/0.00 depending on challenge size — both below 0.60).
	c, _ = e.Issue("s3")
	res := passResults(c.Actions, 0.75)
	res[0].Score = 0.0
	v, err = e.Respond(c.ID, c.Nonce, res)
	if err != nil {
		t.Fatal(err)
	}
	if v.Passed {
		t.Fatalf("mean %.3f below threshold must fail", v.Score)
	}
}

func TestRespondActionFailed(t *testing.T) {
	e := New(0)
	c, _ := e.Issue("s1")
	res := passResults(c.Actions, 0.99)
	res[0].Passed = false
	v, err := e.Respond(c.ID, c.Nonce, res)
	if err != nil {
		t.Fatal(err)
	}
	if v.Passed {
		t.Fatal("any failed action must fail the verdict regardless of scores")
	}
}

func TestRespondIncompleteResponse(t *testing.T) {
	e := New(0)
	c, _ := e.Issue("s1")
	res := passResults(c.Actions[:1], 0.9) // only the first action
	_, err := e.Respond(c.ID, c.Nonce, res)
	if !errors.Is(err, ErrIncompleteResponse) {
		t.Fatalf("expected incomplete-response error, got %v", err)
	}
	// Incomplete response consumes the challenge (fail-closed, no probing).
	_, err = e.Respond(c.ID, c.Nonce, passResults(c.Actions, 0.9))
	if !errors.Is(err, ErrChallengeUsed) {
		t.Fatalf("consumed challenge expected, got %v", err)
	}
}

func TestThresholdFromEnv(t *testing.T) {
	t.Setenv("LIVENESS_THRESHOLD", "")
	if got := ThresholdFromEnv(); got != DefaultThreshold {
		t.Fatalf("unset env should give default, got %v", got)
	}
	t.Setenv("LIVENESS_THRESHOLD", "0.75")
	if got := ThresholdFromEnv(); got != 0.75 {
		t.Fatalf("env override failed, got %v", got)
	}
	t.Setenv("LIVENESS_THRESHOLD", "banana")
	if got := ThresholdFromEnv(); got != DefaultThreshold {
		t.Fatalf("invalid env should give default, got %v", got)
	}
}

func TestFromEnvProvider(t *testing.T) {
	t.Setenv("SMILE_ID_PARTNER_ID", "")
	t.Setenv("SMILE_ID_API_KEY", "")
	if FromEnv() != nil {
		t.Fatal("no credentials -> nil provider (mock/local mode)")
	}
	t.Setenv("SMILE_ID_PARTNER_ID", "partner-1")
	if FromEnv() != nil {
		t.Fatal("partial credentials must yield nil (never half-configured)")
	}
	t.Setenv("SMILE_ID_API_KEY", "key-1")
	p := FromEnv()
	if p == nil {
		t.Fatal("full credentials should produce a Smile ID provider")
	}
}

type stubProvider struct {
	score  float64
	passed bool
	err    error
	sawB64 string
	sawAct []Action
}

func (s *stubProvider) VerifyActive(ctx context.Context, sessionID string, actions []Action, selfieB64 string) (float64, bool, error) {
	s.sawB64, s.sawAct = selfieB64, actions
	return s.score, s.passed, s.err
}

func TestRespondWithProvider(t *testing.T) {
	e := New(0.60)
	sp := &stubProvider{score: 0.92, passed: true}
	e.SetProvider(sp)
	c, _ := e.Issue("sess-p")
	v, err := e.RespondWithProvider(context.Background(), c.ID, c.Nonce, "c2VsZmll")
	if err != nil || !v.Passed || v.Score != 0.92 {
		t.Fatalf("provider verdict wrong: %+v err=%v", v, err)
	}
	if sp.sawB64 != "c2VsZmll" || len(sp.sawAct) != len(c.Actions) {
		t.Fatal("provider should receive selfie + challenged actions")
	}
	// Provider failure is fail-closed.
	sp2 := &stubProvider{err: errors.New("vendor down")}
	e2 := New(0.60)
	e2.SetProvider(sp2)
	c2, _ := e2.Issue("sess-p2")
	v2, err := e2.RespondWithProvider(context.Background(), c2.ID, c2.Nonce, "c2VsZmll")
	if err == nil || v2.Passed {
		t.Fatalf("provider error must fail closed, got %+v err=%v", v2, err)
	}
	// No provider configured.
	e3 := New(0)
	c3, _ := e3.Issue("sess-p3")
	if _, err := e3.RespondWithProvider(context.Background(), c3.ID, c3.Nonce, "x"); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("expected provider-unavailable, got %v", err)
	}
}

func TestSmileIDProviderFailClosedOn5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	p := &SmileIDProvider{PartnerID: "p", APIKey: "k", BaseURL: srv.URL}
	score, passed, err := p.VerifyActive(context.Background(), "sess", []Action{ActionBlink}, "c2VsZmll")
	if err == nil || passed || score != 0 {
		t.Fatalf("5xx must be fail-closed error, got score=%v passed=%v err=%v", score, passed, err)
	}
	if !strings.Contains(err.Error(), "5xx") {
		t.Fatalf("error should call out 5xx, got %v", err)
	}
}

func TestSmileIDProviderHappyPath(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/smart-selfie" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ResultCode":"0810","ResultText":"Liveness Verified","ConfidenceValue":93.5,"LivenessPassed":true}`)
	}))
	defer srv.Close()
	p := &SmileIDProvider{PartnerID: "p", APIKey: "k", BaseURL: srv.URL}
	score, passed, err := p.VerifyActive(context.Background(), "sess", []Action{ActionBlink, ActionNod}, "c2VsZmll")
	if err != nil || !passed || score != 0.935 {
		t.Fatalf("happy path wrong: score=%v passed=%v err=%v", score, passed, err)
	}
	for _, want := range []string{`"partner_id":"p"`, `"signature"`, `"job_type":"smart_selfie_active"`, `"BLINK"`, `"NOD"`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("request body missing %s: %s", want, gotBody)
		}
	}
}
