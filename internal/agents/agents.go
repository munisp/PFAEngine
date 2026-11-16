// Package agents implements the accredited pension-agent network that
// takes the CPS into markets, motor parks and communities (the informal-
// sector expansion channel PenCom announced with the MPG launch). The
// engine manages agent accreditation, batch (offline-first) enrolment of
// micro-pension accounts, and a WORM commission ledger paying agents a
// basis-point share of first-year contributions they originate.
package agents

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var (
	ErrNoAgent    = errors.New("agents: agent not found")
	ErrSuspended  = errors.New("agents: agent is suspended")
	ErrBadAmount  = errors.New("agents: amount must be positive")
)

// Agent is an accredited field agent.
type Agent struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	Name         string    `json:"name"`
	Phone        string    `json:"phone"`
	StateCode    string    `json:"state_code"`
	CommissionBps int64    `json:"commission_bps"` // share of originated contributions
	Suspended    bool      `json:"suspended"`
	AccreditedAt time.Time `json:"accredited_at"`
}

// Enrolment is one agent-originated micro-pension account opening.
type Enrolment struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	AgentID   string    `json:"agent_id"`
	RSAID     string    `json:"rsa_id"`
	Name      string    `json:"name"`
	Phone     string    `json:"phone"`
	Trade     string    `json:"trade"` // trader | artisan | farmer | rider | ...
	At        time.Time `json:"at"`
}

// Commission is one WORM commission ledger entry.
type Commission struct {
	ID           string     `json:"id"`
	TenantID     string     `json:"tenant_id"`
	AgentID      string     `json:"agent_id"`
	Contribution money.Kobo `json:"contribution_kobo"`
	Amount       money.Kobo `json:"amount_kobo"`
	Period       string     `json:"period"`
	At           time.Time  `json:"at"`
}

// Engine holds agents, enrolments and the commission ledger.
type Engine struct {
	mu         sync.Mutex
	agents     map[string]*Agent // tenantID|id
	enrolments []Enrolment
	commissions []Commission
	seq        int
}

func NewEngine() *Engine { return &Engine{agents: map[string]*Agent{}} }

// Accredit registers an agent.
func (e *Engine) Accredit(tenantID, name, phone, stateCode string, commissionBps int64, at time.Time) Agent {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	a := &Agent{ID: fmt.Sprintf("agt-%06d", e.seq), TenantID: tenantID, Name: name, Phone: phone,
		StateCode: stateCode, CommissionBps: commissionBps, AccreditedAt: at}
	e.agents[tenantID+"|"+a.ID] = a
	return *a
}

// Suspend halts an agent's origination.
func (e *Engine) Suspend(tenantID, agentID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.agents[tenantID+"|"+agentID]
	if !ok {
		return ErrNoAgent
	}
	a.Suspended = true
	return nil
}

// Enrol records one agent-originated account opening (offline batch or
// online single).
func (e *Engine) Enrol(tenantID, agentID, rsaID, name, phone, trade string, at time.Time) (Enrolment, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.agents[tenantID+"|"+agentID]
	if !ok {
		return Enrolment{}, ErrNoAgent
	}
	if a.Suspended {
		return Enrolment{}, ErrSuspended
	}
	e.seq++
	en := Enrolment{ID: fmt.Sprintf("age-%06d", e.seq), TenantID: tenantID, AgentID: agentID,
		RSAID: rsaID, Name: name, Phone: phone, Trade: trade, At: at}
	e.enrolments = append(e.enrolments, en)
	return en, nil
}

// EnrolBatch records an offline batch upload; partial failures are
// reported per row, not fatal.
func (e *Engine) EnrolBatch(tenantID, agentID string, rows []struct {
	RSAID, Name, Phone, Trade string
}, at time.Time) (ok []Enrolment, failed []string) {
	for _, r := range rows {
		en, err := e.Enrol(tenantID, agentID, r.RSAID, r.Name, r.Phone, r.Trade, at)
		if err != nil {
			failed = append(failed, r.RSAID+": "+err.Error())
			continue
		}
		ok = append(ok, en)
	}
	return
}

// PayCommission books a contribution-originated commission (WORM).
func (e *Engine) PayCommission(tenantID, agentID string, contribution money.Kobo, period string, at time.Time) (Commission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.agents[tenantID+"|"+agentID]
	if !ok {
		return Commission{}, ErrNoAgent
	}
	if contribution <= 0 {
		return Commission{}, ErrBadAmount
	}
	e.seq++
	c := Commission{ID: fmt.Sprintf("agc-%06d", e.seq), TenantID: tenantID, AgentID: agentID,
		Contribution: contribution, Amount: contribution * money.Kobo(a.CommissionBps) / 10000,
		Period: period, At: at}
	e.commissions = append(e.commissions, c)
	return c, nil
}

// CommissionTotal sums an agent's commissions.
func (e *Engine) CommissionTotal(tenantID, agentID string) money.Kobo {
	e.mu.Lock()
	defer e.mu.Unlock()
	var total money.Kobo
	for _, c := range e.commissions {
		if c.TenantID == tenantID && c.AgentID == agentID {
			total += c.Amount
		}
	}
	return total
}

// Leaderboard ranks agents by enrolment count for a tenant.
func (e *Engine) Leaderboard(tenantID string) map[string]int {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[string]int{}
	for _, en := range e.enrolments {
		if en.TenantID == tenantID {
			out[en.AgentID]++
		}
	}
	return out
}
