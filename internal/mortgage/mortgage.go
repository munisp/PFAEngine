// Package mortgage implements the PRA 2014 s.89(2) residential-mortgage
// equity contribution: an RSA holder in employment may apply up to 25% of
// their RSA balance as equity contribution toward a residential mortgage.
// The engine enforces the 25% cap, the in-employment precondition and a
// SUBMITTED → APPROVED → DISBURSED workflow with audit hooks.
package mortgage

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

// AppStatus is the application lifecycle.
type AppStatus string

const (
	AppSubmitted AppStatus = "SUBMITTED"
	AppApproved  AppStatus = "APPROVED"
	AppDisbursed AppStatus = "DISBURSED"
	AppRejected  AppStatus = "REJECTED"
)

var (
	ErrOverCap       = errors.New("mortgage: amount exceeds 25% of RSA balance (PRA s.89(2))")
	ErrNotEmployed   = errors.New("mortgage: applicant must be in employment")
	ErrBadAmount     = errors.New("mortgage: amount must be positive")
	ErrNoApp         = errors.New("mortgage: application not found")
	ErrBadTransition = errors.New("mortgage: invalid status transition")
)

// MaxEquity returns the s.89(2) cap: 25% of RSA balance.
func MaxEquity(rsaBalance money.Kobo) money.Kobo { return rsaBalance / 4 }

// Application is one mortgage equity-contribution request.
type Application struct {
	ID          string     `json:"id"`
	TenantID    string     `json:"tenant_id"`
	MemberID    string     `json:"member_id"`
	RSABalance  money.Kobo `json:"rsa_balance_kobo"`
	Amount      money.Kobo `json:"amount_kobo"` // ≤ 25% of balance
	LenderRef   string     `json:"lender_ref"`  // mortgage lender reference
	Status      AppStatus  `json:"status"`
	Reason      string     `json:"reason,omitempty"` // rejection reason
	Events      []string   `json:"events"`
	SubmittedAt time.Time  `json:"submitted_at"`
	DecidedAt   *time.Time `json:"decided_at,omitempty"`
}

// Engine holds applications.
type Engine struct {
	mu   sync.Mutex
	apps map[string]*Application // tenantID|id
	seq  int
}

func NewEngine() *Engine { return &Engine{apps: map[string]*Application{}} }

// Apply submits an application. employed must be true (s.89(2)
// precondition); amount is clamp-checked against the 25% cap.
func (e *Engine) Apply(tenantID, memberID string, rsaBalance, amount money.Kobo, lenderRef string, employed bool, at time.Time) (Application, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !employed {
		return Application{}, ErrNotEmployed
	}
	if amount <= 0 {
		return Application{}, ErrBadAmount
	}
	if amount > MaxEquity(rsaBalance) {
		return Application{}, ErrOverCap
	}
	e.seq++
	a := &Application{ID: fmt.Sprintf("mtg-%06d", e.seq), TenantID: tenantID, MemberID: memberID,
		RSABalance: rsaBalance, Amount: amount, LenderRef: lenderRef, Status: AppSubmitted,
		Events: []string{"SUBMITTED"}, SubmittedAt: at}
	e.apps[tenantID+"|"+a.ID] = a
	return *a, nil
}

func (e *Engine) transition(tenantID, id string, from, to AppStatus, reason string, at time.Time) (Application, error) {
	a, ok := e.apps[tenantID+"|"+id]
	if !ok {
		return Application{}, ErrNoApp
	}
	if a.Status != from {
		return Application{}, ErrBadTransition
	}
	a.Status = to
	a.Reason = reason
	a.DecidedAt = &at
	a.Events = append(a.Events, string(to))
	return *a, nil
}

// Approve moves SUBMITTED → APPROVED.
func (e *Engine) Approve(tenantID, id string, at time.Time) (Application, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.transition(tenantID, id, AppSubmitted, AppApproved, "", at)
}

// Reject moves SUBMITTED → REJECTED with reason.
func (e *Engine) Reject(tenantID, id, reason string, at time.Time) (Application, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.transition(tenantID, id, AppSubmitted, AppRejected, reason, at)
}

// Disburse moves APPROVED → DISBURSED.
func (e *Engine) Disburse(tenantID, id string, at time.Time) (Application, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.transition(tenantID, id, AppApproved, AppDisbursed, "", at)
}

// Get fetches an application.
func (e *Engine) Get(tenantID, id string) (Application, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.apps[tenantID+"|"+id]
	if !ok {
		return Application{}, false
	}
	return *a, true
}

// List lists applications, optionally filtered by member.
func (e *Engine) List(tenantID, memberID string) []Application {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Application
	for _, a := range e.apps {
		if a.TenantID == tenantID && (memberID == "" || a.MemberID == memberID) {
			out = append(out, *a)
		}
	}
	return out
}
