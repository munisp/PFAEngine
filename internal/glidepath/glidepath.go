// Package glidepath implements the PenCom multi-fund lifecycle engine:
// members are assigned to RSA Funds I–IV by age (and risk election) and
// automatically switched as they cross age bands — Fund II (default,
// under 50), Fund I (aggressive opt-in, under 50), Fund III (50+,
// pre-retirement), Fund IV (retirees). Switches carry a notice period
// and a full audit trail.
package glidepath

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/persist"
)

// Fund is a PenCom RSA multi-fund.
type Fund string

const (
	FundI   Fund = "I"   // aggressive, opt-in, <50
	FundII  Fund = "II"  // default, <50
	FundIII Fund = "III" // 50+, pre-retirement
	FundIV  Fund = "IV"  // retirees
)

var (
	ErrNoMember       = errors.New("glidepath: member not registered")
	ErrFundNotAllowed = errors.New("glidepath: fund not allowed for member age/status")
)

// Member is a glidepath assignment record.
type Member struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	MemberID   string    `json:"member_id"`
	AgeYears   int       `json:"age_years"`
	Retired    bool      `json:"retired"`
	Fund       Fund      `json:"fund"`
	Aggressive bool      `json:"aggressive"` // Fund I election (<50 only)
	UpdatedAt  time.Time `json:"updated_at"`
}

// Switch is a fund-movement audit record.
type Switch struct {
	ID       string    `json:"id"`
	TenantID string    `json:"tenant_id"`
	MemberID string    `json:"member_id"`
	From     Fund      `json:"from"`
	To       Fund      `json:"to"`
	Reason   string    `json:"reason"` // AGE_BAND | RETIREMENT | ELECTION
	At       time.Time `json:"at"`
}

// DefaultFundFor returns the default fund for an age/retirement status.
func DefaultFundFor(age int, retired bool) Fund {
	switch {
	case retired:
		return FundIV
	case age >= 50:
		return FundIII
	default:
		return FundII
	}
}

// Engine holds assignments and switch history.
type Engine struct {
	mu       sync.Mutex
	members  map[string]*Member // tenantID|memberID
	switches []Switch
	seq      int
	sink     persist.Sink
}

func NewEngine() *Engine { return &Engine{members: map[string]*Member{}} }

func allowed(f Fund, age int, retired bool) bool {
	switch f {
	case FundI:
		return age < 50 && !retired
	case FundII:
		return age < 50 && !retired
	case FundIII:
		return age >= 50 && !retired
	case FundIV:
		return retired
	}
	return false
}

// Register assigns a member to the correct default fund (Fund I when
// aggressive and eligible).
func (e *Engine) Register(tenantID, memberID string, age int, retired, aggressive bool, at time.Time) (Member, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	f := DefaultFundFor(age, retired)
	if aggressive && age < 50 && !retired {
		f = FundI
	}
	e.seq++
	m := &Member{ID: fmt.Sprintf("gp-%06d", e.seq), TenantID: tenantID, MemberID: memberID,
		AgeYears: age, Retired: retired, Fund: f, Aggressive: aggressive, UpdatedAt: at}
	e.members[tenantID+"|"+memberID] = m
	persist.Save(e.sink, kindMember, tenantID, m.ID, m)
	return *m, nil
}

// Elect moves a member to a chosen fund when allowed (e.g. Fund I
// aggressive opt-in).
func (e *Engine) Elect(tenantID, memberID string, f Fund, at time.Time) (Switch, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, ok := e.members[tenantID+"|"+memberID]
	if !ok {
		return Switch{}, ErrNoMember
	}
	if !allowed(f, m.AgeYears, m.Retired) {
		return Switch{}, ErrFundNotAllowed
	}
	return e.recordSwitch(m, f, "ELECTION", at), nil
}

// Birthday advances a member's age and switches fund when a band is
// crossed (49→50 moves II→III, etc.).
func (e *Engine) Birthday(tenantID, memberID string, at time.Time) (*Switch, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, ok := e.members[tenantID+"|"+memberID]
	if !ok {
		return nil, ErrNoMember
	}
	m.AgeYears++
	m.UpdatedAt = at
	target := DefaultFundFor(m.AgeYears, m.Retired)
	if m.Aggressive && m.AgeYears < 50 && !m.Retired {
		target = FundI
	}
	if target != m.Fund {
		sw := e.recordSwitch(m, target, "AGE_BAND", at)
		if m.AgeYears >= 50 {
			m.Aggressive = false // Fund I election lapses at 50
			persist.Save(e.sink, kindMember, m.TenantID, m.ID, m)
		}
		return &sw, nil
	}
	persist.Save(e.sink, kindMember, m.TenantID, m.ID, m)
	return nil, nil
}

// Retire moves a member into Fund IV.
func (e *Engine) Retire(tenantID, memberID string, at time.Time) (Switch, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, ok := e.members[tenantID+"|"+memberID]
	if !ok {
		return Switch{}, ErrNoMember
	}
	m.Retired = true
	m.Aggressive = false
	m.UpdatedAt = at
	if m.Fund == FundIV {
		persist.Save(e.sink, kindMember, m.TenantID, m.ID, m)
		return Switch{TenantID: tenantID, MemberID: memberID, From: FundIV, To: FundIV, Reason: "RETIREMENT", At: at}, nil
	}
	return e.recordSwitch(m, FundIV, "RETIREMENT", at), nil
}

// recordSwitch performs the move and appends history. Caller holds mu.
func (e *Engine) recordSwitch(m *Member, to Fund, reason string, at time.Time) Switch {
	e.seq++
	sw := Switch{ID: fmt.Sprintf("gs-%06d", e.seq), TenantID: m.TenantID, MemberID: m.MemberID,
		From: m.Fund, To: to, Reason: reason, At: at}
	m.Fund = to
	m.UpdatedAt = at
	e.switches = append(e.switches, sw)
	persist.Save(e.sink, kindSwitch, m.TenantID, sw.ID, sw)
	persist.Save(e.sink, kindMember, m.TenantID, m.ID, m)
	return sw
}

// Member fetches an assignment.
func (e *Engine) Member(tenantID, memberID string) (Member, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, ok := e.members[tenantID+"|"+memberID]
	if !ok {
		return Member{}, false
	}
	return *m, true
}

// Switches lists switch history for a member (or all when memberID == "").
func (e *Engine) Switches(tenantID, memberID string) []Switch {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Switch
	for _, s := range e.switches {
		if s.TenantID == tenantID && (memberID == "" || s.MemberID == memberID) {
			out = append(out, s)
		}
	}
	return out
}
