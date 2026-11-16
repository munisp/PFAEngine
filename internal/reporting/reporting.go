// Package reporting implements FR-502: statutory returns (quarterly/annual)
// with sign-off workflow and inspection evidence packs.
package reporting

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var ErrSignOff = errors.New("reporting: sign-off requires two distinct officers")

type Return struct {
	Code        string         `json:"code"`
	TenantID    string         `json:"tenant_id"`
	Period      string         `json:"period"`
	RuleVersion string         `json:"rule_version"` // REG-08: traceable to rule version
	Figures     map[string]any `json:"figures"`
	Status      string         `json:"status"` // DRAFT | SIGNED_OFF | FILED
	SignedBy    []string       `json:"signed_by"`
	GeneratedAt time.Time      `json:"generated_at"`
	FiledAt     time.Time      `json:"filed_at,omitempty"`
}

// QuarterlyReturn aggregates the PenCom quarterly pack.
type QuarterlyReturn struct {
	MembersOpened      int        `json:"members_opened"`
	MembersClosed      int        `json:"members_closed"`
	ContributionsTotal money.Kobo `json:"contributions_total_kobo"`
	BenefitsPaid       money.Kobo `json:"benefits_paid_kobo"`
	SuspenseBalance    money.Kobo `json:"suspense_balance_kobo"`
	TransfersIn        int        `json:"transfers_in"`
	TransfersOut       int        `json:"transfers_out"`
	AutoMatchRate      float64    `json:"auto_match_rate"`
}

type Service struct {
	mu      sync.Mutex
	returns map[string]*Return
	seq     int
}

func NewService() *Service { return &Service{returns: map[string]*Return{}} }

// Generate builds a return draft (one-command generation, §4).
func (s *Service) Generate(tenantID, code, period, ruleVersion string, figures map[string]any) *Return {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	r := &Return{
		Code: code, TenantID: tenantID, Period: period, RuleVersion: ruleVersion,
		Figures: figures, Status: "DRAFT", GeneratedAt: time.Now().UTC(),
	}
	rID := fmt.Sprintf("ret-%d", s.seq)
	r.Figures["_id"] = rID
	s.returns[rID] = r
	return r
}

// SignOff applies dual-officer sign-off before filing (FR-502).
func (s *Service) SignOff(id, officer1, officer2 string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.returns[id]
	if !ok {
		return fmt.Errorf("reporting: return %s not found", id)
	}
	if officer1 == "" || officer2 == "" || officer1 == officer2 {
		return ErrSignOff
	}
	r.SignedBy = []string{officer1, officer2}
	r.Status = "SIGNED_OFF"
	return nil
}

func (s *Service) File(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.returns[id]
	if !ok {
		return fmt.Errorf("reporting: return %s not found", id)
	}
	if r.Status != "SIGNED_OFF" {
		return fmt.Errorf("reporting: must be signed off before filing")
	}
	r.Status = "FILED"
	r.FiledAt = time.Now().UTC()
	return nil
}

func (s *Service) Get(id string) (*Return, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.returns[id]
	return r, ok
}
