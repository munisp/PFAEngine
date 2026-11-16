// Package roundup implements round-up micro-savings: every eligible
// transaction is rounded up to the nearest step (e.g. ₦100) and the
// difference sweeps into the member's AVC (Additional Voluntary
// Contribution) wallet, subject to a monthly cap. Designed for the
// informal sector and salary earners who "save without noticing".
package roundup

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var (
	ErrNoWallet  = errors.New("roundup: no wallet for member")
	ErrBadStep   = errors.New("roundup: step must be positive")
	ErrCapReached = errors.New("roundup: monthly cap reached")
)

// Wallet is one member's round-up AVC accumulator.
type Wallet struct {
	ID            string     `json:"id"`
	TenantID      string     `json:"tenant_id"`
	MemberID      string     `json:"member_id"`
	Step          money.Kobo `json:"step_kobo"`        // rounding step, e.g. ₦100
	MonthlyCap    money.Kobo `json:"monthly_cap_kobo"` // 0 = uncapped
	Swept         money.Kobo `json:"swept_kobo"`       // lifetime swept
	SweptThisMonth money.Kobo `json:"swept_this_month_kobo"`
	Month         string     `json:"month"` // YYYY-MM of SweptThisMonth
	CreatedAt     time.Time  `json:"created_at"`
}

// Sweep is one round-up transfer into the AVC wallet.
type Sweep struct {
	ID        string     `json:"id"`
	TenantID  string     `json:"tenant_id"`
	MemberID  string     `json:"member_id"`
	TxAmount  money.Kobo `json:"tx_amount_kobo"`
	RoundedTo money.Kobo `json:"rounded_to_kobo"`
	Amount    money.Kobo `json:"amount_kobo"`
	At        time.Time  `json:"at"`
}

// Engine holds wallets and sweeps.
type Engine struct {
	mu      sync.Mutex
	wallets map[string]*Wallet // tenantID|memberID
	sweeps  []Sweep
	seq     int
}

func NewEngine() *Engine { return &Engine{wallets: map[string]*Wallet{}} }

// Open creates (or returns) a member's wallet.
func (e *Engine) Open(tenantID, memberID string, step, monthlyCap money.Kobo, at time.Time) (Wallet, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if step <= 0 {
		return Wallet{}, ErrBadStep
	}
	key := tenantID + "|" + memberID
	if w, ok := e.wallets[key]; ok {
		return *w, nil
	}
	e.seq++
	w := &Wallet{ID: fmt.Sprintf("ru-%06d", e.seq), TenantID: tenantID, MemberID: memberID,
		Step: step, MonthlyCap: monthlyCap, CreatedAt: at}
	e.wallets[key] = w
	return *w, nil
}

// SweepTx computes and books the round-up for one transaction. Amounts
// already on the step boundary contribute zero (no sweep booked). The
// monthly cap clamps the sweep; zero-cap means uncapped.
func (e *Engine) SweepTx(tenantID, memberID string, txAmount money.Kobo, at time.Time) (Sweep, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	w, ok := e.wallets[tenantID+"|"+memberID]
	if !ok {
		return Sweep{}, ErrNoWallet
	}
	if txAmount <= 0 {
		return Sweep{}, errors.New("roundup: transaction amount must be positive")
	}
	rem := txAmount % w.Step
	if rem == 0 {
		return Sweep{TenantID: tenantID, MemberID: memberID, TxAmount: txAmount, RoundedTo: txAmount, At: at}, nil
	}
	diff := w.Step - rem
	month := at.Format("2006-01")
	if w.Month != month {
		w.Month, w.SweptThisMonth = month, 0
	}
	if w.MonthlyCap > 0 {
		headroom := w.MonthlyCap - w.SweptThisMonth
		if headroom <= 0 {
			return Sweep{}, ErrCapReached
		}
		if diff > headroom {
			diff = headroom
		}
	}
	e.seq++
	s := Sweep{ID: fmt.Sprintf("rs-%06d", e.seq), TenantID: tenantID, MemberID: memberID,
		TxAmount: txAmount, RoundedTo: txAmount - rem + w.Step, Amount: diff, At: at}
	e.sweeps = append(e.sweeps, s)
	w.Swept += diff
	w.SweptThisMonth += diff
	return s, nil
}

// Wallet fetches a member's wallet.
func (e *Engine) Wallet(tenantID, memberID string) (Wallet, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	w, ok := e.wallets[tenantID+"|"+memberID]
	if !ok {
		return Wallet{}, false
	}
	return *w, true
}

// Sweeps lists a member's sweeps.
func (e *Engine) Sweeps(tenantID, memberID string) []Sweep {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Sweep
	for _, s := range e.sweeps {
		if s.TenantID == tenantID && s.MemberID == memberID {
			out = append(out, s)
		}
	}
	return out
}
