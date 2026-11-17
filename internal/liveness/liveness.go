// Package liveness implements Gap 9: challenge-response (active)
// anti-spoofing for PenCom-grade high-assurance eKYC flows (account opening,
// benefit withdrawal). Passive liveness (MiniFASNet frame scoring in
// internal/ekyc) is spoofable by replayed video; this engine instead issues
// a short-lived, nonce-bound challenge of randomized actions (BLINK,
// TURN_LEFT, TURN_RIGHT, SMILE, NOD) that the client must perform live.
//
// Security properties:
//   - Nonces and action selection use crypto/rand only.
//   - One active challenge per session: re-issue invalidates the prior one.
//   - Challenges are single-use and expire (default TTL 120s).
//   - Nonces are compared in constant time.
//   - Nothing biometric (frames, selfies, b64 payloads) is ever stored,
//     persisted or logged here — only verdict metadata (score, pass/fail).
//
// Vendor-backed verification is available through the Provider interface;
// FromEnv returns a Smile ID SmartSelfie adapter when SMILE_ID_PARTNER_ID
// and SMILE_ID_API_KEY are set, else nil (mock/local-only mode).
package liveness

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Action is one challenge action the user must perform live on camera.
type Action string

const (
	ActionBlink     Action = "BLINK"
	ActionTurnLeft  Action = "TURN_LEFT"
	ActionTurnRight Action = "TURN_RIGHT"
	ActionSmile     Action = "SMILE"
	ActionNod       Action = "NOD"
)

// allActions is the pool challenges are sampled from.
var allActions = []Action{ActionBlink, ActionTurnLeft, ActionTurnRight, ActionSmile, ActionNod}

const (
	// DefaultThreshold is the minimum mean action score for a PASSED verdict.
	DefaultThreshold = 0.60
	// DefaultTTL bounds how long a challenge stays usable.
	DefaultTTL = 120 * time.Second
	// nonceBytes is the raw entropy behind the hex-encoded challenge nonce.
	nonceBytes = 16
)

var (
	ErrChallengeNotFound   = errors.New("liveness: challenge not found")
	ErrNonceMismatch       = errors.New("liveness: nonce mismatch")
	ErrChallengeExpired    = errors.New("liveness: challenge expired")
	ErrChallengeUsed       = errors.New("liveness: challenge already used")
	ErrIncompleteResponse  = errors.New("liveness: response does not cover every challenged action")
	ErrEmptySession        = errors.New("liveness: session id required")
	ErrProviderUnavailable = errors.New("liveness: no verification provider configured")
)

// Challenge is a single-use, expiring, nonce-bound active-liveness prompt.
// It carries no biometric material — only the action list and metadata.
type Challenge struct {
	ID        string    `json:"id"` // lc-<16 hex>
	SessionID string    `json:"session_id"`
	Actions   []Action  `json:"actions"` // 2-3 randomized distinct actions
	Nonce     string    `json:"nonce"`   // hex, 16 bytes of crypto/rand entropy
	ExpiresAt time.Time `json:"expires_at"`
	Used      bool      `json:"used"`
	CreatedAt time.Time `json:"created_at"`
}

// ActionResult is the client-observed outcome of one challenged action.
type ActionResult struct {
	Action Action  `json:"action"`
	Passed bool    `json:"passed"`
	Score  float64 `json:"score"`
}

// Verdict is the outcome of responding to a challenge. Only verdict
// metadata — never imagery — is produced or persisted.
type Verdict struct {
	ChallengeID string   `json:"challenge_id"`
	SessionID   string   `json:"session_id"`
	Actions     []Action `json:"actions"`
	Score       float64  `json:"score"` // mean action score (or vendor score)
	Passed      bool     `json:"passed"`
}

// Engine issues and verifies active-liveness challenges. Safe for
// concurrent use; state is in-memory only (challenge payloads contain no
// PII or biometric data, and sessions re-issue after a restart).
type Engine struct {
	mu              sync.Mutex
	byID            map[string]*Challenge
	activeBySession map[string]string // sessionID -> active challenge ID
	threshold       float64
	ttl             time.Duration
	now             func() time.Time
	rand            io.Reader
	provider        Provider
}

// New builds an engine. A threshold <= 0 or > 1 falls back to
// DefaultThreshold (0.60); use ThresholdFromEnv for LIVENESS_THRESHOLD.
func New(threshold float64) *Engine {
	if threshold <= 0 || threshold > 1 {
		threshold = DefaultThreshold
	}
	return &Engine{
		byID:            map[string]*Challenge{},
		activeBySession: map[string]string{},
		threshold:       threshold,
		ttl:             DefaultTTL,
		now:             func() time.Time { return time.Now().UTC() },
		rand:            rand.Reader,
	}
}

// ThresholdFromEnv parses LIVENESS_THRESHOLD (0-1); unset or invalid values
// yield DefaultThreshold.
func ThresholdFromEnv() float64 {
	if v := strings.TrimSpace(os.Getenv("LIVENESS_THRESHOLD")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f <= 1 {
			return f
		}
	}
	return DefaultThreshold
}

// SetClock overrides the clock (tests / expiry injection).
func (e *Engine) SetClock(f func() time.Time) { e.mu.Lock(); e.now = f; e.mu.Unlock() }

// SetTTL overrides the challenge TTL (tests).
func (e *Engine) SetTTL(d time.Duration) { e.mu.Lock(); e.ttl = d; e.mu.Unlock() }

// SetRand overrides the entropy source (tests). Must remain crypto/rand in
// production.
func (e *Engine) SetRand(r io.Reader) { e.mu.Lock(); e.rand = r; e.mu.Unlock() }

// SetProvider wires an optional vendor verifier (e.g. Smile ID SmartSelfie)
// used by RespondWithProvider.
func (e *Engine) SetProvider(p Provider) { e.mu.Lock(); e.provider = p; e.mu.Unlock() }

// randIntn returns a uniform value in [0, n) from the engine entropy source.
func (e *Engine) randIntn(n int) (int, error) {
	v, err := rand.Int(e.rand, big.NewInt(int64(n)))
	if err != nil {
		return 0, fmt.Errorf("liveness: entropy failure: %w", err)
	}
	return int(v.Int64()), nil
}

// sampleActions picks k distinct actions uniformly at random (Fisher-Yates
// over a copy of the pool).
func (e *Engine) sampleActions(k int) ([]Action, error) {
	pool := append([]Action(nil), allActions...)
	for i := len(pool) - 1; i > 0; i-- {
		j, err := e.randIntn(i + 1)
		if err != nil {
			return nil, err
		}
		pool[i], pool[j] = pool[j], pool[i]
	}
	return pool[:k], nil
}

func (e *Engine) randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(e.rand, b); err != nil {
		return "", fmt.Errorf("liveness: entropy failure: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Issue creates a fresh challenge for a session. At most one challenge is
// active per session: re-issuing invalidates (deletes) the prior one, so a
// stolen or stale challenge can never be replayed after a re-issue.
func (e *Engine) Issue(sessionID string) (Challenge, error) {
	if strings.TrimSpace(sessionID) == "" {
		return Challenge{}, ErrEmptySession
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	// 2 or 3 actions, distinct, uniformly random.
	kN, err := e.randIntn(2)
	if err != nil {
		return Challenge{}, err
	}
	actions, err := e.sampleActions(2 + kN)
	if err != nil {
		return Challenge{}, err
	}
	nonce, err := e.randomHex(nonceBytes)
	if err != nil {
		return Challenge{}, err
	}
	idHex, err := e.randomHex(8)
	if err != nil {
		return Challenge{}, err
	}
	now := e.now()
	c := &Challenge{
		ID:        "lc-" + idHex,
		SessionID: sessionID,
		Actions:   actions,
		Nonce:     nonce,
		CreatedAt: now,
		ExpiresAt: now.Add(e.ttl),
	}
	// One active challenge per session: invalidate any prior challenge.
	if prev, ok := e.activeBySession[sessionID]; ok {
		delete(e.byID, prev)
	}
	e.byID[c.ID] = c
	e.activeBySession[sessionID] = c.ID
	return *c, nil
}

// Verify validates a challenge binding (id + nonce), enforces single-use
// and expiry, and CONSUMES the challenge on success. The returned copy is
// the verified challenge metadata.
func (e *Engine) Verify(challengeID, nonce string) (Challenge, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.byID[challengeID]
	if !ok {
		return Challenge{}, ErrChallengeNotFound
	}
	// Constant-time nonce compare before revealing any other state.
	if subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(nonce)) != 1 {
		return Challenge{}, ErrNonceMismatch
	}
	if c.Used {
		return Challenge{}, ErrChallengeUsed
	}
	if !e.now().Before(c.ExpiresAt) {
		return Challenge{}, ErrChallengeExpired
	}
	c.Used = true
	return *c, nil
}

// Respond verifies the challenge (nonce-bound, single-use, expiry-enforced)
// and scores the client-reported per-action results. Verdict rule: EVERY
// challenged action must be reported and passed, AND the mean score must be
// >= the engine threshold. The challenge is consumed even when the response
// is incomplete (fail-closed: no partial-probe retries).
func (e *Engine) Respond(challengeID, nonce string, results []ActionResult) (Verdict, error) {
	c, err := e.Verify(challengeID, nonce)
	if err != nil {
		return Verdict{}, err
	}
	got := map[Action]ActionResult{}
	for _, r := range results {
		got[r.Action] = r
	}
	allPassed := true
	sum := 0.0
	for _, a := range c.Actions {
		r, ok := got[a]
		if !ok {
			return Verdict{ChallengeID: c.ID, SessionID: c.SessionID, Actions: c.Actions}, ErrIncompleteResponse
		}
		allPassed = allPassed && r.Passed
		sum += r.Score
	}
	mean := sum / float64(len(c.Actions))
	e.mu.Lock()
	threshold := e.threshold
	e.mu.Unlock()
	return Verdict{
		ChallengeID: c.ID,
		SessionID:   c.SessionID,
		Actions:     c.Actions,
		Score:       mean,
		Passed:      allPassed && mean >= threshold,
	}, nil
}

// RespondWithProvider verifies the challenge locally (same binding rules as
// Respond) and then delegates action/liveness scoring to the configured
// vendor Provider with the client-captured selfie. The selfie is passed
// straight through to the vendor over TLS and is never stored or logged.
// Vendor errors are fail-closed (verdict not passed).
func (e *Engine) RespondWithProvider(ctx context.Context, challengeID, nonce, selfieB64 string) (Verdict, error) {
	e.mu.Lock()
	p := e.provider
	e.mu.Unlock()
	if p == nil {
		return Verdict{}, ErrProviderUnavailable
	}
	c, err := e.Verify(challengeID, nonce)
	if err != nil {
		return Verdict{}, err
	}
	score, ok, err := p.VerifyActive(ctx, c.SessionID, c.Actions, selfieB64)
	if err != nil {
		return Verdict{ChallengeID: c.ID, SessionID: c.SessionID, Actions: c.Actions}, err
	}
	e.mu.Lock()
	threshold := e.threshold
	e.mu.Unlock()
	return Verdict{
		ChallengeID: c.ID,
		SessionID:   c.SessionID,
		Actions:     c.Actions,
		Score:       score,
		Passed:      ok && score >= threshold,
	}, nil
}

// ---------------------------------------------------------------------------
// Vendor provider (Smile ID SmartSelfie)
// ---------------------------------------------------------------------------

// Provider verifies an active-liveness challenge against a vendor API.
// Implementations must be fail-closed: any transport or 5xx failure is an
// error, never a pass.
type Provider interface {
	VerifyActive(ctx context.Context, sessionID string, actions []Action, selfieB64 string) (score float64, passed bool, err error)
}

// DefaultSmileIDBaseURL is the Smile ID API host (no version suffix; the
// adapter appends the assumed endpoint path below).
const DefaultSmileIDBaseURL = "https://api.smileidentity.com"

// SmileIDProvider is a Provider backed by Smile ID's SmartSelfie
// (active-liveness) API.
//
// ASSUMED-SPEC — pending vendor documentation: the request/response shapes
// below follow the Smile ID v2 conventions already used by
// internal/vendors (partner_id + timestamp + HMAC-SHA256 signature), POSTed
// to {SMILE_ID_BASE_URL}/v2/smart-selfie. Field names must be reconciled
// with the official SmartSelfie docs before production use; the adapter
// fails closed (no pass) on any unexpected response.
type SmileIDProvider struct {
	PartnerID string
	APIKey    string
	BaseURL   string
	HTTP      *http.Client
}

// FromEnv returns a Smile ID SmartSelfie provider when SMILE_ID_PARTNER_ID
// and SMILE_ID_API_KEY are both set (SMILE_ID_BASE_URL optional), else nil
// (mock/local-only mode). Partial configuration yields nil — never a
// half-configured adapter.
func FromEnv() Provider {
	partnerID, apiKey := os.Getenv("SMILE_ID_PARTNER_ID"), os.Getenv("SMILE_ID_API_KEY")
	if partnerID == "" || apiKey == "" {
		return nil
	}
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("SMILE_ID_BASE_URL")), "/")
	if base == "" {
		base = DefaultSmileIDBaseURL
	}
	return &SmileIDProvider{PartnerID: partnerID, APIKey: apiKey, BaseURL: base}
}

func (p *SmileIDProvider) client() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

// signature mirrors internal/vendors SmileID: HMAC-SHA256 over
// timestamp+partner_id+"sid_request", base64(hex).
func (p *SmileIDProvider) signature(ts time.Time) (string, string) {
	tss := ts.UTC().Format(time.RFC3339Nano)
	mac := hmac.New(sha256.New, []byte(p.APIKey))
	mac.Write([]byte(tss + p.PartnerID + "sid_request"))
	return tss, base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(mac.Sum(nil))))
}

// smileSelfieRequest/Response are ASSUMED-SPEC (see SmileIDProvider doc).
type smileSelfieRequest struct {
	PartnerID string   `json:"partner_id"`
	Timestamp string   `json:"timestamp"`
	Signature string   `json:"signature"`
	SessionID string   `json:"session_id"`
	JobType   string   `json:"job_type"` // "smart_selfie_active"
	Actions   []Action `json:"actions"`
	ImageB64  string   `json:"image_b64"`
}

type smileSelfieResponse struct {
	ResultCode   string  `json:"ResultCode"`
	ResultText   string  `json:"ResultText"`
	Confidence   float64 `json:"ConfidenceValue"` // 0-100 per Smile ID convention
	LivenessPass bool    `json:"LivenessPassed"`
}

func (p *SmileIDProvider) VerifyActive(ctx context.Context, sessionID string, actions []Action, selfieB64 string) (float64, bool, error) {
	ts, sig := p.signature(time.Now())
	body, _ := json.Marshal(smileSelfieRequest{
		PartnerID: p.PartnerID, Timestamp: ts, Signature: sig,
		SessionID: sessionID, JobType: "smart_selfie_active",
		Actions: actions, ImageB64: selfieB64,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/v2/smart-selfie", bytes.NewReader(body))
	if err != nil {
		return 0, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client().Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("liveness: smileid unreachable (fail-closed): %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode >= 500:
		// Fail-closed on vendor 5xx: transient-looking failures must never
		// silently pass.
		return 0, false, fmt.Errorf("liveness: smileid 5xx (fail-closed): %s", resp.Status)
	case resp.StatusCode >= 300:
		return 0, false, fmt.Errorf("liveness: smileid rejected request: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var out smileSelfieResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, false, fmt.Errorf("liveness: smileid bad response (fail-closed): %w", err)
	}
	// ASSUMED-SPEC: 0810/1012 are the Smile ID "verified" codes used by the
	// existing vendors adapter; anything else is a non-pass (not an error).
	passed := out.LivenessPass && (out.ResultCode == "0810" || out.ResultCode == "1012")
	return out.Confidence / 100.0, passed, nil
}
