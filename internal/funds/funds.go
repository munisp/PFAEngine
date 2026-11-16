// Package funds implements FR-304 and the PenCom multi-fund structure:
// RSA funds I–VI with daily NAV per unit and contribution unitization.
package funds

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var ErrNoPrice = errors.New("funds: no NAV published for that date")

// PenCom multi-fund structure (rules://ng/pencom/multifund@2023).
const (
	FundI   = "FUND_I"   // aggressive, active opt-in only
	FundII  = "FUND_II"  // default, age ≤49
	FundIII = "FUND_III" // default, age 50+
	FundIV  = "FUND_IV"  // retirees
	FundV   = "FUND_V"   // micro pension (PPP)
	FundVI  = "FUND_VI"  // non-interest (Sharia, SCM-12)
)

// DefaultFund assigns a fund by age and status (multi-fund rules).
func DefaultFund(ageYears int, retired bool, shariaOptIn bool, aggressiveOptIn bool) string {
	if shariaOptIn {
		return FundVI
	}
	if retired {
		return FundIV
	}
	if aggressiveOptIn && ageYears <= 49 {
		return FundI
	}
	if ageYears <= 49 {
		return FundII
	}
	return FundIII
}

type Fund struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Name     string `json:"name"`
	Currency string `json:"currency"`
}

// NAV is the daily price per unit in kobo (FR-304).
type NAV struct {
	FundID string     `json:"fund_id"`
	Date   string     `json:"date"`       // YYYY-MM-DD
	Price  money.Kobo `json:"price_kobo"` // per whole unit
}

type Holding struct {
	FundID string     `json:"fund_id"`
	Units  money.Unit `json:"units"`
}

type Registry struct {
	mu       sync.Mutex
	funds    map[string]*Fund
	navs     map[string]map[string]money.Kobo // fundID -> date -> price
	holdings map[string]map[string]money.Unit // rsaID -> fundID -> units
}

func NewRegistry() *Registry {
	return &Registry{
		funds: map[string]*Fund{}, navs: map[string]map[string]money.Kobo{},
		holdings: map[string]map[string]money.Unit{},
	}
}

func (r *Registry) Register(f Fund) { r.mu.Lock(); r.funds[f.ID] = &f; r.mu.Unlock() }

// PublishNAV publishes the daily price; emits fund.nav.published upstream.
func (r *Registry) PublishNAV(fundID, date string, price money.Kobo) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.funds[fundID]; !ok {
		return fmt.Errorf("funds: unknown fund %s", fundID)
	}
	if r.navs[fundID] == nil {
		r.navs[fundID] = map[string]money.Kobo{}
	}
	r.navs[fundID][date] = price
	return nil
}

func (r *Registry) Price(fundID, date string) (money.Kobo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.navs[fundID][date]; ok {
		return p, nil
	}
	return 0, ErrNoPrice
}

// Allocate converts a posted contribution into units at the day's price
// and credits the RSA holding (FR-304).
func (r *Registry) Allocate(rsaID, fundID, date string, amount money.Kobo) (money.Unit, error) {
	price, err := r.Price(fundID, date)
	if err != nil {
		return 0, err
	}
	u, err := money.AllocateUnits(amount, price)
	if err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.holdings[rsaID] == nil {
		r.holdings[rsaID] = map[string]money.Unit{}
	}
	r.holdings[rsaID][fundID] += u
	return u, nil
}

// Value returns the current kobo value of an RSA's holdings at latest NAV.
func (r *Registry) Value(rsaID, date string) money.Kobo {
	r.mu.Lock()
	hs := map[string]money.Unit{}
	for f, u := range r.holdings[rsaID] {
		hs[f] = u
	}
	r.mu.Unlock()
	var total money.Kobo
	for f, u := range hs {
		if p, err := r.Price(f, date); err == nil {
			total += money.ValueUnits(u, p)
		}
	}
	return total
}

func (r *Registry) Holdings(rsaID string) []Holding {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Holding
	for f, u := range r.holdings[rsaID] {
		out = append(out, Holding{FundID: f, Units: u})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FundID < out[j].FundID })
	return out
}

func Today() string { return time.Now().UTC().Format("2006-01-02") }
