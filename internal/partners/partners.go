// Package partners implements the organizational distribution-partner
// network (Gap 10): banks, telcos, fintechs and aggregators that refer
// members into a tenant PFA. Unlike internal/agents (person-level field
// agents for micro-pension enrolment) or internal/embedded (API keys for
// embedded-finance integrations), partners are institutional relationships
// with referral-code attribution and a revenue-share ledger.
//
// The engine covers the Onboard → Approve → Suspend lifecycle, first-
// write-wins referral attribution of members, and integer-kobo revenue-
// share accrual (contribution * bps / 10000, floored) that only accrues
// while a partner is ACTIVE. Settlement reports itemise accruals per
// period. All mutations write through to the persist.Sink (NFR-10).
package partners

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/persist"
)

var (
	ErrNoPartner     = errors.New("partners: partner not found")
	ErrBadType       = errors.New("partners: type must be BANK, TELCO, FINTECH or AGGREGATOR")
	ErrBadCode       = errors.New("partners: referral code must be 3-32 chars of A-Z, 0-9 or dash")
	ErrCodeTaken     = errors.New("partners: referral code already in use for this tenant")
	ErrBadBps        = errors.New("partners: rev share bps must be in 0..10000")
	ErrBadTransition = errors.New("partners: illegal status transition")
	ErrUnknownCode   = errors.New("partners: unknown referral code")
	ErrNotActive     = errors.New("partners: partner is not active")
	ErrSuspended     = errors.New("partners: partner is suspended")
	ErrBadAmount     = errors.New("partners: contribution must be positive")
)

// Snapshot kinds owned by this engine.
const (
	KindPartner     = "partners.partner"
	KindAttribution = "partners.attribution"
	KindAccrual     = "partners.accrual"
)

// Type is the institutional class of a distribution partner.
type Type string

const (
	Bank       Type = "BANK"
	Telco      Type = "TELCO"
	Fintech    Type = "FINTECH"
	Aggregator Type = "AGGREGATOR"
)

// Status is the partner lifecycle state.
type Status string

const (
	Pending   Status = "PENDING"
	Active    Status = "ACTIVE"
	Suspended Status = "SUSPENDED"
)

// Partner is an institutional distribution partner of one tenant.
type Partner struct {
	ID            string    `json:"id"`
	TenantID      string    `json:"tenant_id"`
	Name          string    `json:"name"`
	Type          Type      `json:"type"`
	Status        Status    `json:"status"`
	ReferralCode  string    `json:"referral_code"` // unique per tenant, uppercase slug
	RevShareBps   int64     `json:"rev_share_bps"` // share of attributed contributions
	OnboardedAt   time.Time `json:"onboarded_at"`
	OnboardedBy   string    `json:"onboarded_by"`
	ApprovedAt    time.Time `json:"approved_at,omitempty"`
	ApprovedBy    string    `json:"approved_by,omitempty"`
	SuspendedAt   time.Time `json:"suspended_at,omitempty"`
	SuspendedBy   string    `json:"suspended_by,omitempty"`
	SuspendReason string    `json:"suspend_reason,omitempty"`
}

// Attribution binds a member to the partner whose referral code they
// joined with. First write wins: later codes do not rebind the member.
type Attribution struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	MemberID     string    `json:"member_id"`
	PartnerID    string    `json:"partner_id"`
	ReferralCode string    `json:"referral_code"`
	At           time.Time `json:"at"`
}

// Accrual is one revenue-share ledger entry (integer kobo, floored).
type Accrual struct {
	ID           string     `json:"id"`
	TenantID     string     `json:"tenant_id"`
	PartnerID    string     `json:"partner_id"`
	MemberID     string     `json:"member_id"`
	Contribution money.Kobo `json:"contribution_kobo"`
	Bps          int64      `json:"bps"`
	Amount       money.Kobo `json:"amount_kobo"`
	Period       string     `json:"period"` // YYYY-MM
	At           time.Time  `json:"at"`
}

// SettlementReport itemises one partner's accruals for a period.
type SettlementReport struct {
	TenantID  string     `json:"tenant_id"`
	PartnerID string     `json:"partner_id"`
	Period    string     `json:"period"`
	Lines     []Accrual  `json:"lines"`
	Total     money.Kobo `json:"total"`
	Generated time.Time  `json:"generated_at"`
}

// Engine holds partners, attributions and the accrual ledger.
type Engine struct {
	mu           sync.Mutex
	partners     map[string]*Partner     // tenantID|id
	byCode       map[string]string       // tenantID|CODE -> partner ID
	attributions map[string]*Attribution // tenantID|memberID (first-write-wins)
	accruals     map[string]*Accrual     // id
	seq          int
	sink         persist.Sink // nil in pure in-memory mode
}

// NewEngine returns an empty engine.
func NewEngine() *Engine {
	return &Engine{
		partners:     map[string]*Partner{},
		byCode:       map[string]string{},
		attributions: map[string]*Attribution{},
		accruals:     map[string]*Accrual{},
	}
}

// AttachSink wires write-through snapshot persistence (NFR-10).
func (e *Engine) AttachSink(s persist.Sink) { e.sink = s }

// Restore rehydrates partners, attributions and accruals from boot
// snapshots, continuing the ID sequence past restored state.
func (e *Engine) Restore(snaps []persist.Snapshot) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	bump := func(id, prefix string) {
		if n, err := strconv.Atoi(strings.TrimPrefix(id, prefix)); err == nil && n > e.seq {
			e.seq = n
		}
	}
	for _, s := range snaps {
		switch s.Kind {
		case KindPartner:
			var p Partner
			if err := json.Unmarshal(s.Payload, &p); err != nil {
				return err
			}
			pc := p
			e.partners[p.TenantID+"|"+p.ID] = &pc
			e.byCode[p.TenantID+"|"+p.ReferralCode] = p.ID
			bump(p.ID, "prt-")
		case KindAttribution:
			var a Attribution
			if err := json.Unmarshal(s.Payload, &a); err != nil {
				return err
			}
			ac := a
			e.attributions[a.TenantID+"|"+a.MemberID] = &ac
			bump(a.ID, "pat-")
		case KindAccrual:
			var ac Accrual
			if err := json.Unmarshal(s.Payload, &ac); err != nil {
				return err
			}
			acc := ac
			e.accruals[ac.ID] = &acc
			bump(ac.ID, "pac-")
		}
	}
	return nil
}

func normCode(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }

func validCode(code string) bool {
	if len(code) < 3 || len(code) > 32 {
		return false
	}
	for _, r := range code {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

func validType(t Type) bool {
	switch t {
	case Bank, Telco, Fintech, Aggregator:
		return true
	}
	return false
}

// Onboard registers a partner in PENDING state. The referral code is
// normalised to an uppercase slug and must be unique within the tenant.
func (e *Engine) Onboard(tenantID, name string, ptype Type, referralCode string, revShareBps int64, actor string, at time.Time) (Partner, error) {
	referralCode = normCode(referralCode)
	ptype = Type(strings.ToUpper(string(ptype)))
	switch {
	case strings.TrimSpace(name) == "":
		return Partner{}, errors.New("partners: name required")
	case !validType(ptype):
		return Partner{}, ErrBadType
	case !validCode(referralCode):
		return Partner{}, ErrBadCode
	case revShareBps < 0 || revShareBps > 10000:
		return Partner{}, ErrBadBps
	}
	e.mu.Lock()
	if _, taken := e.byCode[tenantID+"|"+referralCode]; taken {
		e.mu.Unlock()
		return Partner{}, ErrCodeTaken
	}
	e.seq++
	p := &Partner{
		ID: fmt.Sprintf("prt-%06d", e.seq), TenantID: tenantID, Name: name, Type: ptype,
		Status: Pending, ReferralCode: referralCode, RevShareBps: revShareBps,
		OnboardedAt: at, OnboardedBy: actor,
	}
	e.partners[tenantID+"|"+p.ID] = p
	e.byCode[tenantID+"|"+referralCode] = p.ID
	out := *p
	e.mu.Unlock()
	persist.Save(e.sink, KindPartner, tenantID, out.ID, out)
	return out, nil
}

func (e *Engine) get(tenantID, id string) (*Partner, error) {
	p, ok := e.partners[tenantID+"|"+id]
	if !ok {
		return nil, ErrNoPartner
	}
	return p, nil
}

// Approve moves a partner PENDING → ACTIVE (or reinstates SUSPENDED →
// ACTIVE), recording the approving actor.
func (e *Engine) Approve(tenantID, id, actor string, at time.Time) (Partner, error) {
	e.mu.Lock()
	p, err := e.get(tenantID, id)
	if err != nil {
		e.mu.Unlock()
		return Partner{}, err
	}
	if p.Status != Pending && p.Status != Suspended {
		e.mu.Unlock()
		return Partner{}, fmt.Errorf("%w: %s → ACTIVE", ErrBadTransition, p.Status)
	}
	p.Status = Active
	p.ApprovedAt, p.ApprovedBy = at, actor
	p.SuspendedAt, p.SuspendedBy, p.SuspendReason = time.Time{}, "", ""
	out := *p
	e.mu.Unlock()
	persist.Save(e.sink, KindPartner, tenantID, out.ID, out)
	return out, nil
}

// Suspend halts a partner's attribution and accrual, recording actor and
// reason for audit.
func (e *Engine) Suspend(tenantID, id, actor, reason string, at time.Time) (Partner, error) {
	e.mu.Lock()
	p, err := e.get(tenantID, id)
	if err != nil {
		e.mu.Unlock()
		return Partner{}, err
	}
	if p.Status == Suspended {
		e.mu.Unlock()
		return Partner{}, fmt.Errorf("%w: already SUSPENDED", ErrBadTransition)
	}
	p.Status = Suspended
	p.SuspendedAt, p.SuspendedBy, p.SuspendReason = at, actor, reason
	out := *p
	e.mu.Unlock()
	persist.Save(e.sink, KindPartner, tenantID, out.ID, out)
	return out, nil
}

// Partner returns one partner by ID.
func (e *Engine) Partner(tenantID, id string) (Partner, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.partners[tenantID+"|"+id]
	if !ok {
		return Partner{}, false
	}
	return *p, true
}

// Partners lists a tenant's partners, optionally filtered by status,
// ordered by name.
func (e *Engine) Partners(tenantID string, status Status) []Partner {
	e.mu.Lock()
	var out []Partner
	for _, p := range e.partners {
		if p.TenantID == tenantID && (status == "" || p.Status == status) {
			out = append(out, *p)
		}
	}
	e.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AttributeJoin binds a member to the ACTIVE partner owning the referral
// code. Idempotent and first-write-wins: if the member is already
// attributed, the existing attribution is returned unchanged (even if it
// names a different partner) so repeat joins and retries never rebind.
func (e *Engine) AttributeJoin(tenantID, memberID, referralCode string, at time.Time) (Attribution, error) {
	if strings.TrimSpace(memberID) == "" {
		return Attribution{}, errors.New("partners: member id required")
	}
	referralCode = normCode(referralCode)
	e.mu.Lock()
	if a, ok := e.attributions[tenantID+"|"+memberID]; ok {
		out := *a
		e.mu.Unlock()
		return out, nil
	}
	partnerID, ok := e.byCode[tenantID+"|"+referralCode]
	if !ok {
		e.mu.Unlock()
		return Attribution{}, ErrUnknownCode
	}
	p := e.partners[tenantID+"|"+partnerID]
	if p.Status != Active {
		e.mu.Unlock()
		return Attribution{}, ErrNotActive
	}
	e.seq++
	a := &Attribution{
		ID: fmt.Sprintf("pat-%06d", e.seq), TenantID: tenantID, MemberID: memberID,
		PartnerID: partnerID, ReferralCode: referralCode, At: at,
	}
	e.attributions[tenantID+"|"+memberID] = a
	out := *a
	e.mu.Unlock()
	persist.Save(e.sink, KindAttribution, tenantID, out.ID, out)
	return out, nil
}

// AttributionOf returns the member's attribution, if any.
func (e *Engine) AttributionOf(tenantID, memberID string) (Attribution, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.attributions[tenantID+"|"+memberID]
	if !ok {
		return Attribution{}, false
	}
	return *a, true
}

// Attributions lists members attributed to a partner, ordered by time.
func (e *Engine) Attributions(tenantID, partnerID string) []Attribution {
	e.mu.Lock()
	var out []Attribution
	for _, a := range e.attributions {
		if a.TenantID == tenantID && a.PartnerID == partnerID {
			out = append(out, *a)
		}
	}
	e.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// AccrueContribution books revenue share on an attributed member's
// contribution: amount = contribution * bps / 10000 (integer kobo, floor
// rounding). Unattributed members accrue nothing (attributed=false, nil
// error — most members join directly). Suspension blocks accrual:
// contributions from members of a SUSPENDED partner are rejected so no
// liability accrues while the relationship is under review; non-ACTIVE
// pending partners cannot hold attributions at all (AttributeJoin gates).
func (e *Engine) AccrueContribution(tenantID, memberID string, contribution money.Kobo, period string, at time.Time) (accrual Accrual, attributed bool, err error) {
	if contribution <= 0 {
		return Accrual{}, false, ErrBadAmount
	}
	e.mu.Lock()
	a, ok := e.attributions[tenantID+"|"+memberID]
	if !ok {
		e.mu.Unlock()
		return Accrual{}, false, nil
	}
	p := e.partners[tenantID+"|"+a.PartnerID]
	if p == nil {
		e.mu.Unlock()
		return Accrual{}, false, ErrNoPartner
	}
	if p.Status == Suspended {
		e.mu.Unlock()
		return Accrual{}, true, ErrSuspended
	}
	if p.Status != Active {
		e.mu.Unlock()
		return Accrual{}, true, ErrNotActive
	}
	e.seq++
	ac := Accrual{
		ID: fmt.Sprintf("pac-%06d", e.seq), TenantID: tenantID, PartnerID: p.ID,
		MemberID: memberID, Contribution: contribution, Bps: p.RevShareBps,
		Amount: contribution * money.Kobo(p.RevShareBps) / 10000,
		Period: period, At: at,
	}
	e.accruals[ac.ID] = &ac
	e.mu.Unlock()
	persist.Save(e.sink, KindAccrual, tenantID, ac.ID, ac)
	return ac, true, nil
}

// PartnerBalance sums a partner's lifetime accruals (integer kobo).
func (e *Engine) PartnerBalance(tenantID, partnerID string) money.Kobo {
	e.mu.Lock()
	defer e.mu.Unlock()
	var total money.Kobo
	for _, ac := range e.accruals {
		if ac.TenantID == tenantID && ac.PartnerID == partnerID {
			total += ac.Amount
		}
	}
	return total
}

// SettlementReport itemises a partner's accruals for one period
// (YYYY-MM), ready for payout review.
func (e *Engine) SettlementReport(tenantID, partnerID, period string, at time.Time) (SettlementReport, error) {
	e.mu.Lock()
	if _, ok := e.partners[tenantID+"|"+partnerID]; !ok {
		e.mu.Unlock()
		return SettlementReport{}, ErrNoPartner
	}
	var lines []Accrual
	var total money.Kobo
	for _, ac := range e.accruals {
		if ac.TenantID == tenantID && ac.PartnerID == partnerID && ac.Period == period {
			lines = append(lines, *ac)
			total += ac.Amount
		}
	}
	e.mu.Unlock()
	sort.Slice(lines, func(i, j int) bool { return lines[i].ID < lines[j].ID })
	return SettlementReport{
		TenantID: tenantID, PartnerID: partnerID, Period: period,
		Lines: lines, Total: total, Generated: at,
	}, nil
}
