// Package diaspora implements cross-border pensions for Nigerians abroad:
// passport-based onboarding tier, FCY contributions (USD/GBP/EUR) with
// FX booking into the member's RSA in kobo, and per-member remittance
// tracking. Complements internal/fcy (fund-level FX) with the retail
// diaspora flow — a growth channel competitors under-serve.
package diaspora

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var (
	ErrBadCurrency = errors.New("diaspora: unsupported currency (USD | GBP | EUR)")
	ErrBadAmount   = errors.New("diaspora: amount must be positive")
	ErrNoMember    = errors.New("diaspora: member not enrolled")
	ErrNoFX        = errors.New("diaspora: FX rate not set for currency")
)

// Member is a diaspora enrollee (passport-tier KYC).
type Member struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	RSAID     string    `json:"rsa_id"`
	Country   string    `json:"country"` // ISO 3166-1 alpha-2
	PassportRef string  `json:"passport_ref"`
	EnrolledAt time.Time `json:"enrolled_at"`
}

// Contribution is one FCY remittance booked into the RSA.
type Contribution struct {
	ID           string     `json:"id"`
	TenantID     string     `json:"tenant_id"`
	RSAID        string     `json:"rsa_id"`
	Currency     string     `json:"currency"`
	AmountMinor  int64      `json:"amount_minor"` // cents/pence
	RateKoboPerUnit int64   `json:"rate_kobo_per_unit"` // kobo per 1.00 unit
	Credited     money.Kobo `json:"credited_kobo"`
	At           time.Time  `json:"at"`
}

// Engine holds members, FX rates and contributions.
type Engine struct {
	mu       sync.Mutex
	members  map[string]*Member // tenantID|rsaID
	rates    map[string]int64   // currency -> kobo per unit
	contribs []Contribution
	seq      int
}

func NewEngine() *Engine {
	return &Engine{members: map[string]*Member{}, rates: map[string]int64{}}
}

// SetRate sets the booking rate (kobo per 1.00 unit of currency).
func (e *Engine) SetRate(currency string, koboPerUnit int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rates[currency] = koboPerUnit
}

// Enrol registers a diaspora member on the passport tier.
func (e *Engine) Enrol(tenantID, rsaID, country, passportRef string, at time.Time) (Member, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	m := &Member{ID: fmt.Sprintf("dia-%06d", e.seq), TenantID: tenantID, RSAID: rsaID,
		Country: country, PassportRef: passportRef, EnrolledAt: at}
	e.members[tenantID+"|"+rsaID] = m
	return *m, nil
}

// Contribute books an FCY remittance: minor units × rate / 100 → kobo.
func (e *Engine) Contribute(tenantID, rsaID, currency string, amountMinor int64, at time.Time) (Contribution, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch currency {
	case "USD", "GBP", "EUR":
	default:
		return Contribution{}, ErrBadCurrency
	}
	if amountMinor <= 0 {
		return Contribution{}, ErrBadAmount
	}
	if _, ok := e.members[tenantID+"|"+rsaID]; !ok {
		return Contribution{}, ErrNoMember
	}
	rate, ok := e.rates[currency]
	if !ok || rate <= 0 {
		return Contribution{}, ErrNoFX
	}
	e.seq++
	c := Contribution{ID: fmt.Sprintf("dc-%06d", e.seq), TenantID: tenantID, RSAID: rsaID,
		Currency: currency, AmountMinor: amountMinor, RateKoboPerUnit: rate,
		Credited: money.Kobo(amountMinor * rate / 100), At: at}
	e.contribs = append(e.contribs, c)
	return c, nil
}

// Contributions lists a member's remittances with the kobo total.
func (e *Engine) Contributions(tenantID, rsaID string) ([]Contribution, money.Kobo) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Contribution
	var total money.Kobo
	for _, c := range e.contribs {
		if c.TenantID == tenantID && c.RSAID == rsaID {
			out = append(out, c)
			total += c.Credited
		}
	}
	return out, total
}
