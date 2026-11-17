package ekyc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/casemgmt"
	"github.com/munisp/pfaengine/internal/docupload"
	"github.com/munisp/pfaengine/internal/kyc"
	"github.com/munisp/pfaengine/internal/liveness"
)

type stubIntel struct {
	enabled     bool
	extractErr  error
	fields      map[string]string
	confidence  float64
	liveness    LivenessResult
	livenessErr error
	face        FaceCompareResult
	faceErr     error
}

func (s stubIntel) Enabled() bool { return s.enabled }
func (s stubIntel) Extract(ctx context.Context, img string, vlm bool) (ExtractResult, error) {
	if s.extractErr != nil {
		return ExtractResult{}, s.extractErr
	}
	return ExtractResult{Fields: s.fields, Confidence: s.confidence}, nil
}
func (s stubIntel) Liveness(ctx context.Context, frames []string) (LivenessResult, error) {
	return s.liveness, s.livenessErr
}
func (s stubIntel) FaceCompare(ctx context.Context, a, b string) (FaceCompareResult, error) {
	return s.face, s.faceErr
}
func (s stubIntel) Health(ctx context.Context) (HealthResult, error) {
	return HealthResult{OK: s.enabled}, nil
}

func newEngine(intel DocIntel) (*Engine, *casemgmt.Engine) {
	ks := kyc.NewService("salt", kyc.MockProvider{ProviderName: "mock"})
	ce := casemgmt.New(nil)
	return New(intel, ks, ce), ce
}

func runHappyPath(t *testing.T, intel DocIntel) (*Engine, Session) {
	t.Helper()
	e, _ := newEngine(intel)
	s := e.Start("t1", "m1", "tester")
	var err error
	if s, err = e.SubmitDocument("t1", s.ID, docupload.DocNINSlip, "ref://1", "aW1hZ2U=", false, "tester"); err != nil {
		t.Fatal(err)
	}
	if s, err = e.VerifyIdentity("t1", s.ID, "12345678901", "98765432109", "Amara Okafor", "1992-01-01", "0803", "tester"); err != nil {
		t.Fatal(err)
	}
	if s, err = e.SubmitBiometric("t1", s.ID, []string{"c2VsZmll"}, "tester"); err != nil {
		t.Fatal(err)
	}
	if s, err = e.Decide("t1", s.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	return e, s
}

var goodIntel = stubIntel{
	enabled:    true,
	fields:     map[string]string{"nin": "12345678901", "name": "AMARA OKAFOR", "date_of_birth": "1992-01-01"},
	confidence: 0.93,
	liveness:   LivenessResult{Live: true, Score: 0.97},
	face:       FaceCompareResult{Match: true, Score: 0.71},
}

func TestAutoApproveHappyPath(t *testing.T) {
	_, s := runHappyPath(t, goodIntel)
	if s.Decision != DecisionAutoApprove {
		t.Fatalf("expected AUTO_APPROVE, got %s risk=%d flags=%v", s.Decision, s.RiskScore, s.RiskFlags)
	}
	if s.CaseID != "" {
		t.Fatal("auto-approved session must not open a case")
	}
	if !s.NINVerified || !s.BVNVerified || !s.LivenessPassed || !s.FaceMatched {
		t.Fatalf("all checks should pass: %+v", s)
	}
}

func TestSidecarDownForcesManualReview(t *testing.T) {
	_, s := runHappyPath(t, stubIntel{enabled: false})
	if s.Decision != DecisionManualReview {
		t.Fatalf("fail-closed: expected MANUAL_REVIEW, got %s risk=%d", s.Decision, s.RiskScore)
	}
	if s.CaseID == "" {
		t.Fatal("manual review must open a KYC_REVIEW case")
	}
}

func TestSpoofRejected(t *testing.T) {
	intel := goodIntel
	intel.liveness = LivenessResult{Live: false, Score: 0.12}
	_, s := runHappyPath(t, intel)
	if s.Decision != DecisionReject {
		t.Fatalf("spoof must be rejected, got %s", s.Decision)
	}
}

func TestFaceClearMismatchRejected(t *testing.T) {
	intel := goodIntel
	intel.face = FaceCompareResult{Match: false, Score: 0.20}
	_, s := runHappyPath(t, intel)
	if s.Decision != DecisionReject {
		t.Fatalf("clear face mismatch must be rejected, got %s", s.Decision)
	}
}

func TestDuplicateIdentityManualReview(t *testing.T) {
	e, ce := newEngine(goodIntel)
	// first member occupies the identity
	s1 := e.Start("t1", "m1", "t")
	s1, _ = e.SubmitDocument("t1", s1.ID, docupload.DocNINSlip, "r1", "aW1hZ2U=", false, "t")
	s1, _ = e.VerifyIdentity("t1", s1.ID, "12345678901", "", "Amara Okafor", "1992-01-01", "0803", "t")
	s1, _ = e.SubmitBiometric("t1", s1.ID, []string{"c2VsZmll"}, "t")
	if s1, _ = e.Decide("t1", s1.ID, "t"); s1.Decision != DecisionAutoApprove {
		t.Fatalf("first should auto-approve, got %s risk=%d flags=%v", s1.Decision, s1.RiskScore, s1.RiskFlags)
	}
	// second member reusing the same NIN
	s2 := e.Start("t1", "m2", "t")
	s2, _ = e.SubmitDocument("t1", s2.ID, docupload.DocNINSlip, "r2", "aW1hZ2U=", false, "t")
	s2, _ = e.VerifyIdentity("t1", s2.ID, "12345678901", "", "Amara Clone", "1992-01-01", "0803", "t")
	s2, _ = e.SubmitBiometric("t1", s2.ID, []string{"c2VsZmll"}, "t")
	s2, _ = e.Decide("t1", s2.ID, "t")
	if s2.Decision == DecisionAutoApprove {
		t.Fatalf("duplicate identity must not auto-approve (risk=%d)", s2.RiskScore)
	}
	if s2.CaseID == "" {
		t.Fatal("duplicate identity manual review must open a case")
	}
	c, err := ce.Get("t1", s2.CaseID)
	if err != nil || c.Type != casemgmt.TypeKYCReview {
		t.Fatalf("expected KYC_REVIEW case, got %+v err=%v", c, err)
	}
}

func TestOverrideResolvesCase(t *testing.T) {
	intel := stubIntel{enabled: false} // forces manual review
	e, ce := newEngine(intel)
	s := e.Start("t1", "m1", "t")
	s, _ = e.SubmitDocument("t1", s.ID, docupload.DocIDCard, "r", "aW1hZ2U=", false, "t")
	s, _ = e.VerifyIdentity("t1", s.ID, "12345678901", "", "Amara Okafor", "1992-01-01", "0803", "t")
	s, _ = e.SubmitBiometric("t1", s.ID, []string{"c2VsZmll"}, "t")
	s, _ = e.Decide("t1", s.ID, "t")
	if s.Decision != DecisionManualReview || s.CaseID == "" {
		t.Fatalf("expected manual review with case, got %s", s.Decision)
	}
	s, err := e.Override("t1", s.ID, true, "staff1", "docs verified by hand")
	if err != nil {
		t.Fatal(err)
	}
	if s.Decision != DecisionAutoApprove {
		t.Fatalf("override approve failed: %s", s.Decision)
	}
	c, _ := ce.Get("t1", s.CaseID)
	if c.Status != casemgmt.Resolved {
		t.Fatalf("linked case should be resolved, got %s", c.Status)
	}
}

func TestStateMachineEnforced(t *testing.T) {
	e, _ := newEngine(goodIntel)
	s := e.Start("t1", "m1", "t")
	if _, err := e.Decide("t1", s.ID, "t"); err != ErrBadTransition {
		t.Fatalf("decide before biometric must fail, got %v", err)
	}
}

func TestNameTokenOverlap(t *testing.T) {
	if !nameTokensOverlap("AMARA OKAFOR", "Amara Okafor") {
		t.Fatal("identical names must overlap")
	}
	if !nameTokensOverlap("OKAFOR AMARA", "Amara Okafor") {
		t.Fatal("reordered names must overlap")
	}
	if nameTokensOverlap("JOHN DOE", "Amara Okafor") {
		t.Fatal("different names must not overlap")
	}
}

// ---- Gap 9: challenge-response (active) liveness --------------------------------

// challengeEngine returns an ekyc engine wired to a challenge-response
// liveness engine.
func challengeEngine(t *testing.T, require bool) *Engine {
	t.Helper()
	e, _ := newEngine(goodIntel)
	e.SetLiveness(liveness.New(0.60))
	e.SetRequireChallenge(require)
	return e
}

// toIdentityCheck drives a session to IDENTITY_CHECK.
func toIdentityCheck(t *testing.T, e *Engine) Session {
	t.Helper()
	s := e.Start("t1", "m1", "tester")
	var err error
	if s, err = e.SubmitDocument("t1", s.ID, docupload.DocNINSlip, "ref://1", "aW1hZ2U=", false, "tester"); err != nil {
		t.Fatal(err)
	}
	if s, err = e.VerifyIdentity("t1", s.ID, "12345678901", "98765432109", "Amara Okafor", "1992-01-01", "0803", "tester"); err != nil {
		t.Fatal(err)
	}
	if s.State != StateIdentityCheck {
		t.Fatalf("expected IDENTITY_CHECK, got %s", s.State)
	}
	return s
}

func passAll(view ChallengeView, score float64) []liveness.ActionResult {
	out := make([]liveness.ActionResult, 0, len(view.Actions))
	for _, a := range view.Actions {
		out = append(out, liveness.ActionResult{Action: a, Passed: true, Score: score})
	}
	return out
}

func TestChallengeIssueRespondPass(t *testing.T) {
	e := challengeEngine(t, false)
	s := toIdentityCheck(t, e)
	view, err := e.IssueChallenge("t1", s.ID, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if view.ID == "" || view.Nonce == "" || len(view.Actions) < 2 || view.SessionID != s.ID {
		t.Fatalf("bad challenge view: %+v", view)
	}
	if time.Until(view.ExpiresAt) > 121*time.Second || time.Until(view.ExpiresAt) <= 0 {
		t.Fatalf("expiry out of range: %v", view.ExpiresAt)
	}
	// Issuing from IDENTITY_CHECK moves the session to BIOMETRIC and marks
	// the challenge required.
	s, err = e.Get("t1", s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if s.State != StateBiometric || !s.ChallengeRequired {
		t.Fatalf("expected BIOMETRIC + challenge_required, got %s %+v", s.State, s)
	}
	// Respond and pass.
	s, err = e.SubmitChallengeResponse("t1", s.ID, view.ID, view.Nonce, passAll(view, 0.90), "tester")
	if err != nil {
		t.Fatal(err)
	}
	if !s.ChallengePassed || !s.LivenessPassed || s.ChallengeScore != 0.90 {
		t.Fatalf("challenge should pass: %+v", s)
	}
	// challenge_score recorded in history.
	found := false
	for _, h := range s.History {
		if h.Action == "CHALLENGE_PASSED" && strings.Contains(h.Detail, "challenge_score=0.900") {
			found = true
		}
	}
	if !found {
		t.Fatalf("history missing challenge_score entry: %+v", s.History)
	}
	// Full pipeline still decides cleanly.
	s, err = e.Decide("t1", s.ID, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if s.Decision != DecisionAutoApprove {
		t.Fatalf("clean challenge flow should auto-approve, got %s risk=%d flags=%v", s.Decision, s.RiskScore, s.RiskFlags)
	}
}

func TestIssueChallengeStateGuard(t *testing.T) {
	e := challengeEngine(t, false)
	s := e.Start("t1", "m1", "t")
	if _, err := e.IssueChallenge("t1", s.ID, "t"); err != ErrBadTransition {
		t.Fatalf("challenge issue in DOCUMENT_CAPTURE must fail, got %v", err)
	}
	if _, err := e.IssueChallenge("t1", "ky-999999", "t"); err != ErrSessionNotFound {
		t.Fatalf("unknown session must 404-equivalent, got %v", err)
	}
}

func TestIssueChallengeNoEngine(t *testing.T) {
	e, _ := newEngine(goodIntel) // no liveness engine wired
	s := toIdentityCheck(t, e)
	if _, err := e.IssueChallenge("t1", s.ID, "t"); err != ErrLivenessNotConfigured {
		t.Fatalf("expected fail-closed not-configured, got %v", err)
	}
}

func TestChallengeReplayRejected(t *testing.T) {
	e := challengeEngine(t, false)
	s := toIdentityCheck(t, e)
	view, _ := e.IssueChallenge("t1", s.ID, "t")
	if _, err := e.SubmitChallengeResponse("t1", s.ID, view.ID, view.Nonce, passAll(view, 0.9), "t"); err != nil {
		t.Fatal(err)
	}
	// Replay the same challenge id + nonce.
	_, err := e.SubmitChallengeResponse("t1", s.ID, view.ID, view.Nonce, passAll(view, 0.9), "t")
	if !errors.Is(err, liveness.ErrChallengeUsed) {
		t.Fatalf("replay must fail with challenge-used, got %v", err)
	}
}

func TestChallengeNonceMismatch(t *testing.T) {
	e := challengeEngine(t, false)
	s := toIdentityCheck(t, e)
	view, _ := e.IssueChallenge("t1", s.ID, "t")
	_, err := e.SubmitChallengeResponse("t1", s.ID, view.ID, strings.Repeat("0", 32), passAll(view, 0.9), "t")
	if !errors.Is(err, liveness.ErrNonceMismatch) {
		t.Fatalf("expected nonce mismatch, got %v", err)
	}
}

func TestChallengeFailedFlagsRisk(t *testing.T) {
	e := challengeEngine(t, false)
	s := toIdentityCheck(t, e)
	view, _ := e.IssueChallenge("t1", s.ID, "t")
	res := passAll(view, 0.95)
	res[0].Passed = false // one action failed
	s, err := e.SubmitChallengeResponse("t1", s.ID, view.ID, view.Nonce, res, "t")
	if err != nil {
		t.Fatal(err)
	}
	if s.ChallengePassed {
		t.Fatal("failed action must not pass the challenge")
	}
	hasFlag := false
	for _, f := range s.RiskFlags {
		if f == "challenge_failed" {
			hasFlag = true
		}
	}
	if !hasFlag || s.RiskScore < wChallengeFail {
		t.Fatalf("challenge_failed flag/weight missing: flags=%v risk=%d", s.RiskFlags, s.RiskScore)
	}
	s, err = e.Decide("t1", s.ID, "t")
	if err != nil {
		t.Fatal(err)
	}
	if s.Decision != DecisionReject {
		t.Fatalf("failed challenge should reject, got %s risk=%d", s.Decision, s.RiskScore)
	}
}

func TestSubmitBiometricFailClosedWhenChallengeRequired(t *testing.T) {
	// Tenant policy on: passive path closed from the start.
	e := challengeEngine(t, true)
	s := toIdentityCheck(t, e)
	if _, err := e.SubmitBiometric("t1", s.ID, []string{"c2VsZmll"}, "t"); err != ErrChallengeRequired {
		t.Fatalf("passive biometric must be rejected under challenge policy, got %v", err)
	}
	// Per-call policy: issuing a challenge closes the passive path even
	// without the global flag — the session is already in BIOMETRIC, so the
	// state machine itself rejects passive re-submission.
	e2 := challengeEngine(t, false)
	s2 := toIdentityCheck(t, e2)
	if _, err := e2.IssueChallenge("t1", s2.ID, "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.SubmitBiometric("t1", s2.ID, []string{"c2VsZmll"}, "t"); err != ErrBadTransition {
		t.Fatalf("passive biometric must be rejected after challenge issued, got %v", err)
	}
}

func TestDecideFailClosedWhenChallengeIncomplete(t *testing.T) {
	// Challenge issued but never responded -> Decide must not auto-approve.
	e := challengeEngine(t, false)
	s := toIdentityCheck(t, e)
	if _, err := e.IssueChallenge("t1", s.ID, "t"); err != nil {
		t.Fatal(err)
	}
	s, err := e.Decide("t1", s.ID, "t")
	if err != nil {
		t.Fatal(err)
	}
	if s.Decision == DecisionAutoApprove {
		t.Fatal("incomplete required challenge must never auto-approve")
	}
	hasFlag := false
	for _, f := range s.RiskFlags {
		if f == "challenge_incomplete" {
			hasFlag = true
		}
	}
	if !hasFlag {
		t.Fatalf("expected challenge_incomplete flag, got %v", s.RiskFlags)
	}
}

func TestChallengeReissueInvalidatesPrior(t *testing.T) {
	e := challengeEngine(t, false)
	s := toIdentityCheck(t, e)
	v1, _ := e.IssueChallenge("t1", s.ID, "t")
	v2, err := e.IssueChallenge("t1", s.ID, "t") // re-issue allowed in BIOMETRIC
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.SubmitChallengeResponse("t1", s.ID, v1.ID, v1.Nonce, passAll(v1, 0.9), "t")
	if !errors.Is(err, liveness.ErrChallengeNotFound) {
		t.Fatalf("stale challenge must be invalidated, got %v", err)
	}
	s, err = e.SubmitChallengeResponse("t1", s.ID, v2.ID, v2.Nonce, passAll(v2, 0.9), "t")
	if err != nil || !s.ChallengePassed {
		t.Fatalf("fresh challenge should pass, got %+v err=%v", s, err)
	}
}
