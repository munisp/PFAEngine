// Package kyb implements next-generation KYB (Know Your Business):
// CAC RC number and TIN validation, directors and ultimate beneficial
// owner (UBO) registry with the 25% ownership rule, sanctions/PEP
// screening of all principals via internal/aml, corporate-document
// evidence via internal/docupload, and a risk-scored decision routed into
// the unified case management engine for manual review.
package kyb

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/aml"
	"github.com/munisp/pfaengine/internal/casemgmt"
)

var (
	ErrBusinessNotFound = errors.New("kyb: business not found")
	ErrBadRC            = errors.New("kyb: invalid CAC RC number (expected RC followed by 5-7 digits)")
	ErrBadTIN           = errors.New("kyb: invalid TIN (expected 8-13 digits)")
	ErrBadTransition    = errors.New("kyb: action not allowed in current status")
)

var (
	reRC  = regexp.MustCompile(`^RC\d{5,7}$`)
	reTIN = regexp.MustCompile(`^\d{8,13}$`)
)

type Status string

const (
	StatusPending      Status = "PENDING"
	StatusVerified     Status = "VERIFIED"
	StatusManualReview Status = "MANUAL_REVIEW"
	StatusRejected     Status = "REJECTED"
)

// Person is a director or other principal of the business.
type Person struct {
	Name string `json:"name"`
	DOB  string `json:"dob"` // YYYY-MM-DD
	NIN  string `json:"nin,omitempty"`
	Role string `json:"role,omitempty"`
}

// UBO is an ultimate beneficial owner with ownership percentage.
type UBO struct {
	Person
	OwnershipPct float64 `json:"ownership_pct"`
}

// Business is one KYB verification record for an employer.
type Business struct {
	ID         string `json:"id"` // kb-000001
	TenantID   string `json:"tenant_id"`
	EmployerID string `json:"employer_id"`
	Name       string `json:"name"`
	RCNumber   string `json:"rc_number"`
	TIN        string `json:"tin"`

	Directors []Person `json:"directors"`
	UBOs      []UBO    `json:"ubos"`
	DocRefs   []string `json:"doc_refs,omitempty"`

	Screening []aml.ScreeningResult `json:"screening,omitempty"`

	RiskScore int      `json:"risk_score"`
	RiskFlags []string `json:"risk_flags,omitempty"`
	Status    Status   `json:"status"`
	CaseID    string   `json:"case_id,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	DecidedAt time.Time `json:"decided_at,omitempty"`
}

const (
	wNoUBO25       = 35 // no single UBO owns >= 25% (SCUML/CBN threshold)
	wOwnershipOver = 40 // declared ownership exceeds 100%
	wNoDirectors   = 30
	wMissingDocs   = 30 // no corporate docs at all -> always human review
	wPEPHit        = 40
	wTinyCapital   = 0 // reserved

	threshKYBReview = 30
)

// Engine verifies businesses and routes manual-review outcomes into the
// case management engine.
type Engine struct {
	mu    sync.Mutex
	aml   *aml.Service
	cases *casemgmt.Engine
	biz   map[string]*Business
	seq   int
	now   func() time.Time
}

func New(a *aml.Service, ce *casemgmt.Engine) *Engine {
	return &Engine{
		aml: a, cases: ce, biz: map[string]*Business{},
		now: func() time.Time { return time.Now().UTC() },
	}
}

// NormalizeRC canonicalises a CAC RC number (strip spaces, uppercase).
func NormalizeRC(rc string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(rc), " ", ""))
}

// Submit registers and verifies a business. The decision is immediate:
// VERIFIED, MANUAL_REVIEW (opens a casemgmt KYC_REVIEW case), or REJECTED
// (sanctions hit on any principal).
func (e *Engine) Submit(tenantID, employerID, name, rc, tin string, directors []Person, ubos []UBO, docRefs []string, actor string) (Business, error) {
	rc = NormalizeRC(rc)
	if !reRC.MatchString(rc) {
		return Business{}, ErrBadRC
	}
	tin = strings.TrimSpace(tin)
	if tin != "" && !reTIN.MatchString(tin) {
		return Business{}, ErrBadTIN
	}
	if name == "" {
		return Business{}, errors.New("kyb: business name is required")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	b := &Business{
		ID:       fmt.Sprintf("kb-%06d", e.seq),
		TenantID: tenantID, EmployerID: employerID,
		Name: name, RCNumber: rc, TIN: tin,
		Directors: directors, UBOs: ubos, DocRefs: docRefs,
		Status:    StatusPending,
		CreatedAt: e.now(),
	}

	// --- UBO 25% rule (CBN/SCUML beneficial-ownership threshold) ---
	has25 := false
	total := 0.0
	for _, u := range ubos {
		total += u.OwnershipPct
		if u.OwnershipPct >= 25 {
			has25 = true
		}
	}
	if !has25 {
		e.flagLocked(b, "no_ubo_25pct", wNoUBO25)
	}
	if total > 100.0001 {
		e.flagLocked(b, "ownership_exceeds_100pct", wOwnershipOver)
	}
	if len(directors) == 0 {
		e.flagLocked(b, "no_directors", wNoDirectors)
	}
	if len(docRefs) == 0 {
		e.flagLocked(b, "missing_corporate_docs", wMissingDocs)
	}

	// --- Screen every principal against sanctions/PEP lists ---
	sanctionHit := false
	screenOne := func(p Person) {
		if e.aml == nil || p.Name == "" {
			return
		}
		res := e.aml.Screen(tenantID, b.ID, p.Name, p.DOB, aml.Tier3)
		b.Screening = append(b.Screening, res)
		if res.SanctionHit {
			sanctionHit = true
			e.flagLocked(b, "sanction_hit:"+p.Name, 100)
		}
		if res.PEP {
			e.flagLocked(b, "pep:"+p.Name, wPEPHit)
		}
	}
	for _, d := range directors {
		screenOne(d)
	}
	for _, u := range ubos {
		screenOne(u.Person)
	}

	// --- Decision ---
	switch {
	case sanctionHit:
		b.Status = StatusRejected
	case b.RiskScore >= threshKYBReview:
		b.Status = StatusManualReview
	default:
		b.Status = StatusVerified
	}
	b.DecidedAt = e.now()

	if b.Status == StatusManualReview && e.cases != nil {
		c, _, err := e.cases.Open(casemgmt.OpenInput{
			TenantID:    tenantID,
			Type:        casemgmt.TypeKYCReview,
			Subject:     "KYB manual review: " + name,
			Description: fmt.Sprintf("Business %s (RC %s) risk=%d flags=%v", b.ID, rc, b.RiskScore, b.RiskFlags),
			EmployerID:  employerID,
			LinkedKind:  "KYB_BUSINESS",
			LinkedID:    b.ID,
			Source:      casemgmt.SourceAPI,
			Actor:       actor,
			NoDedup:     true,
		})
		if err == nil {
			b.CaseID = c.ID
		}
	}
	e.biz[b.ID] = b
	return *b, nil
}

func (e *Engine) flagLocked(b *Business, flag string, weight int) {
	for _, f := range b.RiskFlags {
		if f == flag {
			return
		}
	}
	b.RiskFlags = append(b.RiskFlags, flag)
	b.RiskScore += weight
}

func (e *Engine) Get(tenantID, id string) (Business, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b, ok := e.biz[id]
	if !ok || b.TenantID != tenantID {
		return Business{}, ErrBusinessNotFound
	}
	return *b, nil
}

// List returns businesses for a tenant, optionally filtered by status,
// newest first.
func (e *Engine) List(tenantID string, status string) []Business {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Business
	for _, b := range e.biz {
		if b.TenantID != tenantID {
			continue
		}
		if status != "" && string(b.Status) != status {
			continue
		}
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Review lets staff approve or reject a MANUAL_REVIEW business, resolving
// the linked KYC_REVIEW case.
func (e *Engine) Review(tenantID, id string, approve bool, actor, note string) (Business, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b, ok := e.biz[id]
	if !ok || b.TenantID != tenantID {
		return Business{}, ErrBusinessNotFound
	}
	if b.Status != StatusManualReview {
		return Business{}, ErrBadTransition
	}
	if approve {
		b.Status = StatusVerified
	} else {
		b.Status = StatusRejected
	}
	if b.CaseID != "" && e.cases != nil {
		code := "VERIFIED"
		if !approve {
			code = "REJECTED"
		}
		_, _ = e.cases.Resolve(tenantID, b.CaseID, note, code, actor)
	}
	return *b, nil
}
