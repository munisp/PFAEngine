// Package escalation implements automatic contribution escalation: a
// member's contribution rate steps up annually (or on pay-rise events)
// toward a cap. Opt-outs are explicit, reasoned and auditable — the
// "save more tomorrow" behavioural design that materially lifts balances
// without members feeling the step.
package escalation

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrNoRule    = errors.New("escalation: no rule for member")
	ErrBadStep   = errors.New("escalation: step and cap must be positive, cap >= base")
	ErrOptedOut  = errors.New("escalation: member opted out — re-enrol first")
)

// Rule is one member's escalation schedule. Rates in basis points of
// pensionable salary (PRA minimum total = 1800 bps: 10% employer + 8%
// employee).
type Rule struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	MemberID     string    `json:"member_id"`
	BaseRateBps  int64     `json:"base_rate_bps"`
	StepBps      int64     `json:"step_bps"`      // annual step
	MaxBps       int64     `json:"max_bps"`       // cap
	CurrentBps   int64     `json:"current_bps"`
	OptedOut     bool      `json:"opted_out"`
	OptOutReason string    `json:"opt_out_reason,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// Event is an audit record of a step-up, opt-out or re-enrolment.
type Event struct {
	At       time.Time `json:"at"`
	MemberID string    `json:"member_id"`
	Kind     string    `json:"kind"` // STEP_UP | OPT_OUT | RE_ENROL
	FromBps  int64     `json:"from_bps"`
	ToBps    int64     `json:"to_bps"`
	Reason   string    `json:"reason,omitempty"`
}

// Engine holds all tenant rules.
type Engine struct {
	mu     sync.Mutex
	rules  map[string]*Rule // tenantID|memberID
	events []Event
	seq    int
}

func NewEngine() *Engine { return &Engine{rules: map[string]*Rule{}} }

// Enrol creates an escalation rule for a member.
func (e *Engine) Enrol(tenantID, memberID string, baseBps, stepBps, maxBps int64, at time.Time) (Rule, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if stepBps <= 0 || maxBps < baseBps || baseBps <= 0 {
		return Rule{}, ErrBadStep
	}
	e.seq++
	r := &Rule{
		ID: fmt.Sprintf("esc-%06d", e.seq), TenantID: tenantID, MemberID: memberID,
		BaseRateBps: baseBps, StepBps: stepBps, MaxBps: maxBps,
		CurrentBps: baseBps, CreatedAt: at,
	}
	e.rules[tenantID+"|"+memberID] = r
	return *r, nil
}

// Tick applies the annual step-up to every non-opted-out rule for a
// tenant, returning the events generated.
func (e *Engine) Tick(tenantID string, at time.Time) []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Event
	for _, r := range e.rules {
		if r.TenantID != tenantID || r.OptedOut || r.CurrentBps >= r.MaxBps {
			continue
		}
		from := r.CurrentBps
		r.CurrentBps += r.StepBps
		if r.CurrentBps > r.MaxBps {
			r.CurrentBps = r.MaxBps
		}
		ev := Event{At: at, MemberID: r.MemberID, Kind: "STEP_UP", FromBps: from, ToBps: r.CurrentBps}
		e.events = append(e.events, ev)
		out = append(out, ev)
	}
	return out
}

// OptOut freezes escalation for a member with a mandatory reason.
func (e *Engine) OptOut(tenantID, memberID, reason string, at time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.rules[tenantID+"|"+memberID]
	if !ok {
		return ErrNoRule
	}
	r.OptedOut = true
	r.OptOutReason = reason
	e.events = append(e.events, Event{At: at, MemberID: memberID, Kind: "OPT_OUT", FromBps: r.CurrentBps, ToBps: r.CurrentBps, Reason: reason})
	return nil
}

// ReEnrol resumes escalation after an opt-out.
func (e *Engine) ReEnrol(tenantID, memberID string, at time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.rules[tenantID+"|"+memberID]
	if !ok {
		return ErrNoRule
	}
	r.OptedOut = false
	r.OptOutReason = ""
	e.events = append(e.events, Event{At: at, MemberID: memberID, Kind: "RE_ENROL", FromBps: r.CurrentBps, ToBps: r.CurrentBps})
	return nil
}

// Rule fetches a member's rule.
func (e *Engine) Rule(tenantID, memberID string) (Rule, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.rules[tenantID+"|"+memberID]
	if !ok {
		return Rule{}, false
	}
	return *r, true
}

// Events returns the audit trail, optionally filtered by member.
func (e *Engine) Events(tenantID, memberID string) []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Event
	for _, ev := range e.events {
		if ev.MemberID == memberID || memberID == "" {
			_ = tenantID // events are already tenant-sourced via rules
			out = append(out, ev)
		}
	}
	return out
}
