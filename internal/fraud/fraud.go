// Package fraud implements real-time transaction anomaly scoring for
// contributions and withdrawals, complementing internal/aml (sanctions &
// KYC-level checks) with behavioural detection: velocity spikes,
// amount-vs-history outliers, channel flips, geo jumps and
// dormant-then-large patterns. High scores open a case queue for
// investigators.
package fraud

import (
	"fmt"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

// Tx is a transaction under screening.
type Tx struct {
	TenantID  string     `json:"tenant_id"`
	MemberID  string     `json:"member_id"`
	Kind      string     `json:"kind"` // CONTRIBUTION | WITHDRAWAL | TRANSFER
	Amount    money.Kobo `json:"amount_kobo"`
	Channel   string     `json:"channel"`   // USSD | WEB | WHATSAPP | AGENT | API
	StateCode string     `json:"state_code"` // member's transaction geo
	At        time.Time  `json:"at"`
}

// CaseStatus is the investigation lifecycle.
type CaseStatus string

const (
	CaseOpen      CaseStatus = "OPEN"
	CaseConfirmed CaseStatus = "CONFIRMED_FRAUD"
	CaseCleared   CaseStatus = "CLEARED"
)

// Case is an opened investigation.
type Case struct {
	ID        string     `json:"id"`
	TenantID  string     `json:"tenant_id"`
	MemberID  string     `json:"member_id"`
	Score     int        `json:"score"`
	Signals   []string   `json:"signals"`
	Tx        Tx         `json:"tx"`
	Status    CaseStatus `json:"status"`
	OpenedAt  time.Time  `json:"opened_at"`
}

// ScoreThreshold is the score at/above which a case opens automatically.
const ScoreThreshold = 60

// Engine screens transactions and manages cases.
type Engine struct {
	mu       sync.Mutex
	history  map[string][]Tx // tenantID|memberID -> recent txs
	cases    map[string]*Case
	caseSeq  int
	window   time.Duration // velocity window
}

func NewEngine() *Engine {
	return &Engine{history: map[string][]Tx{}, cases: map[string]*Case{}, window: time.Hour}
}

// Score evaluates a transaction, records it in member history, and opens
// a case when the score crosses the threshold. Returns score + signals.
func (e *Engine) Score(tx Tx) (int, []string, *Case) {
	e.mu.Lock()
	defer e.mu.Unlock()
	key := tx.TenantID + "|" + tx.MemberID
	hist := e.history[key]
	score := 0
	var signals []string

	// Signal 1: amount outlier vs member median (5x median, min history 3).
	if len(hist) >= 3 {
		var sum money.Kobo
		for _, h := range hist {
			sum += h.Amount
		}
		avg := sum / money.Kobo(len(hist))
		if avg > 0 && tx.Amount > avg*5 {
			score += 35
			signals = append(signals, "AMOUNT_OUTLIER_5X")
		}
	}
	// Signal 2: velocity — >3 txs in the window.
	recent := 0
	for _, h := range hist {
		if tx.At.Sub(h.At) <= e.window {
			recent++
		}
	}
	if recent >= 3 {
		score += 30
		signals = append(signals, "VELOCITY_SPIKE")
	}
	// Signal 3: channel flip within window.
	for i := len(hist) - 1; i >= 0 && i >= len(hist)-3; i-- {
		if hist[i].Channel != "" && tx.Channel != "" && hist[i].Channel != tx.Channel {
			score += 15
			signals = append(signals, "CHANNEL_FLIP")
			break
		}
	}
	// Signal 4: geo jump within window.
	for i := len(hist) - 1; i >= 0 && i >= len(hist)-3; i-- {
		if hist[i].StateCode != "" && tx.StateCode != "" && hist[i].StateCode != tx.StateCode {
			score += 20
			signals = append(signals, "GEO_JUMP")
			break
		}
	}
	// Signal 5: dormant-then-large (no tx in 180 days, then ≥ ₦500k).
	if len(hist) > 0 && tx.At.Sub(hist[len(hist)-1].At) > 180*24*time.Hour && tx.Amount >= money.NGN(500_000) {
		score += 60 // highest-risk pattern: auto-opens a case
		signals = append(signals, "DORMANT_THEN_LARGE")
	}

	e.history[key] = append(hist, tx)
	var c *Case
	if score >= ScoreThreshold {
		e.caseSeq++
		c = &Case{ID: fmt.Sprintf("fc-%06d", e.caseSeq), TenantID: tx.TenantID, MemberID: tx.MemberID,
			Score: score, Signals: signals, Tx: tx, Status: CaseOpen, OpenedAt: tx.At}
		e.cases[c.ID] = c
	}
	return score, signals, c
}

// Resolve closes a case as CONFIRMED_FRAUD or CLEARED.
func (e *Engine) Resolve(tenantID, caseID string, st CaseStatus) (Case, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.cases[caseID]
	if !ok || c.TenantID != tenantID {
		return Case{}, fmt.Errorf("fraud: case not found")
	}
	if st != CaseConfirmed && st != CaseCleared {
		return Case{}, fmt.Errorf("fraud: resolution must be CONFIRMED_FRAUD or CLEARED")
	}
	c.Status = st
	return *c, nil
}

// Cases lists cases, optionally only OPEN.
func (e *Engine) Cases(tenantID string, openOnly bool) []Case {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Case
	for _, c := range e.cases {
		if c.TenantID == tenantID && (!openOnly || c.Status == CaseOpen) {
			out = append(out, *c)
		}
	}
	return out
}
