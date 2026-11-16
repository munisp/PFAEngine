// Package succession implements the beneficiary nomination vault: members
// nominate next-of-kin with percentage shares (must total 100%), every
// update is versioned and append-only (WORM history), and nominations
// carry a liveness re-confirmation schedule so records never go stale
// before a death claim (internal/death) needs them.
package succession

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrShares     = errors.New("succession: beneficiary shares must total 10000 bps (100%)")
	ErrNoNomination = errors.New("succession: no nomination for member")
)

// Beneficiary is one nominee with a share in basis points.
type Beneficiary struct {
	Name         string `json:"name"`
	Relationship string `json:"relationship"`
	Phone        string `json:"phone"`
	ShareBps     int64  `json:"share_bps"` // total across nominees = 10000
}

// Nomination is one versioned nomination (history is WORM).
type Nomination struct {
	ID              string        `json:"id"`
	TenantID        string        `json:"tenant_id"`
	MemberID        string        `json:"member_id"`
	Version         int           `json:"version"`
	Beneficiaries   []Beneficiary `json:"beneficiaries"`
	ConfirmedAt     time.Time     `json:"confirmed_at"`      // liveness confirmation
	NextReconfirmDue time.Time    `json:"next_reconfirm_due"`
	Superseded      bool          `json:"superseded"` // true once a newer version exists
}

// Engine holds nomination history.
type Engine struct {
	mu           sync.Mutex
	nominations  []Nomination // append-only history
	reconfirmGap time.Duration
	seq          int
}

// NewEngine builds a vault; reconfirmGap is the liveness re-confirmation
// interval (e.g. 12 months).
func NewEngine(reconfirmGap time.Duration) *Engine {
	return &Engine{reconfirmGap: reconfirmGap}
}

// Nominate creates a new version, superseding the previous one.
func (e *Engine) Nominate(tenantID, memberID string, bens []Beneficiary, at time.Time) (Nomination, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var total int64
	for _, b := range bens {
		total += b.ShareBps
	}
	if total != 10000 {
		return Nomination{}, ErrShares
	}
	version := 1
	for i := range e.nominations {
		n := &e.nominations[i]
		if n.TenantID == tenantID && n.MemberID == memberID && !n.Superseded {
			n.Superseded = true
			version = n.Version + 1
		}
	}
	e.seq++
	n := Nomination{ID: fmt.Sprintf("nom-%06d", e.seq), TenantID: tenantID, MemberID: memberID,
		Version: version, Beneficiaries: bens, ConfirmedAt: at, NextReconfirmDue: at.Add(e.reconfirmGap)}
	e.nominations = append(e.nominations, n)
	return n, nil
}

// Current returns the live (non-superseded) nomination.
func (e *Engine) Current(tenantID, memberID string) (Nomination, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := len(e.nominations) - 1; i >= 0; i-- {
		n := e.nominations[i]
		if n.TenantID == tenantID && n.MemberID == memberID && !n.Superseded {
			return n, nil
		}
	}
	return Nomination{}, ErrNoNomination
}

// Reconfirm refreshes liveness on the current nomination.
func (e *Engine) Reconfirm(tenantID, memberID string, at time.Time) (Nomination, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := len(e.nominations) - 1; i >= 0; i-- {
		n := &e.nominations[i]
		if n.TenantID == tenantID && n.MemberID == memberID && !n.Superseded {
			n.ConfirmedAt = at
			n.NextReconfirmDue = at.Add(e.reconfirmGap)
			return *n, nil
		}
	}
	return Nomination{}, ErrNoNomination
}

// DueForReconfirm lists member IDs whose reconfirmation deadline has
// passed (for nudge campaigns).
func (e *Engine) DueForReconfirm(tenantID string, at time.Time) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, n := range e.nominations {
		if n.TenantID == tenantID && !n.Superseded && at.After(n.NextReconfirmDue) {
			out = append(out, n.MemberID)
		}
	}
	return out
}

// History returns the full version chain for a member (WORM).
func (e *Engine) History(tenantID, memberID string) []Nomination {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Nomination
	for _, n := range e.nominations {
		if n.TenantID == tenantID && n.MemberID == memberID {
			out = append(out, n)
		}
	}
	return out
}
