// Package scheme implements SCM-01/02/03/04/15: the scheme typology
// registry — any DC/DB/hybrid/personal/occupational design as data +
// rule-set references, never code forks.
package scheme

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrExists = errors.New("scheme: id exists")

type Typology string

const (
	DCOccupational Typology = "DC_OCCUPATIONAL"
	DBFinalSalary  Typology = "DB_FINAL_SALARY"
	DBCareerAvg    Typology = "DB_CAREER_AVERAGE" // CARE
	Hybrid         Typology = "HYBRID"
	Personal       Typology = "PERSONAL"
	GroupPersonal  Typology = "GROUP_PERSONAL"
	AVC            Typology = "AVC"
	Sharia         Typology = "SHARIA" // SCM-12
)

// ContributionDesign (SCM-04): percentage or flat; tiered by grade;
// auto-escalation; employer match; salary sacrifice; AVC.
type ContributionDesign struct {
	Mode            string `json:"mode"` // PERCENT | FLAT
	EmployeePct     int    `json:"employee_pct,omitempty"`
	EmployerPct     int    `json:"employer_pct,omitempty"`
	FlatKobo        int64  `json:"flat_kobo,omitempty"`
	MatchUpToPct    int    `json:"employer_match_up_to_pct,omitempty"`
	EscalationPct   int    `json:"auto_escalation_pct,omitempty"`
	SalarySacrifice bool   `json:"salary_sacrifice,omitempty"`
	RuleURI         string `json:"rule_uri"` // effective-dated rule pack (SCM-15)
}

// Scheme carries its own rule-set reference (SCM-01).
type Scheme struct {
	ID           string             `json:"id"`
	TenantID     string             `json:"tenant_id"`
	Name         string             `json:"name"`
	Typology     Typology           `json:"typology"`
	Jurisdiction string             `json:"jurisdiction"`
	Umbrella     bool               `json:"umbrella"` // SCM-02
	ParentScheme string             `json:"parent_scheme,omitempty"`
	Design       ContributionDesign `json:"contribution_design"`
	RuleURIs     []string           `json:"rule_uris"` // every governing rule pack (SCM-15)
	CreatedAt    time.Time          `json:"created_at"`
}

type Registry struct {
	mu      sync.Mutex
	schemes map[string]*Scheme
}

func NewRegistry() *Registry { return &Registry{schemes: map[string]*Scheme{}} }

func (r *Registry) Register(s Scheme) (*Scheme, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.schemes[s.ID]; ok {
		return nil, ErrExists
	}
	if len(s.RuleURIs) == 0 {
		return nil, fmt.Errorf("scheme: at least one rule URI required (SCM-15)")
	}
	s.CreatedAt = time.Now().UTC()
	cp := s
	r.schemes[s.ID] = &cp
	return &cp, nil
}

func (r *Registry) Get(id string) (*Scheme, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.schemes[id]
	return s, ok
}

func (r *Registry) List(tenantID string) []*Scheme {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*Scheme
	for _, s := range r.schemes {
		if s.TenantID == tenantID {
			out = append(out, s)
		}
	}
	return out
}

// EmployerSubAccounts implements scheme/employer-level sub-accounting (SCM-02).
func (r *Registry) EmployerSubAccounts(schemeID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, s := range r.schemes {
		if s.ParentScheme == schemeID {
			out = append(out, s.ID)
		}
	}
	return out
}
