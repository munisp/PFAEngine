// Package kyc implements FR-101..109 and INT-02..04: NIN verification via
// licensed aggregators (tokenized), BVN validation (salted-hash only),
// PAD-certified liveness with regulator thresholds, duplicate detection,
// NDPA consent lifecycle, agent-assisted enrollment, and I-Am-Alive reuse.
package kyc

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrProviderUnavailable = errors.New("kyc: all aggregators unavailable; queued for retry")
	ErrRetryCapExceeded    = errors.New("kyc: liveness retry cap (3) exceeded")
	ErrVerificationFailed  = errors.New("kyc: verification failed")
)

// ---- Provider facade (INT-02/03/04): ≥2 aggregators from day one ----

type VerifyResult struct {
	ProviderRef string  `json:"provider_ref"`
	Token       string  `json:"token"` // provider token, never raw NIN (FR-101)
	Matched     bool    `json:"matched"`
	Score       float64 `json:"score"`
	EvidenceRef string  `json:"evidence_ref"`
}

type Provider interface {
	Name() string
	VerifyNIN(nin, name, dob string) (VerifyResult, error)
	ValidateBVN(bvn, name, dob, phone string) (VerifyResult, error)
	LivenessFaceMatch(selfieRef, ninToken string) (score float64, evidenceRef string, err error)
}

// MockProvider is deterministic for tests/dev (scores derived from input).
type MockProvider struct {
	ProviderName string
	FailAll      bool
}

func (m MockProvider) Name() string { return m.ProviderName }

func (m MockProvider) VerifyNIN(nin, name, dob string) (VerifyResult, error) {
	if m.FailAll {
		return VerifyResult{}, ErrProviderUnavailable
	}
	if len(nin) != 11 {
		return VerifyResult{Matched: false}, nil
	}
	tok := sha256.Sum256([]byte(m.ProviderName + "|" + nin))
	return VerifyResult{
		ProviderRef: fmt.Sprintf("%s-%d", m.ProviderName, time.Now().UnixNano()),
		Token:       hex.EncodeToString(tok[:]),
		Matched:     true, Score: 1.0,
		EvidenceRef: "ev://" + m.ProviderName + "/nin/" + hex.EncodeToString(tok[:8]),
	}, nil
}

func (m MockProvider) ValidateBVN(bvn, name, dob, phone string) (VerifyResult, error) {
	if m.FailAll {
		return VerifyResult{}, ErrProviderUnavailable
	}
	if len(bvn) != 11 {
		return VerifyResult{Matched: false}, nil
	}
	return VerifyResult{
		ProviderRef: fmt.Sprintf("%s-bvn-%d", m.ProviderName, time.Now().UnixNano()),
		Matched:     true, Score: 1.0,
		EvidenceRef: "ev://" + m.ProviderName + "/bvn/" + bvn[:4],
	}, nil
}

// LivenessFaceMatch returns a deterministic PAD score for tests:
// selfieRef "spoof*" -> 0.30 (fail), "blur*" -> 0.70 (manual review), else 0.95.
func (m MockProvider) LivenessFaceMatch(selfieRef, ninToken string) (float64, string, error) {
	if m.FailAll {
		return 0, "", ErrProviderUnavailable
	}
	switch {
	case len(selfieRef) >= 5 && selfieRef[:5] == "spoof":
		return 0.30, "ev://liveness/spoof", nil
	case len(selfieRef) >= 4 && selfieRef[:4] == "blur":
		return 0.70, "ev://liveness/blur", nil
	default:
		return 0.95, "ev://liveness/pass", nil
	}
}

// ---- Verification records ----

type LivenessKind string

const (
	KindEnroll   LivenessKind = "ENROLL"
	KindIAmAlive LivenessKind = "IAM_ALIVE" // FR-108
)

type LivenessStatus string

const (
	StatusPassed       LivenessStatus = "PASSED"
	StatusManualReview LivenessStatus = "MANUAL_REVIEW"
	StatusFailed       LivenessStatus = "FAILED"
)

type LivenessCheck struct {
	ID          string         `json:"id"`
	TenantID    string         `json:"tenant_id"`
	PersonID    string         `json:"person_id"`
	Kind        LivenessKind   `json:"kind"`
	Score       float64        `json:"score"`
	Status      LivenessStatus `json:"status"`
	Attempts    int            `json:"attempts"`
	EvidenceRef string         `json:"evidence_ref"`
	At          time.Time      `json:"at"`
}

type NINRecord struct {
	PersonID    string    `json:"person_id"`
	Token       string    `json:"token"` // provider token only (FR-101)
	ProviderRef string    `json:"provider_ref"`
	EvidenceRef string    `json:"evidence_ref"`
	NINHash     string    `json:"nin_hash"` // platform-wide salted hash for dup detection (FR-105)
	VerifiedAt  time.Time `json:"verified_at"`
}

type BVNRecord struct {
	PersonID    string    `json:"person_id"`
	BVNHash     string    `json:"bvn_hash"` // salted hash only (FR-102)
	PhoneBound  string    `json:"phone_bound"`
	EvidenceRef string    `json:"evidence_ref"`
	VerifiedAt  time.Time `json:"verified_at"`
}

type Consent struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	PersonID       string    `json:"person_id"`
	Purpose        string    `json:"purpose"`
	WordingVersion string    `json:"wording_version"`
	Granted        bool      `json:"granted"`
	WithdrawnAt    time.Time `json:"withdrawn_at,omitempty"`
	At             time.Time `json:"at"`
}

// Service is the kyc-service facade (§8.3): all four verification flows
// behind one interface with aggregator failover and queue-on-degradation.
type Service struct {
	mu            sync.Mutex
	providers     []Provider
	salt          []byte
	nin           map[string]NINRecord // personID -> record
	bvn           map[string]BVNRecord
	liveness      map[string]*LivenessCheck
	consents      map[string][]Consent
	identityIndex map[string]string // identity hash -> personID (cross-tenant dup detection)
	queued        []string          // degraded submissions (INT-02: queue-and-notify)
	seq           int
}

func NewService(salt string, providers ...Provider) *Service {
	return &Service{
		providers: providers, salt: []byte(salt),
		nin: map[string]NINRecord{}, bvn: map[string]BVNRecord{},
		liveness: map[string]*LivenessCheck{}, consents: map[string][]Consent{},
		identityIndex: map[string]string{},
	}
}

func (s *Service) hashID(raw string) string {
	h := hmac.New(sha256.New, s.salt)
	h.Write([]byte(raw))
	return hex.EncodeToString(h.Sum(nil))
}

// VerifyNIN runs NIN verification, stores only the provider token, and
// registers the salted identity hash for cross-tenant duplicate detection.
func (s *Service) VerifyNIN(tenantID, personID, nin, name, dob string) (NINRecord, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var res VerifyResult
	ok := false
	for _, p := range s.providers {
		r, err := p.VerifyNIN(nin, name, dob)
		if err == nil {
			res, ok = r, true
			break
		}
	}
	if !ok {
		s.queued = append(s.queued, "nin:"+personID)
		return NINRecord{}, "", ErrProviderUnavailable
	}
	if !res.Matched {
		return NINRecord{}, "", ErrVerificationFailed
	}
	h := s.hashID("NIN:" + nin)
	dupOf := s.identityIndex[h]
	s.identityIndex[h] = personID
	rec := NINRecord{
		PersonID: personID, Token: res.Token, ProviderRef: res.ProviderRef,
		EvidenceRef: res.EvidenceRef, NINHash: h, VerifiedAt: time.Now().UTC(),
	}
	s.nin[personID] = rec
	return rec, dupOf, nil
}

// ValidateBVN persists only a salted hash and binds the OTP phone (FR-102).
func (s *Service) ValidateBVN(tenantID, personID, bvn, name, dob, phone string) (BVNRecord, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var res VerifyResult
	ok := false
	for _, p := range s.providers {
		r, err := p.ValidateBVN(bvn, name, dob, phone)
		if err == nil {
			res, ok = r, true
			break
		}
	}
	if !ok {
		s.queued = append(s.queued, "bvn:"+personID)
		return BVNRecord{}, "", ErrProviderUnavailable
	}
	if !res.Matched {
		return BVNRecord{}, "", ErrVerificationFailed
	}
	h := s.hashID("BVN:" + bvn)
	dupOf := s.identityIndex[h]
	s.identityIndex[h] = personID
	rec := BVNRecord{
		PersonID: personID, BVNHash: h, PhoneBound: phone,
		EvidenceRef: res.EvidenceRef, VerifiedAt: time.Now().UTC(),
	}
	s.bvn[personID] = rec
	return rec, dupOf, nil
}

// SubmitLiveness applies regulator thresholds (FR-103): auto-pass ≥0.80,
// manual review 0.60–0.80, fail <0.60; retry cap 3.
func (s *Service) SubmitLiveness(tenantID, personID string, kind LivenessKind, selfieRef string) (*LivenessCheck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := personID + ":" + string(kind)
	chk, exists := s.liveness[key]
	if !exists {
		s.seq++
		chk = &LivenessCheck{ID: fmt.Sprintf("lv-%d", s.seq), TenantID: tenantID, PersonID: personID, Kind: kind}
		s.liveness[key] = chk
	}
	if chk.Status == StatusFailed && chk.Attempts >= 3 {
		return chk, ErrRetryCapExceeded
	}
	ninTok := ""
	if r, ok := s.nin[personID]; ok {
		ninTok = r.Token
	}
	var score float64
	var evRef string
	var err error
	for _, p := range s.providers {
		score, evRef, err = p.LivenessFaceMatch(selfieRef, ninTok)
		if err == nil {
			break
		}
	}
	if err != nil {
		s.queued = append(s.queued, "liveness:"+key)
		return chk, ErrProviderUnavailable
	}
	chk.Attempts++
	chk.Score = score
	chk.EvidenceRef = evRef
	chk.At = time.Now().UTC()
	switch {
	case score >= 0.80:
		chk.Status = StatusPassed
	case score >= 0.60:
		chk.Status = StatusManualReview
	default:
		chk.Status = StatusFailed
	}
	return chk, nil
}

func (s *Service) Liveness(personID string, kind LivenessKind) (*LivenessCheck, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.liveness[personID+":"+string(kind)]
	return c, ok
}

// RecordConsent captures NDPA consent per purpose with wording version (FR-106).
func (s *Service) RecordConsent(tenantID, personID, purpose, wordingVersion string, granted bool) Consent {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	c := Consent{
		ID: fmt.Sprintf("consent-%d", s.seq), TenantID: tenantID, PersonID: personID,
		Purpose: purpose, WordingVersion: wordingVersion, Granted: granted, At: time.Now().UTC(),
	}
	s.consents[personID] = append(s.consents[personID], c)
	return c
}

// WithdrawConsent supports per-purpose withdrawal (FR-106).
func (s *Service) WithdrawConsent(personID, purpose string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cs := s.consents[personID]
	for i := range cs {
		if cs[i].Purpose == purpose && cs[i].Granted {
			cs[i].Granted = false
			cs[i].WithdrawnAt = time.Now().UTC()
			s.consents[personID] = cs
			return true
		}
	}
	return false
}

func (s *Service) Consents(personID string) []Consent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Consent{}, s.consents[personID]...)
}

// Queued returns degraded submissions awaiting retry (INT-02).
func (s *Service) Queued() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.queued...)
}
