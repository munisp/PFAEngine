package ekyc

import (
	"context"
	"testing"

	"github.com/munisp/pfaengine/internal/casemgmt"
	"github.com/munisp/pfaengine/internal/docupload"
	"github.com/munisp/pfaengine/internal/kyc"
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
