// Package mpg implements the Minimum Pension Guarantee (MPG) top-up engine
// and the Pension Protection Fund (PPF) contribution ledger.
//
// Regulatory basis: PRA 2014 s.84 empowers PenCom to guarantee a minimum
// pension for CPS retirees. PenCom's November 2025 directive (Presidential
// Pension Security Initiative launch) sets the first MPG at ₦52,500/month,
// funded by the Pension Protection Fund (PPF) — contributions from the
// Federal Government, the pension industry, states, and (urged) private
// sector employers. The figure is actuarially validated and subject to
// periodic review. Sector gating: STATE-sector retirees qualify only when
// their state has contributed to the PPF; private-sector and federal
// retirees qualify unconditionally.
//
// Money is integer kobo throughout (DA-03) — no floats.
package mpg

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

// DefaultMinimumMonthly is the ₦52,500/month MPG floor (PenCom directive,
// November 2025). The value is configurable per directive revision (the
// MPG is subject to periodic actuarial review).
const DefaultMinimumMonthly = money.Kobo(52500 * 100)

// Sector classifies a retiree's CPS service sector. Sector drives PPF
// access gating: states must contribute to the PPF before their retirees
// can draw MPG top-ups.
type Sector string

const (
	SectorPrivate Sector = "PRIVATE" // CPS private-sector retirees
	SectorFederal Sector = "FEDERAL" // federal civil-service retirees
	SectorState   Sector = "STATE"   // state civil-service retirees (gated)
)

// PPFSource classifies a contribution into the Pension Protection Fund.
type PPFSource string

const (
	SourceFG       PPFSource = "FG"       // Federal Government appropriation
	SourceIndustry PPFSource = "INDUSTRY" // pension-industry levy
	SourceState    PPFSource = "STATE"    // state government contribution
	SourceEmployer PPFSource = "EMPLOYER" // private-sector employer contribution
)

var (
	ErrNotEligible   = errors.New("mpg: pension meets or exceeds the minimum guarantee")
	ErrStateNotInPPF = errors.New("mpg: state has not contributed to the Pension Protection Fund — retirees not eligible")
	ErrBadAmount     = errors.New("mpg: amount must be positive")
	ErrUnknownSource = errors.New("mpg: unknown PPF contribution source")
)

// Assessment is the MPG decision for one retiree in one payroll period.
type Assessment struct {
	ID             string     `json:"id"`
	TenantID       string     `json:"tenant_id"`
	RSAID          string     `json:"rsa_id"`
	Sector         Sector     `json:"sector"`
	StateCode      string     `json:"state_code,omitempty"` // STATE sector only
	Period         string     `json:"period"`               // YYYY-MM
	ComputedPension money.Kobo `json:"computed_pension_kobo"`
	Minimum        money.Kobo `json:"minimum_kobo"`
	TopUp          money.Kobo `json:"topup_kobo"` // 0 when not eligible
	Eligible       bool       `json:"eligible"`
	Gated          bool       `json:"gated"` // true: below floor but blocked (state not in PPF)
	AssessedAt     time.Time  `json:"assessed_at"`
}

// Contribution is one receipt into the Pension Protection Fund. Records
// are WORM (append-only) — corrections are reversal entries.
type Contribution struct {
	ID             string     `json:"id"`
	TenantID       string     `json:"tenant_id"`
	Source         PPFSource  `json:"source"`
	ContributorRef string     `json:"contributor_ref"` // state code, employer ID, "FG", or "INDUSTRY"
	Amount         money.Kobo `json:"amount_kobo"`
	Period         string     `json:"period"` // YYYY-MM
	ReceivedAt     time.Time  `json:"received_at"`
}

// Review carries the actuarial-validation metadata for the MPG floor.
type Review struct {
	Minimum           money.Kobo `json:"minimum_kobo"`
	ActuariallyValid  bool       `json:"actuarially_validated"`
	ValidatedBy       string     `json:"validated_by,omitempty"` // actuary / PenCom circular ref
	EffectivePeriod   string     `json:"effective_period"`       // YYYY-MM from which the floor applies
	NextReviewDue     time.Time  `json:"next_review_due"`        // periodic review schedule
}

// Engine holds the MPG floor, the PPF ledger, and the state-gating set.
type Engine struct {
	mu          sync.Mutex
	review      Review
	contribs    []Contribution
	stateFunded map[string]bool // state code -> contributed to PPF
	seq         int
}

// NewEngine builds an engine on the default ₦52,500 floor.
func NewEngine() *Engine {
	return &Engine{
		review: Review{
			Minimum:          DefaultMinimumMonthly,
			ActuariallyValid: true,
			EffectivePeriod:  "2025-11",
		},
		stateFunded: map[string]bool{},
	}
}

// Floor returns the current MPG floor and its review metadata.
func (e *Engine) Floor() Review {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.review
}

// SetFloor applies a revised MPG floor (periodic actuarial review). The
// floor must be actuarially validated before it can take effect.
func (e *Engine) SetFloor(minimum money.Kobo, validated bool, validatedBy, effectivePeriod string, nextReview time.Time) error {
	if !validated {
		return errors.New("mpg: revised floor must be actuarially validated before effect")
	}
	if minimum <= 0 {
		return ErrBadAmount
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.review = Review{
		Minimum: minimum, ActuariallyValid: true, ValidatedBy: validatedBy,
		EffectivePeriod: effectivePeriod, NextReviewDue: nextReview,
	}
	return nil
}

// RecordContribution books a receipt into the PPF (append-only). A STATE
// contribution marks that state as PPF-funded, unlocking MPG access for
// its retirees.
func (e *Engine) RecordContribution(tenantID string, src PPFSource, ref string, amount money.Kobo, period string, at time.Time) (Contribution, error) {
	switch src {
	case SourceFG, SourceIndustry, SourceState, SourceEmployer:
	default:
		return Contribution{}, ErrUnknownSource
	}
	if amount <= 0 {
		return Contribution{}, ErrBadAmount
	}
	if ref == "" {
		return Contribution{}, errors.New("mpg: contributor reference required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	c := Contribution{
		ID: fmt.Sprintf("ppf-%06d", e.seq), TenantID: tenantID,
		Source: src, ContributorRef: ref, Amount: amount,
		Period: period, ReceivedAt: at.UTC(),
	}
	e.contribs = append(e.contribs, c)
	if src == SourceState {
		e.stateFunded[ref] = true
	}
	return c, nil
}

// StateFunded reports whether a state has contributed to the PPF (the
// access precondition for STATE-sector retirees).
func (e *Engine) StateFunded(stateCode string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stateFunded[stateCode]
}

// PPFAggregates returns total PPF receipts grouped by source.
func (e *Engine) PPFAggregates(tenantID string) map[PPFSource]money.Kobo {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[PPFSource]money.Kobo{}
	for _, c := range e.contribs {
		if c.TenantID == tenantID {
			out[c.Source] += c.Amount
		}
	}
	return out
}

// Contributions lists PPF receipts for a tenant (ledger view).
func (e *Engine) Contributions(tenantID string) []Contribution {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Contribution
	for _, c := range e.contribs {
		if c.TenantID == tenantID {
			out = append(out, c)
		}
	}
	return out
}

// Assess evaluates one retiree against the MPG floor for a payroll period.
// Below the floor → Eligible with TopUp = floor − pension, EXCEPT
// STATE-sector retirees whose state has not contributed to the PPF
// (Eligible=false, Gated=true). At/above the floor → not eligible.
func (e *Engine) Assess(tenantID, rsaID string, sector Sector, stateCode string, pension money.Kobo, period string, at time.Time) Assessment {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	a := Assessment{
		ID: fmt.Sprintf("mpg-%06d", e.seq), TenantID: tenantID, RSAID: rsaID,
		Sector: sector, Period: period,
		ComputedPension: pension, Minimum: e.review.Minimum,
		AssessedAt: at.UTC(),
	}
	if sector == SectorState {
		a.StateCode = stateCode
	}
	if pension >= e.review.Minimum {
		return a // not eligible: meets the guarantee already
	}
	if sector == SectorState && !e.stateFunded[stateCode] {
		a.Gated = true
		return a
	}
	a.Eligible = true
	a.TopUp = e.review.Minimum - pension
	return a
}

// AssessPayroll runs Assess over a payroll batch, returning the
// assessments and the total top-up drawn from the PPF for the period.
func (e *Engine) AssessPayroll(tenantID, period string, rows []PayrollRow, at time.Time) ([]Assessment, money.Kobo) {
	out := make([]Assessment, 0, len(rows))
	var total money.Kobo
	for _, r := range rows {
		a := e.Assess(tenantID, r.RSAID, r.Sector, r.StateCode, r.MonthlyPension, period, at)
		out = append(out, a)
		total += a.TopUp
	}
	return out, total
}

// PayrollRow is the per-retiree payroll input the MPG engine needs.
type PayrollRow struct {
	RSAID          string
	Sector         Sector
	StateCode      string
	MonthlyPension money.Kobo
}
