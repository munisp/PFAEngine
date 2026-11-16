// Package invmandate implements the infrastructure-investment mandate
// registry: the platform-side machinery for pension-industry co-investment
// vehicles such as the FSD Africa–backed $750m infrastructure platform
// announced with the Presidential Pension Security Initiative (first
// $250m committed, deployment targeted H1 2027).
//
// A PFA needs to: register a mandate (asset class, partner, target size,
// currency), record per-RSA-fund commitments against it, enforce the
// PenCom per-fund exposure cap for the asset class (configurable in basis
// points — default 5% of fund AUM per the Regulation on Investment of
// Pension Fund Assets), and track deployment of committed capital into
// projects. Multi-currency: amounts are integer minor units (kobo for
// NGN, cents for USD); cap checks convert to NGN via a configurable FX
// rate. The per-holding compliance screen remains internal/investment —
// this package governs the allocation lifecycle that feeds it.
//
// All amounts are integer minor units — no floats (DA-03). Commitments
// and deployments are WORM (append-only).
package invmandate

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

// AssetClass classifies a mandate.
type AssetClass string

const (
	ClassInfraFund AssetClass = "INFRASTRUCTURE_FUND" // infra private-equity funds
	ClassInfraBond AssetClass = "INFRASTRUCTURE_BOND" // infra bonds/sukuk
)

// MandateStatus is the lifecycle of a mandate.
type MandateStatus string

const (
	MandateOpen   MandateStatus = "OPEN"   // accepting commitments
	MandateClosed MandateStatus = "CLOSED" // fully committed / fundraising over
)

// DefaultInfraCapBps is the default per-fund exposure cap for
// infrastructure assets: 5% of fund AUM (500 basis points). Configurable
// per mandate to track the current PenCom investment regulation.
const DefaultInfraCapBps int64 = 500

var (
	ErrBadAmount      = errors.New("invmandate: amount must be positive")
	ErrBadCap         = errors.New("invmandate: cap must be in (0, 10000] basis points")
	ErrNoMandate      = errors.New("invmandate: mandate not found")
	ErrMandateClosed  = errors.New("invmandate: mandate is closed to new commitments")
	ErrCurrencyMix    = errors.New("invmandate: currency does not match mandate currency")
	ErrCapExceeded    = errors.New("invmandate: commitment would breach the fund's exposure cap for this mandate")
	ErrOverSubscribed = errors.New("invmandate: commitment would exceed the mandate target size")
	ErrOverDeployed   = errors.New("invmandate: deployment exceeds undrawn commitments")
	ErrNoAUM          = errors.New("invmandate: fund AUM not set — cannot run cap check")
)

// Mandate is one co-investment vehicle (e.g. the FSD Africa infra bond).
type Mandate struct {
	ID          string        `json:"id"`
	TenantID    string        `json:"tenant_id"`
	Name        string        `json:"name"`
	AssetClass  AssetClass    `json:"asset_class"`
	Partner     string        `json:"partner"`      // e.g. "FSD Africa"
	Currency    string        `json:"currency"`     // ISO 4217: NGN, USD
	TargetMinor int64         `json:"target_minor"` // target fund size, minor units
	CapBps      int64         `json:"cap_bps"`      // per-fund exposure cap, basis points of AUM
	Status      MandateStatus `json:"status"`
	CreatedAt   time.Time     `json:"created_at"`
}

// Commitment is one RSA fund's committed allocation to a mandate. WORM.
type Commitment struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	MandateID   string    `json:"mandate_id"`
	FundID      string    `json:"fund_id"` // RSA multi-fund: "I", "II", "III", "IV", ...
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	Ref         string    `json:"ref"` // board/investment-committee reference
	CommittedAt time.Time `json:"committed_at"`
}

// Deployment is a drawdown of committed capital into a project/SPV. WORM.
type Deployment struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	MandateID   string    `json:"mandate_id"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	Vehicle     string    `json:"vehicle"` // project / SPV / tranche reference
	DeployedAt  time.Time `json:"deployed_at"`
}

// Engine is the in-memory mandate core; persistence layers on via
// store.InvMandateRepo (write-through, mirroring the mpg pattern).
type Engine struct {
	mu           sync.Mutex
	mandates     map[string]*Mandate // tenantID|id
	commitments  []Commitment
	deployments  []Deployment
	aum          map[string]money.Kobo // tenantID|fundID -> AUM in kobo
	fxKoboPerUSD int64                 // NGN kobo per 1 USD (e.g. 153000 = ₦1,530.00)
	seq          int
}

// NewEngine builds an empty registry.
func NewEngine() *Engine {
	return &Engine{mandates: map[string]*Mandate{}, aum: map[string]money.Kobo{}}
}

// SetFundAUM sets a tenant's RSA-fund AUM (kobo) used for cap checks.
func (e *Engine) SetFundAUM(tenantID, fundID string, aum money.Kobo) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.aum[tenantID+"|"+fundID] = aum
}

// SetFX sets the NGN-kobo-per-USD conversion rate for cap checks.
func (e *Engine) SetFX(koboPerUSD int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fxKoboPerUSD = koboPerUSD
}

func (e *Engine) nextID(prefix string) string {
	e.seq++
	return fmt.Sprintf("%s-%06d", prefix, e.seq)
}

// toKobo converts a minor-unit amount in cur to NGN kobo.
func (e *Engine) toKobo(amountMinor int64, cur string) money.Kobo {
	if cur == "NGN" {
		return money.Kobo(amountMinor)
	}
	// USD cents -> kobo: cents * koboPerUSD / 100
	return money.Kobo(amountMinor * e.fxKoboPerUSD / 100)
}

// CreateMandate registers a new mandate. CapBps defaults to
// DefaultInfraCapBps when zero.
func (e *Engine) CreateMandate(tenantID, name string, class AssetClass, partner, currency string, targetMinor, capBps int64, at time.Time) (Mandate, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if targetMinor <= 0 {
		return Mandate{}, ErrBadAmount
	}
	if capBps == 0 {
		capBps = DefaultInfraCapBps
	}
	if capBps < 0 || capBps > 10000 {
		return Mandate{}, ErrBadCap
	}
	m := &Mandate{
		ID:          e.nextID("im"),
		TenantID:    tenantID,
		Name:        name,
		AssetClass:  class,
		Partner:     partner,
		Currency:    currency,
		TargetMinor: targetMinor,
		CapBps:      capBps,
		Status:      MandateOpen,
		CreatedAt:   at,
	}
	e.mandates[tenantID+"|"+m.ID] = m
	return *m, nil
}

// Mandate fetches one mandate.
func (e *Engine) Mandate(tenantID, id string) (Mandate, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, ok := e.mandates[tenantID+"|"+id]
	if !ok {
		return Mandate{}, false
	}
	return *m, true
}

// Mandates lists a tenant's mandates.
func (e *Engine) Mandates(tenantID string) []Mandate {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Mandate
	for _, m := range e.mandates {
		if m.TenantID == tenantID {
			out = append(out, *m)
		}
	}
	return out
}

// CloseMandate stops new commitments (fundraising complete).
func (e *Engine) CloseMandate(tenantID, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, ok := e.mandates[tenantID+"|"+id]
	if !ok {
		return ErrNoMandate
	}
	m.Status = MandateClosed
	return nil
}

// committedByFundMandate sums a fund's commitments to a mandate (kobo).
// Caller must hold e.mu.
func (e *Engine) committedByFundMandate(tenantID, mandateID, fundID string) money.Kobo {
	var total money.Kobo
	for _, c := range e.commitments {
		if c.TenantID == tenantID && c.MandateID == mandateID && c.FundID == fundID {
			total += e.toKobo(c.AmountMinor, c.Currency)
		}
	}
	return total
}

// committedTotal sums all commitments to a mandate (kobo). Caller holds mu.
func (e *Engine) committedTotal(tenantID, mandateID string) money.Kobo {
	var total money.Kobo
	for _, c := range e.commitments {
		if c.TenantID == tenantID && c.MandateID == mandateID {
			total += e.toKobo(c.AmountMinor, c.Currency)
		}
	}
	return total
}

// RecordCommitment appends a fund's commitment after three gates:
// mandate open, mandate target not oversubscribed, and the fund's
// per-mandate exposure cap (capBps of fund AUM) not breached. WORM.
func (e *Engine) RecordCommitment(tenantID, mandateID, fundID string, amountMinor int64, currency, ref string, at time.Time) (Commitment, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, ok := e.mandates[tenantID+"|"+mandateID]
	if !ok {
		return Commitment{}, ErrNoMandate
	}
	if m.Status != MandateOpen {
		return Commitment{}, ErrMandateClosed
	}
	if amountMinor <= 0 {
		return Commitment{}, ErrBadAmount
	}
	if currency != m.Currency {
		return Commitment{}, ErrCurrencyMix
	}
	aum, ok := e.aum[tenantID+"|"+fundID]
	if !ok {
		return Commitment{}, ErrNoAUM
	}
	newKobo := e.toKobo(amountMinor, currency)
	// Gate 1: mandate target (in mandate currency space via kobo compare
	// is fine because currency is uniform per mandate).
	if e.committedTotal(tenantID, mandateID)+newKobo > e.toKobo(m.TargetMinor, m.Currency) {
		return Commitment{}, ErrOverSubscribed
	}
	// Gate 2: per-fund exposure cap for this mandate.
	capKobo := money.Kobo(int64(aum) * m.CapBps / 10000)
	if e.committedByFundMandate(tenantID, mandateID, fundID)+newKobo > capKobo {
		return Commitment{}, ErrCapExceeded
	}
	c := Commitment{
		ID:          e.nextID("ic"),
		TenantID:    tenantID,
		MandateID:   mandateID,
		FundID:      fundID,
		AmountMinor: amountMinor,
		Currency:    currency,
		Ref:         ref,
		CommittedAt: at,
	}
	e.commitments = append(e.commitments, c)
	return c, nil
}

// Commitments lists commitments, optionally filtered by mandate.
func (e *Engine) Commitments(tenantID, mandateID string) []Commitment {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Commitment
	for _, c := range e.commitments {
		if c.TenantID == tenantID && (mandateID == "" || c.MandateID == mandateID) {
			out = append(out, c)
		}
	}
	return out
}

// RecordDeployment draws down committed capital into a vehicle. Total
// deployments to a mandate can never exceed total commitments. WORM.
func (e *Engine) RecordDeployment(tenantID, mandateID string, amountMinor int64, currency, vehicle string, at time.Time) (Deployment, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, ok := e.mandates[tenantID+"|"+mandateID]
	if !ok {
		return Deployment{}, ErrNoMandate
	}
	if amountMinor <= 0 {
		return Deployment{}, ErrBadAmount
	}
	if currency != m.Currency {
		return Deployment{}, ErrCurrencyMix
	}
	var deployed money.Kobo
	for _, d := range e.deployments {
		if d.TenantID == tenantID && d.MandateID == mandateID {
			deployed += e.toKobo(d.AmountMinor, d.Currency)
		}
	}
	if deployed+e.toKobo(amountMinor, currency) > e.committedTotal(tenantID, mandateID) {
		return Deployment{}, ErrOverDeployed
	}
	d := Deployment{
		ID:          e.nextID("idep"),
		TenantID:    tenantID,
		MandateID:   mandateID,
		AmountMinor: amountMinor,
		Currency:    currency,
		Vehicle:     vehicle,
		DeployedAt:  at,
	}
	e.deployments = append(e.deployments, d)
	return d, nil
}

// Deployments lists deployments, optionally filtered by mandate.
func (e *Engine) Deployments(tenantID, mandateID string) []Deployment {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Deployment
	for _, d := range e.deployments {
		if d.TenantID == tenantID && (mandateID == "" || d.MandateID == mandateID) {
			out = append(out, d)
		}
	}
	return out
}

// Exposure is the headline state of one mandate, in mandate minor units.
type Exposure struct {
	MandateID      string `json:"mandate_id"`
	Currency       string `json:"currency"`
	TargetMinor    int64  `json:"target_minor"`
	CommittedMinor int64  `json:"committed_minor"`
	DeployedMinor  int64  `json:"deployed_minor"`
	HeadroomMinor  int64  `json:"headroom_minor"` // target − committed
	UndrawnMinor   int64  `json:"undrawn_minor"`  // committed − deployed
	FillBps        int64  `json:"fill_bps"`       // committed/target in basis points
}

// Exposure reports one mandate's subscription and drawdown state.
func (e *Engine) Exposure(tenantID, mandateID string) (Exposure, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, ok := e.mandates[tenantID+"|"+mandateID]
	if !ok {
		return Exposure{}, ErrNoMandate
	}
	var committed, deployed int64
	for _, c := range e.commitments {
		if c.TenantID == tenantID && c.MandateID == mandateID {
			committed += c.AmountMinor // uniform currency per mandate
		}
	}
	for _, d := range e.deployments {
		if d.TenantID == tenantID && d.MandateID == mandateID {
			deployed += d.AmountMinor
		}
	}
	return Exposure{
		MandateID:      mandateID,
		Currency:       m.Currency,
		TargetMinor:    m.TargetMinor,
		CommittedMinor: committed,
		DeployedMinor:  deployed,
		HeadroomMinor:  m.TargetMinor - committed,
		UndrawnMinor:   committed - deployed,
		FillBps:        committed * 10000 / m.TargetMinor,
	}, nil
}
