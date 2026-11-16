// Package aml implements PLT-05: AML/CFT & sanctions screening — CDD/KYC
// tiers, PEP screening, hash-based sanctions lists (privacy-safe),
// suspicious-activity flags, and audit-grade screening history.
package aml

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

type Tier string

const (
	Tier1 Tier = "TIER_1" // simplified CDD (PPP micro)
	Tier2 Tier = "TIER_2"
	Tier3 Tier = "TIER_3" // full CDD
)

type ScreeningResult struct {
	PersonID    string    `json:"person_id"`
	TenantID    string    `json:"tenant_id"`
	PEP         bool      `json:"pep"`
	SanctionHit bool      `json:"sanction_hit"`
	SAR         bool      `json:"suspicious_activity"`
	Tier        Tier      `json:"cdd_tier"`
	ScreenedAt  time.Time `json:"screened_at"`
}

type Service struct {
	mu            sync.Mutex
	sanctionsHash map[string]bool // sha256(name|dob) -> listed
	pepHash       map[string]bool
	history       []ScreeningResult
}

func NewService() *Service {
	return &Service{sanctionsHash: map[string]bool{}, pepHash: map[string]bool{}}
}

func subjectHash(name, dob string) string {
	h := sha256.Sum256([]byte(name + "|" + dob))
	return hex.EncodeToString(h[:])
}

// LoadSanctions ingests UN/local list entries (hash-only, privacy-safe).
func (s *Service) LoadSanctions(entries [][2]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range entries {
		s.sanctionsHash[subjectHash(e[0], e[1])] = true
	}
}

func (s *Service) LoadPEP(entries [][2]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range entries {
		s.pepHash[subjectHash(e[0], e[1])] = true
	}
}

// Screen runs onboarding/periodic screening and archives the result.
func (s *Service) Screen(tenantID, personID, name, dob string, tier Tier) ScreeningResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := subjectHash(name, dob)
	res := ScreeningResult{
		PersonID: personID, TenantID: tenantID, PEP: s.pepHash[h],
		SanctionHit: s.sanctionsHash[h], Tier: tier, ScreenedAt: time.Now().UTC(),
	}
	s.history = append(s.history, res)
	return res
}

func (s *Service) FlagSuspicious(tenantID, personID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, ScreeningResult{
		PersonID: personID, TenantID: tenantID, SAR: true, ScreenedAt: time.Now().UTC(),
	})
}

// History is the audit-grade screening trail (PLT-05).
func (s *Service) History(tenantID string) []ScreeningResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ScreeningResult
	for _, r := range s.history {
		if r.TenantID == tenantID {
			out = append(out, r)
		}
	}
	return out
}
