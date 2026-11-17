// Package ekyc implements the next-generation eKYC engine (openKYC-style):
// document intelligence via the kyc-service sidecar (PaddleOCR + Docling +
// optional VLM cross-validation), NIN/BVN cross-checks via internal/kyc,
// passive liveness (MiniFASNet dual-scale ensemble) and face match
// (YuNet + SFace) via the sidecar, and a Ballerine-style verification
// session state machine with risk-scored decisions:
//
//	DOCUMENT_CAPTURE -> DOC_ANALYSIS -> IDENTITY_CHECK -> BIOMETRIC -> DECIDED
//
// Decisions: AUTO_APPROVE, MANUAL_REVIEW (routed into casemgmt KYC_REVIEW),
// or REJECT. All sidecar calls are fail-closed: when the document
// intelligence service is unavailable the session is forced to manual
// review, never auto-approved.
package ekyc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/casemgmt"
	"github.com/munisp/pfaengine/internal/docupload"
	"github.com/munisp/pfaengine/internal/kyc"
	"github.com/munisp/pfaengine/internal/liveness"
	"github.com/munisp/pfaengine/internal/persist"
)

var (
	ErrSessionNotFound = errors.New("ekyc: session not found")
	ErrBadTransition   = errors.New("ekyc: action not allowed in current state")
	ErrClientDisabled  = errors.New("ekyc: kyc-service client not configured")
	// ErrChallengeRequired fails passive-only biometric submission when the
	// tenant policy (or an issued challenge) demands active liveness.
	ErrChallengeRequired = errors.New("ekyc: challenge-response liveness is required for this session")
	// ErrLivenessNotConfigured fails challenge endpoints when no
	// challenge-response engine is wired (fail-closed).
	ErrLivenessNotConfigured = errors.New("ekyc: liveness challenge engine not configured")
)

// ---------------------------------------------------------------------------
// Document-intelligence client (fail-closed)
// ---------------------------------------------------------------------------

// ExtractResult is the parsed output of the sidecar /extract endpoint.
type ExtractResult struct {
	Fields     map[string]string `json:"fields"`
	RawText    string            `json:"raw_text"`
	Confidence float64           `json:"confidence"`
	VLMUsed    bool              `json:"vlm_used"`
}

// LivenessResult is the parsed output of the sidecar /liveness endpoint.
type LivenessResult struct {
	Live  bool      `json:"live"`
	Score float64   `json:"score"` // P(real) averaged over frames
	Probs []float64 `json:"probs"`
}

// FaceCompareResult is the parsed output of /face/compare (SFace cosine).
type FaceCompareResult struct {
	Match bool    `json:"match"`
	Score float64 `json:"score"`
}

// HealthResult mirrors the sidecar /healthz payload.
type HealthResult struct {
	OK            bool            `json:"ok"`
	ModelsPresent map[string]bool `json:"models_present"`
}

// DocIntel abstracts the sidecar so tests can stub it.
type DocIntel interface {
	Enabled() bool
	Extract(ctx context.Context, imageB64 string, useVLM bool) (ExtractResult, error)
	Liveness(ctx context.Context, frames []string) (LivenessResult, error)
	FaceCompare(ctx context.Context, selfieB64, idB64 string) (FaceCompareResult, error)
	Health(ctx context.Context) (HealthResult, error)
}

// Client is a fail-closed HTTP client for the Python kyc-service sidecar.
type Client struct {
	base string
	hc   *http.Client
}

// NewClient builds a client; an empty baseURL disables it (Enabled()==false).
func NewClient(baseURL string) *Client {
	return &Client{
		base: strings.TrimRight(baseURL, "/"),
		hc:   &http.Client{Timeout: 90 * time.Second},
	}
}

func (c *Client) Enabled() bool { return c != nil && c.base != "" }

func (c *Client) call(ctx context.Context, path string, payload any) (map[string]any, error) {
	if !c.Enabled() {
		return nil, ErrClientDisabled
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ekyc: kyc-service unreachable: %w", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("ekyc: bad sidecar response: %w", err)
	}
	// Fail-closed: the sidecar reports every internal error as ok:false.
	if ok, _ := out["ok"].(bool); !ok {
		msg, _ := out["error"].(string)
		return nil, fmt.Errorf("ekyc: sidecar error: %s", msg)
	}
	return out, nil
}

func get[T any](m map[string]any, key string, dst *T) {
	if v, ok := m[key]; ok {
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, dst)
	}
}

func (c *Client) Extract(ctx context.Context, imageB64 string, useVLM bool) (ExtractResult, error) {
	out, err := c.call(ctx, "/extract", map[string]any{"image_b64": imageB64, "use_vlm": useVLM})
	if err != nil {
		return ExtractResult{}, err
	}
	var r ExtractResult
	get(out, "fields", &r.Fields)
	get(out, "raw_text", &r.RawText)
	get(out, "confidence", &r.Confidence)
	get(out, "vlm_used", &r.VLMUsed)
	return r, nil
}

// Liveness runs the MiniFASNet ensemble once per frame and averages the
// real-face probability; a session is live only when every frame passes.
func (c *Client) Liveness(ctx context.Context, frames []string) (LivenessResult, error) {
	if len(frames) == 0 {
		return LivenessResult{}, errors.New("ekyc: no frames")
	}
	agg := LivenessResult{Live: true}
	for _, f := range frames {
		out, err := c.call(ctx, "/liveness", map[string]any{"image_b64": f})
		if err != nil {
			return LivenessResult{}, err
		}
		var fr LivenessResult
		get(out, "live", &fr.Live)
		get(out, "score", &fr.Score)
		get(out, "probs", &fr.Probs)
		agg.Score += fr.Score
		agg.Live = agg.Live && fr.Live
		if len(fr.Probs) == 3 {
			if agg.Probs == nil {
				agg.Probs = []float64{0, 0, 0}
			}
			for i := range fr.Probs {
				agg.Probs[i] += fr.Probs[i]
			}
		}
	}
	n := float64(len(frames))
	agg.Score /= n
	for i := range agg.Probs {
		agg.Probs[i] /= n
	}
	return agg, nil
}

func (c *Client) FaceCompare(ctx context.Context, selfieB64, idB64 string) (FaceCompareResult, error) {
	out, err := c.call(ctx, "/face/compare", map[string]any{"selfie_b64": selfieB64, "id_image_b64": idB64})
	if err != nil {
		return FaceCompareResult{}, err
	}
	var r FaceCompareResult
	get(out, "matched", &r.Match)
	get(out, "similarity", &r.Score)
	return r, nil
}

func (c *Client) Health(ctx context.Context) (HealthResult, error) {
	if !c.Enabled() {
		return HealthResult{OK: false, ModelsPresent: map[string]bool{}}, ErrClientDisabled
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/healthz", nil)
	if err != nil {
		return HealthResult{}, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return HealthResult{OK: false}, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return HealthResult{OK: false}, err
	}
	var h HealthResult
	get(out, "ok", &h.OK)
	get(out, "models_present", &h.ModelsPresent)
	return h, nil
}

// ---------------------------------------------------------------------------
// Verification session state machine (Ballerine-style)
// ---------------------------------------------------------------------------

type State string

const (
	StateDocumentCapture State = "DOCUMENT_CAPTURE"
	StateDocAnalysis     State = "DOC_ANALYSIS"
	StateIdentityCheck   State = "IDENTITY_CHECK"
	StateBiometric       State = "BIOMETRIC"
	StateDecided         State = "DECIDED"
)

type Decision string

const (
	DecisionNone         Decision = ""
	DecisionAutoApprove  Decision = "AUTO_APPROVE"
	DecisionManualReview Decision = "MANUAL_REVIEW"
	DecisionReject       Decision = "REJECT"
)

// DocumentEvidence captures one analysed identity document.
type DocumentEvidence struct {
	Kind         string            `json:"kind"`
	Ref          string            `json:"ref"`
	Fields       map[string]string `json:"fields,omitempty"`
	Confidence   float64           `json:"confidence"`
	VLMUsed      bool              `json:"vlm_used"`
	Expired      bool              `json:"expired"`
	AnalysedAt   time.Time         `json:"analysed_at"`
	IntelSkipped bool              `json:"intel_skipped"` // sidecar unavailable
}

type SessionEvent struct {
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Detail string    `json:"detail,omitempty"`
}

// Session is one end-to-end eKYC verification run for a member.
type Session struct {
	ID       string           `json:"id"` // ky-000001
	TenantID string           `json:"tenant_id"`
	MemberID string           `json:"member_id"`
	State    State            `json:"state"`
	Doc      DocumentEvidence `json:"document"`

	NINVerified  bool   `json:"nin_verified"`
	BVNVerified  bool   `json:"bvn_verified"`
	DuplicateOf  string `json:"duplicate_of,omitempty"`
	NameProvided string `json:"name_provided,omitempty"`
	DOBProvided  string `json:"dob_provided,omitempty"`

	LivenessScore  float64 `json:"liveness_score"`
	LivenessPassed bool    `json:"liveness_passed"`
	FaceScore      float64 `json:"face_score"`
	FaceMatched    bool    `json:"face_matched"`

	// Gap 9: challenge-response (active) liveness. ChallengeRequired is set
	// when a challenge is issued (or forced by tenant policy at engine
	// level); ChallengePassed/Score hold verdict metadata only — no
	// biometric material is ever stored on the session.
	ChallengeRequired bool    `json:"challenge_required,omitempty"`
	ChallengePassed   bool    `json:"challenge_passed,omitempty"`
	ChallengeScore    float64 `json:"challenge_score,omitempty"`
	ChallengeID       string  `json:"challenge_id,omitempty"`

	RiskScore int      `json:"risk_score"`
	RiskFlags []string `json:"risk_flags,omitempty"`
	Decision  Decision `json:"decision"`
	CaseID    string   `json:"case_id,omitempty"` // casemgmt KYC_REVIEW case

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DecidedAt time.Time      `json:"decided_at,omitempty"`
	History   []SessionEvent `json:"history,omitempty"`

	docImageB64 string // retained in-memory only, for face match
}

// Risk weights.
const (
	wDocIntelUnavailable = 40
	wOCRLowConfidence    = 25
	wDocExpired          = 30
	wNINUnverified       = 50
	wBVNUnverified       = 20
	wDuplicateIdentity   = 45
	wNameMismatch        = 30
	wDOBMismatch         = 25
	wFaceBorderline      = 30
	wLivenessBorderline  = 40
	wLivenessFail        = 100 // hard reject
	wFaceClearMismatch   = 100 // hard reject
	wChallengeFail       = 60  // active-liveness failure: reject w/ negative evidence
	wChallengeIncomplete = 40  // required challenge never passed: manual review

	threshManualReview = 25
	threshReject       = 60

	faceMatchThreshold    = 0.50 // SFace cosine (license-clean stack)
	faceClearMismatch     = 0.35
	livenessPassThreshold = 0.50 // P(real)
	ocrLowConfidence      = 0.60
)

// Engine runs verification sessions and routes manual-review outcomes into
// the unified case management engine.
type Engine struct {
	mu       sync.Mutex
	intel    DocIntel
	kyc      *kyc.Service
	cases    *casemgmt.Engine
	sessions map[string]*Session
	seq      int
	now      func() time.Time
	sink     persist.Sink // nil in pure in-memory mode
	// Gap 9: active liveness. liveness is the challenge-response engine
	// (nil disables challenge endpoints, fail-closed); requireChallenge is
	// the tenant policy (EKYC_REQUIRE_CHALLENGE) that blocks the passive
	// SubmitBiometric path and forces Decide to manual review until a
	// challenge passes.
	liveness         *liveness.Engine
	requireChallenge bool
}

func New(intel DocIntel, ks *kyc.Service, ce *casemgmt.Engine) *Engine {
	return &Engine{
		intel: intel, kyc: ks, cases: ce,
		sessions: map[string]*Session{},
		now:      func() time.Time { return time.Now().UTC() },
	}
}

// SetLiveness wires the Gap-9 challenge-response liveness engine.
func (e *Engine) SetLiveness(le *liveness.Engine) { e.mu.Lock(); e.liveness = le; e.mu.Unlock() }

// SetRequireChallenge toggles the tenant policy that mandates
// challenge-response liveness (EKYC_REQUIRE_CHALLENGE).
func (e *Engine) SetRequireChallenge(on bool) { e.mu.Lock(); e.requireChallenge = on; e.mu.Unlock() }

// AttachSink wires write-through snapshot persistence (NFR-10). Only the
// exported Session fields persist — raw document/selfie images never leave
// process memory.
func (e *Engine) AttachSink(s persist.Sink) { e.sink = s }

// Restore rehydrates sessions from boot snapshots; foreign kinds ignored.
// Mid-flight sessions restore without their raw images, so a subsequent
// biometric step skips the face compare against the document portrait and
// routes to manual review instead of failing.
func (e *Engine) Restore(snaps []persist.Snapshot) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range persist.OfKind(snaps, "ekyc.session") {
		var sess Session
		if err := json.Unmarshal(s.Payload, &sess); err != nil {
			return err
		}
		sc := sess
		e.sessions[sess.ID] = &sc
		if n, err := strconv.Atoi(strings.TrimPrefix(sess.ID, "ky-")); err == nil && n > e.seq {
			e.seq = n
		}
	}
	return nil
}

// persistAfter persists the returned session when err == nil. Register
// BEFORE the mutex-unlock defer so it runs after the lock is released.
func (e *Engine) persistAfter(sess *Session, err *error) {
	if *err == nil && sess != nil && sess.ID != "" {
		persist.Save(e.sink, "ekyc.session", sess.TenantID, sess.ID, *sess)
	}
}

// SetClock overrides the clock (tests).
func (e *Engine) SetClock(f func() time.Time) { e.mu.Lock(); e.now = f; e.mu.Unlock() }

// Start opens a new verification session for a member.
func (e *Engine) Start(tenantID, memberID, actor string) Session {
	e.mu.Lock()
	e.seq++
	s := &Session{
		ID:       fmt.Sprintf("ky-%06d", e.seq),
		TenantID: tenantID, MemberID: memberID,
		State:     StateDocumentCapture,
		CreatedAt: e.now(), UpdatedAt: e.now(),
	}
	e.logLocked(s, actor, "STARTED", "verification session opened")
	e.sessions[s.ID] = s
	out := *s
	e.mu.Unlock()
	persist.Save(e.sink, "ekyc.session", tenantID, out.ID, out)
	return out
}

func (e *Engine) Get(tenantID, id string) (Session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.sessions[id]
	if !ok || s.TenantID != tenantID {
		return Session{}, ErrSessionNotFound
	}
	return *s, nil
}

// List returns sessions, optionally filtered by state, newest first.
func (e *Engine) List(tenantID string, state string) []Session {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Session
	for _, s := range e.sessions {
		if s.TenantID != tenantID {
			continue
		}
		if state != "" && string(s.State) != state && string(s.Decision) != state {
			continue
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (e *Engine) get(tenantID, id string) (*Session, error) {
	s, ok := e.sessions[id]
	if !ok || s.TenantID != tenantID {
		return nil, ErrSessionNotFound
	}
	return s, nil
}

func (e *Engine) logLocked(s *Session, actor, action, detail string) {
	if actor == "" {
		actor = "system"
	}
	s.History = append(s.History, SessionEvent{At: e.now(), Actor: actor, Action: action, Detail: detail})
	s.UpdatedAt = e.now()
}

func (e *Engine) flagLocked(s *Session, flag string, weight int) {
	for _, f := range s.RiskFlags {
		if f == flag {
			return
		}
	}
	s.RiskFlags = append(s.RiskFlags, flag)
	s.RiskScore += weight
}

// SubmitDocument registers the identity document and runs document
// intelligence (OCR + field extraction, optional VLM cross-validation).
// imageB64 may be empty (ref-only submissions) — the session then relies on
// manual review. Advances DOCUMENT_CAPTURE -> DOC_ANALYSIS.
func (e *Engine) SubmitDocument(tenantID, id string, kind docupload.Kind, ref, imageB64 string, useVLM bool, actor string) (sess Session, err error) {
	defer func() { e.persistAfter(&sess, &err) }()
	e.mu.Lock()
	defer e.mu.Unlock()
	s, err := e.get(tenantID, id)
	if err != nil {
		return Session{}, err
	}
	if s.State != StateDocumentCapture {
		return Session{}, ErrBadTransition
	}
	s.Doc = DocumentEvidence{Kind: string(kind), Ref: ref, AnalysedAt: e.now()}
	if imageB64 != "" {
		s.docImageB64 = imageB64
		if e.intel != nil && e.intel.Enabled() {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			res, err := e.intel.Extract(ctx, imageB64, useVLM)
			cancel()
			if err != nil {
				s.Doc.IntelSkipped = true
				e.flagLocked(s, "doc_intel_unavailable", wDocIntelUnavailable)
				e.logLocked(s, actor, "DOC_INTEL_ERROR", err.Error())
			} else {
				s.Doc.Fields = res.Fields
				s.Doc.Confidence = res.Confidence
				s.Doc.VLMUsed = res.VLMUsed
				if res.Confidence < ocrLowConfidence {
					e.flagLocked(s, "ocr_low_confidence", wOCRLowConfidence)
				}
				if exp, ok := res.Fields["expiry_date"]; ok && exp != "" {
					if t, perr := time.Parse("2006-01-02", exp); perr == nil && t.Before(e.now()) {
						s.Doc.Expired = true
						e.flagLocked(s, "document_expired", wDocExpired)
					}
				}
				e.logLocked(s, actor, "DOC_ANALYSED",
					fmt.Sprintf("kind=%s confidence=%.2f fields=%d vlm=%v", kind, res.Confidence, len(res.Fields), res.VLMUsed))
			}
		} else {
			s.Doc.IntelSkipped = true
			e.flagLocked(s, "doc_intel_unavailable", wDocIntelUnavailable)
		}
	} else {
		s.Doc.IntelSkipped = true
		e.flagLocked(s, "no_document_image", wDocIntelUnavailable)
	}
	s.State = StateDocAnalysis
	e.logLocked(s, actor, "STATE", "-> DOC_ANALYSIS")
	return *s, nil
}

// VerifyIdentity cross-checks the extracted document against NIN and BVN via
// internal/kyc, and checks the doc-extracted identifiers against the
// applicant-supplied ones. Advances DOC_ANALYSIS -> IDENTITY_CHECK.
func (e *Engine) VerifyIdentity(tenantID, id, nin, bvn, name, dob, phone, actor string) (sess Session, err error) {
	defer func() { e.persistAfter(&sess, &err) }()
	e.mu.Lock()
	defer e.mu.Unlock()
	s, err := e.get(tenantID, id)
	if err != nil {
		return Session{}, err
	}
	if s.State != StateDocAnalysis {
		return Session{}, ErrBadTransition
	}
	s.NameProvided, s.DOBProvided = name, dob

	if e.kyc != nil && nin != "" {
		_, dup, err := e.kyc.VerifyNIN(tenantID, s.MemberID, nin, name, dob)
		switch {
		case err != nil:
			e.flagLocked(s, "nin_unverified", wNINUnverified)
			e.logLocked(s, actor, "NIN_FAIL", err.Error())
		default:
			s.NINVerified = true
			if dup != "" {
				s.DuplicateOf = dup
				e.flagLocked(s, "duplicate_identity", wDuplicateIdentity)
			}
		}
	} else if nin != "" {
		e.flagLocked(s, "nin_unverified", wNINUnverified)
	}
	if e.kyc != nil && bvn != "" {
		if _, _, err := e.kyc.ValidateBVN(tenantID, s.MemberID, bvn, name, dob, phone); err != nil {
			e.flagLocked(s, "bvn_unverified", wBVNUnverified)
		} else {
			s.BVNVerified = true
		}
	}

	// Cross-check document-extracted fields against supplied identity.
	if docNIN := s.Doc.Fields["nin"]; docNIN != "" && nin != "" && docNIN != nin {
		e.flagLocked(s, "nin_doc_mismatch", wNINUnverified)
	}
	if docName := s.Doc.Fields["full_name"]; docName != "" && name != "" && !nameTokensOverlap(docName, name) {
		e.flagLocked(s, "name_mismatch", wNameMismatch)
	}
	if docDOB := s.Doc.Fields["date_of_birth"]; docDOB != "" && dob != "" && docDOB != dob {
		e.flagLocked(s, "dob_mismatch", wDOBMismatch)
	}
	s.State = StateIdentityCheck
	e.logLocked(s, actor, "STATE", "-> IDENTITY_CHECK")
	return *s, nil
}

func nameTokensOverlap(a, b string) bool {
	ta := strings.Fields(strings.ToLower(a))
	tb := map[string]bool{}
	for _, t := range strings.Fields(strings.ToLower(b)) {
		tb[t] = true
	}
	match := 0
	for _, t := range ta {
		if tb[t] {
			match++
		}
	}
	return len(ta) > 0 && match*2 >= len(ta) // >=50% of doc tokens present
}

// SubmitBiometric runs passive liveness on the selfie frames and face-matches
// the selfie against the identity document portrait. Advances
// IDENTITY_CHECK -> BIOMETRIC.
func (e *Engine) SubmitBiometric(tenantID, id string, frames []string, actor string) (sess Session, err error) {
	defer func() { e.persistAfter(&sess, &err) }()
	e.mu.Lock()
	defer e.mu.Unlock()
	s, err := e.get(tenantID, id)
	if err != nil {
		return Session{}, err
	}
	if s.State != StateIdentityCheck {
		return Session{}, ErrBadTransition
	}
	// Gap 9 fail-closed: when active liveness is required (tenant policy or
	// a challenge already issued for this session) and no challenge has
	// passed yet, the passive path is closed — use IssueChallenge +
	// SubmitChallengeResponse instead.
	if (e.requireChallenge || s.ChallengeRequired) && !s.ChallengePassed {
		return Session{}, ErrChallengeRequired
	}
	if len(frames) == 0 {
		return Session{}, errors.New("ekyc: at least one selfie frame is required")
	}
	if e.intel == nil || !e.intel.Enabled() {
		e.flagLocked(s, "biometrics_unavailable", wDocIntelUnavailable)
		s.State = StateBiometric
		e.logLocked(s, actor, "BIOMETRIC_SKIPPED", "sidecar unavailable")
		return *s, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	lv, err := e.intel.Liveness(ctx, frames)
	if err != nil {
		e.flagLocked(s, "liveness_error", wDocIntelUnavailable)
		e.logLocked(s, actor, "LIVENESS_ERROR", err.Error())
	} else {
		s.LivenessScore = lv.Score
		s.LivenessPassed = lv.Live && lv.Score >= livenessPassThreshold
		switch {
		case lv.Score < livenessPassThreshold:
			e.flagLocked(s, "liveness_fail", wLivenessFail)
		case lv.Score < 0.70:
			e.flagLocked(s, "liveness_borderline", wLivenessBorderline)
		}
		e.logLocked(s, actor, "LIVENESS", fmt.Sprintf("score=%.3f live=%v", lv.Score, lv.Live))
	}

	if s.docImageB64 != "" && len(frames) > 0 {
		fc, err := e.intel.FaceCompare(ctx, frames[0], s.docImageB64)
		if err != nil {
			e.flagLocked(s, "face_compare_error", wFaceBorderline)
			e.logLocked(s, actor, "FACE_ERROR", err.Error())
		} else {
			s.FaceScore = fc.Score
			s.FaceMatched = fc.Match && fc.Score >= faceMatchThreshold
			switch {
			case fc.Score < faceClearMismatch:
				e.flagLocked(s, "face_clear_mismatch", wFaceClearMismatch)
			case fc.Score < faceMatchThreshold:
				e.flagLocked(s, "face_borderline", wFaceBorderline)
			}
			e.logLocked(s, actor, "FACE_MATCH", fmt.Sprintf("score=%.3f match=%v", fc.Score, fc.Match))
		}
	}
	s.State = StateBiometric
	e.logLocked(s, actor, "STATE", "-> BIOMETRIC")
	return *s, nil
}

// ---------------------------------------------------------------------------
// Gap 9: challenge-response (active) liveness
// ---------------------------------------------------------------------------

// ChallengeView is the client-facing rendering of an issued challenge. It
// deliberately exposes ONLY the action list, nonce and expiry — internal
// fields (Used, engine state) never leave the engine.
type ChallengeView struct {
	ID        string            `json:"id"`
	SessionID string            `json:"session_id"`
	Actions   []liveness.Action `json:"actions"`
	Nonce     string            `json:"nonce"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// IssueChallenge enters (or re-arms) the active-liveness step. Guard: the
// session must be in IDENTITY_CHECK (transitions to BIOMETRIC with a
// REQUIRED challenge) or already in BIOMETRIC (re-issue; the liveness
// engine invalidates the prior challenge). Issuing a challenge marks the
// session challenge-required, which closes the passive SubmitBiometric
// path fail-closed.
func (e *Engine) IssueChallenge(tenantID, id, actor string) (view ChallengeView, err error) {
	var sess Session
	defer func() { e.persistAfter(&sess, &err) }()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.liveness == nil {
		return ChallengeView{}, ErrLivenessNotConfigured
	}
	s, err := e.get(tenantID, id)
	if err != nil {
		return ChallengeView{}, err
	}
	if s.State != StateIdentityCheck && s.State != StateBiometric {
		return ChallengeView{}, ErrBadTransition
	}
	ch, err := e.liveness.Issue(id)
	if err != nil {
		return ChallengeView{}, err
	}
	s.ChallengeRequired = true
	s.ChallengeID = ch.ID
	if s.State == StateIdentityCheck {
		s.State = StateBiometric
		e.logLocked(s, actor, "STATE", "-> BIOMETRIC (challenge required)")
	}
	e.logLocked(s, actor, "CHALLENGE_ISSUED",
		fmt.Sprintf("challenge=%s actions=%d expires=%s", ch.ID, len(ch.Actions), ch.ExpiresAt.Format(time.RFC3339)))
	sess = *s
	return ChallengeView{
		ID: ch.ID, SessionID: ch.SessionID, Actions: ch.Actions,
		Nonce: ch.Nonce, ExpiresAt: ch.ExpiresAt,
	}, nil
}

// SubmitChallengeResponse verifies a nonce-bound challenge response through
// the liveness engine and records the verdict metadata on the session.
// PASSED sets LivenessPassed (and the challenge score); FAIL flags
// challenge_failed (weight 60 → REJECT with negative evidence). Binding,
// single-use and expiry errors (nonce mismatch, replay, expired) are
// returned to the caller and leave the session untouched.
func (e *Engine) SubmitChallengeResponse(tenantID, id, challengeID, nonce string, results []liveness.ActionResult, actor string) (sess Session, err error) {
	defer func() { e.persistAfter(&sess, &err) }()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.liveness == nil {
		return Session{}, ErrLivenessNotConfigured
	}
	s, err := e.get(tenantID, id)
	if err != nil {
		return Session{}, err
	}
	if s.State != StateBiometric {
		return Session{}, ErrBadTransition
	}
	v, err := e.liveness.Respond(challengeID, nonce, results)
	if err != nil {
		return Session{}, err
	}
	if v.SessionID != id {
		// A challenge minted for another session must never advance this one.
		return Session{}, fmt.Errorf("ekyc: challenge %s does not belong to session %s", challengeID, id)
	}
	s.ChallengeScore = v.Score
	s.ChallengeID = v.ChallengeID
	if v.Passed {
		s.ChallengePassed = true
		s.LivenessPassed = true
		s.LivenessScore = v.Score
		e.logLocked(s, actor, "CHALLENGE_PASSED", fmt.Sprintf("challenge_score=%.3f actions=%d", v.Score, len(v.Actions)))
	} else {
		e.flagLocked(s, "challenge_failed", wChallengeFail)
		e.logLocked(s, actor, "CHALLENGE_FAILED", fmt.Sprintf("challenge_score=%.3f actions=%d", v.Score, len(v.Actions)))
	}
	return *s, nil
}

// Decide scores the accumulated risk and reaches a decision. MANUAL_REVIEW
// sessions open a casemgmt KYC_REVIEW case for the ops team. Advances
// BIOMETRIC -> DECIDED.
func (e *Engine) Decide(tenantID, id, actor string) (sess Session, err error) {
	defer func() { e.persistAfter(&sess, &err) }()
	e.mu.Lock()
	defer e.mu.Unlock()
	s, err := e.get(tenantID, id)
	if err != nil {
		return Session{}, err
	}
	if s.State != StateBiometric {
		return Session{}, ErrBadTransition
	}

	// Gap 9 fail-closed: a session whose policy demands active liveness but
	// which never passed a challenge must route to a human, never
	// auto-approve.
	if (e.requireChallenge || s.ChallengeRequired) && !s.ChallengePassed {
		e.flagLocked(s, "challenge_incomplete", wChallengeIncomplete)
		e.logLocked(s, actor, "CHALLENGE_INCOMPLETE", "active liveness required but not passed")
	}

	hardReject := false
	negativeEvidence := false
	for _, f := range s.RiskFlags {
		switch f {
		case "liveness_fail", "face_clear_mismatch":
			hardReject = true
			negativeEvidence = true
		case "doc_intel_unavailable", "biometrics_unavailable", "no_document_image",
			"liveness_error", "face_compare_error":
			// Fail-closed: unavailability alone must route to a human,
			// never to an outright rejection.
		default:
			negativeEvidence = true
		}
	}
	switch {
	case hardReject || (s.RiskScore >= threshReject && negativeEvidence):
		s.Decision = DecisionReject
	case s.RiskScore >= threshManualReview:
		s.Decision = DecisionManualReview
	default:
		s.Decision = DecisionAutoApprove
	}
	s.DecidedAt = e.now()
	s.State = StateDecided

	if s.Decision == DecisionManualReview && e.cases != nil {
		c, _, cerr := e.cases.Open(casemgmt.OpenInput{
			TenantID:    tenantID,
			Type:        casemgmt.TypeKYCReview,
			Subject:     "eKYC manual review: " + s.MemberID,
			Description: fmt.Sprintf("Verification session %s risk=%d flags=%v", s.ID, s.RiskScore, s.RiskFlags),
			MemberID:    s.MemberID,
			LinkedKind:  "EKYC_SESSION",
			LinkedID:    s.ID,
			Source:      casemgmt.SourceAPI,
			Actor:       actor,
			NoDedup:     true,
		})
		if cerr == nil {
			s.CaseID = c.ID
		}
	}
	e.logLocked(s, actor, "DECIDED", fmt.Sprintf("decision=%s risk=%d flags=%v case=%s",
		s.Decision, s.RiskScore, s.RiskFlags, s.CaseID))
	return *s, nil
}

// Override lets staff approve or reject a manual-review session, resolving
// the linked KYC_REVIEW case when one exists.
func (e *Engine) Override(tenantID, id string, approve bool, actor, note string) (sess Session, err error) {
	defer func() { e.persistAfter(&sess, &err) }()
	e.mu.Lock()
	defer e.mu.Unlock()
	s, err := e.get(tenantID, id)
	if err != nil {
		return Session{}, err
	}
	if s.State != StateDecided || s.Decision != DecisionManualReview {
		return Session{}, ErrBadTransition
	}
	if approve {
		s.Decision = DecisionAutoApprove
	} else {
		s.Decision = DecisionReject
	}
	if s.CaseID != "" && e.cases != nil {
		code := "VERIFIED"
		if !approve {
			code = "REJECTED"
		}
		_, _ = e.cases.Resolve(tenantID, s.CaseID, note, code, actor)
	}
	e.logLocked(s, actor, "OVERRIDE", fmt.Sprintf("approve=%v note=%s", approve, note))
	return *s, nil
}
