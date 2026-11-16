// Package pencare implements the Pension Industry Healthcare Plan for
// Retirees (PenCare) registry.
//
// Regulatory basis: PenCare was launched alongside the Minimum Pension
// Guarantee at the Presidential Pension Security Initiative (November
// 2025). It provides free essential healthcare to low-income pensioners,
// funded by the pension industry (₦7bn+ committed at launch, premiums
// already paid for ~30,000 retirees). Eligible retirees request health
// access cards through their PFA. PFAs therefore need: an eligibility
// screen (low-income = monthly pension below the MPG floor), an enrollee
// registry, a card request/issue lifecycle, premium-payment tracking, and
// an auditable fund-contribution ledger.
//
// Money is integer kobo throughout (DA-03) — no floats. Premium and fund
// ledgers are WORM (append-only); corrections are reversal entries.
package pencare

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

// FundSource classifies a contribution into the PenCare fund.
type FundSource string

const (
	SourceIndustry FundSource = "INDUSTRY" // pension-industry pool (PenOp operators)
	SourcePFA      FundSource = "PFA"      // individual PFA contribution
	SourcePenCom   FundSource = "PENCOM"   // regulator contribution
	SourceDonor    FundSource = "DONOR"    // development partner / donor
)

// EnrolleeStatus is the lifecycle of a PenCare enrollee.
type EnrolleeStatus string

const (
	EnrolleeActive    EnrolleeStatus = "ACTIVE"
	EnrolleeSuspended EnrolleeStatus = "SUSPENDED"
	EnrolleeExited    EnrolleeStatus = "EXITED" // deceased or no longer eligible
)

// CardStatus is the lifecycle of a health access card.
type CardStatus string

const (
	CardRequested CardStatus = "REQUESTED" // retiree asked their PFA for a card
	CardIssued    CardStatus = "ISSUED"    // card number assigned
	CardActive    CardStatus = "ACTIVE"    // usable at providers
	CardSuspended CardStatus = "SUSPENDED"
)

var (
	ErrNotLowIncome  = errors.New("pencare: pension meets or exceeds the MPG floor — not a low-income retiree")
	ErrUnknownSource = errors.New("pencare: unknown fund contribution source")
	ErrBadAmount     = errors.New("pencare: amount must be positive")
	ErrNoEnrollee    = errors.New("pencare: enrollee not found")
	ErrNotActive     = errors.New("pencare: enrollee is not active")
	ErrCardExists    = errors.New("pencare: enrollee already has a live card")
	ErrNoCard        = errors.New("pencare: card not found")
	ErrBadTransition = errors.New("pencare: invalid card status transition")
)

// Enrollee is a low-income retiree registered for PenCare.
type Enrollee struct {
	ID             string         `json:"id"`
	TenantID       string         `json:"tenant_id"`
	RSAID          string         `json:"rsa_id"`
	MonthlyPension money.Kobo     `json:"monthly_pension_kobo"`
	Floor          money.Kobo     `json:"floor_kobo"` // MPG floor at enrolment
	Status         EnrolleeStatus `json:"status"`
	EnrolledAt     time.Time      `json:"enrolled_at"`
}

// Card is a PenCare health access card for one enrollee.
type Card struct {
	ID          string     `json:"id"`
	TenantID    string     `json:"tenant_id"`
	EnrolleeID  string     `json:"enrollee_id"`
	CardNumber  string     `json:"card_number,omitempty"`
	Status      CardStatus `json:"status"`
	RequestedAt time.Time  `json:"requested_at"`
	IssuedAt    *time.Time `json:"issued_at,omitempty"`
}

// Premium is one insurance premium payment covering an enrollee for a
// period. WORM (append-only).
type Premium struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	EnrolleeID string     `json:"enrollee_id"`
	InsurerRef string     `json:"insurer_ref"` // HMO / insurer reference
	Amount     money.Kobo `json:"amount_kobo"`
	Period     string     `json:"period"` // YYYY-MM coverage period
	PaidAt     time.Time  `json:"paid_at"`
}

// FundContribution is one receipt into the PenCare industry fund.
// WORM (append-only).
type FundContribution struct {
	ID             string     `json:"id"`
	TenantID       string     `json:"tenant_id"`
	Source         FundSource `json:"source"`
	ContributorRef string     `json:"contributor_ref"` // operator/donor identifier
	Amount         money.Kobo `json:"amount_kobo"`
	Period         string     `json:"period"`
	ReceivedAt     time.Time  `json:"received_at"`
}

// Engine is the in-memory PenCare core. Persistence is layered on via
// store.PenCareRepo (write-through, mirroring the mpg pattern).
type Engine struct {
	mu        sync.Mutex
	floor     money.Kobo
	enrollees map[string]*Enrollee // key: tenantID + "|" + id
	byRSA     map[string]string    // tenantID|rsaID -> enrollee ID
	cards     map[string]*Card
	premiums  []Premium
	fund      []FundContribution
	seq       int
}

// NewEngine builds a PenCare engine bound to the current MPG floor.
func NewEngine(floor money.Kobo) *Engine {
	return &Engine{
		floor:     floor,
		enrollees: map[string]*Enrollee{},
		byRSA:     map[string]string{},
		cards:     map[string]*Card{},
	}
}

// Floor returns the MPG floor used for low-income screening.
func (e *Engine) Floor() money.Kobo { return e.floor }

func validSource(s FundSource) bool {
	switch s {
	case SourceIndustry, SourcePFA, SourcePenCom, SourceDonor:
		return true
	}
	return false
}

func (e *Engine) nextID(prefix string) string {
	e.seq++
	return fmt.Sprintf("%s-%06d", prefix, e.seq)
}

// Enroll registers a retiree. Eligibility: monthly pension strictly below
// the MPG floor (the PenCare "low-income pensioner" definition). One
// enrollee per RSA per tenant; re-enrolment returns the existing record.
func (e *Engine) Enroll(tenantID, rsaID string, pension money.Kobo, at time.Time) (Enrollee, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if pension >= e.floor {
		return Enrollee{}, ErrNotLowIncome
	}
	key := tenantID + "|" + rsaID
	if id, ok := e.byRSA[key]; ok {
		return *e.enrollees[tenantID+"|"+id], nil
	}
	en := &Enrollee{
		ID:             e.nextID("pc-enr"),
		TenantID:       tenantID,
		RSAID:          rsaID,
		MonthlyPension: pension,
		Floor:          e.floor,
		Status:         EnrolleeActive,
		EnrolledAt:     at,
	}
	e.enrollees[tenantID+"|"+en.ID] = en
	e.byRSA[key] = en.ID
	return *en, nil
}

// Enrollee fetches one enrollee.
func (e *Engine) Enrollee(tenantID, id string) (Enrollee, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	en, ok := e.enrollees[tenantID+"|"+id]
	if !ok {
		return Enrollee{}, false
	}
	return *en, true
}

// Enrollees lists enrollees for a tenant.
func (e *Engine) Enrollees(tenantID string) []Enrollee {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Enrollee
	for _, en := range e.enrollees {
		if en.TenantID == tenantID {
			out = append(out, *en)
		}
	}
	return out
}

// SetStatus moves an enrollee between ACTIVE/SUSPENDED/EXITED.
func (e *Engine) SetStatus(tenantID, id string, st EnrolleeStatus) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	en, ok := e.enrollees[tenantID+"|"+id]
	if !ok {
		return ErrNoEnrollee
	}
	en.Status = st
	return nil
}

// RequestCard records a card request ("demand on their card" at the PFA).
// At most one live (REQUESTED/ISSUED/ACTIVE) card per enrollee.
func (e *Engine) RequestCard(tenantID, enrolleeID string, at time.Time) (Card, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	en, ok := e.enrollees[tenantID+"|"+enrolleeID]
	if !ok {
		return Card{}, ErrNoEnrollee
	}
	if en.Status != EnrolleeActive {
		return Card{}, ErrNotActive
	}
	for _, c := range e.cards {
		if c.TenantID == tenantID && c.EnrolleeID == enrolleeID &&
			(c.Status == CardRequested || c.Status == CardIssued || c.Status == CardActive) {
			return Card{}, ErrCardExists
		}
	}
	c := &Card{
		ID:          e.nextID("pc-card"),
		TenantID:    tenantID,
		EnrolleeID:  enrolleeID,
		Status:      CardRequested,
		RequestedAt: at,
	}
	e.cards[tenantID+"|"+c.ID] = c
	return *c, nil
}

// IssueCard assigns a card number and moves REQUESTED -> ISSUED.
func (e *Engine) IssueCard(tenantID, cardID, cardNumber string, at time.Time) (Card, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.cards[tenantID+"|"+cardID]
	if !ok {
		return Card{}, ErrNoCard
	}
	if c.Status != CardRequested {
		return Card{}, ErrBadTransition
	}
	c.CardNumber = cardNumber
	c.Status = CardIssued
	c.IssuedAt = &at
	return *c, nil
}

// ActivateCard moves ISSUED -> ACTIVE (usable at healthcare providers).
func (e *Engine) ActivateCard(tenantID, cardID string) (Card, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.cards[tenantID+"|"+cardID]
	if !ok {
		return Card{}, ErrNoCard
	}
	if c.Status != CardIssued {
		return Card{}, ErrBadTransition
	}
	c.Status = CardActive
	return *c, nil
}

// Cards lists cards, optionally filtered by enrollee.
func (e *Engine) Cards(tenantID, enrolleeID string) []Card {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Card
	for _, c := range e.cards {
		if c.TenantID == tenantID && (enrolleeID == "" || c.EnrolleeID == enrolleeID) {
			out = append(out, *c)
		}
	}
	return out
}

// RecordPremium appends a premium payment for an active enrollee. WORM.
func (e *Engine) RecordPremium(tenantID, enrolleeID, insurerRef string, amount money.Kobo, period string, at time.Time) (Premium, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if amount <= 0 {
		return Premium{}, ErrBadAmount
	}
	en, ok := e.enrollees[tenantID+"|"+enrolleeID]
	if !ok {
		return Premium{}, ErrNoEnrollee
	}
	if en.Status != EnrolleeActive {
		return Premium{}, ErrNotActive
	}
	p := Premium{
		ID:         e.nextID("pc-prem"),
		TenantID:   tenantID,
		EnrolleeID: enrolleeID,
		InsurerRef: insurerRef,
		Amount:     amount,
		Period:     period,
		PaidAt:     at,
	}
	e.premiums = append(e.premiums, p)
	return p, nil
}

// Premiums lists premium payments, optionally filtered by enrollee.
func (e *Engine) Premiums(tenantID, enrolleeID string) []Premium {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Premium
	for _, p := range e.premiums {
		if p.TenantID == tenantID && (enrolleeID == "" || p.EnrolleeID == enrolleeID) {
			out = append(out, p)
		}
	}
	return out
}

// RecordFundContribution appends a receipt into the PenCare fund. WORM.
func (e *Engine) RecordFundContribution(tenantID string, src FundSource, ref string, amount money.Kobo, period string, at time.Time) (FundContribution, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validSource(src) {
		return FundContribution{}, ErrUnknownSource
	}
	if amount <= 0 {
		return FundContribution{}, ErrBadAmount
	}
	fc := FundContribution{
		ID:             e.nextID("pc-fund"),
		TenantID:       tenantID,
		Source:         src,
		ContributorRef: ref,
		Amount:         amount,
		Period:         period,
		ReceivedAt:     at,
	}
	e.fund = append(e.fund, fc)
	return fc, nil
}

// FundContributions lists fund receipts for a tenant.
func (e *Engine) FundContributions(tenantID string) []FundContribution {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []FundContribution
	for _, fc := range e.fund {
		if fc.TenantID == tenantID {
			out = append(out, fc)
		}
	}
	return out
}

// FundAggregates totals fund receipts by source for a tenant.
func (e *Engine) FundAggregates(tenantID string) map[FundSource]money.Kobo {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[FundSource]money.Kobo{}
	for _, fc := range e.fund {
		if fc.TenantID == tenantID {
			out[fc.Source] += fc.Amount
		}
	}
	return out
}

// Coverage reports enrollee/card/premium headline numbers for a tenant.
func (e *Engine) Coverage(tenantID string) (enrollees, activeCards int, premiumsPaid money.Kobo) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, en := range e.enrollees {
		if en.TenantID == tenantID && en.Status == EnrolleeActive {
			enrollees++
		}
	}
	for _, c := range e.cards {
		if c.TenantID == tenantID && c.Status == CardActive {
			activeCards++
		}
	}
	for _, p := range e.premiums {
		if p.TenantID == tenantID {
			premiumsPaid += p.Amount
		}
	}
	return
}
