// Package pricing implements the tenant-facing pricing product surface
// (Gap 10): a published tier catalog (STARTER/GROWTH/SCALE/ENTERPRISE),
// effective-dated plan assignment per tenant, and deterministic quotes
// whose line items feed the billing invoice model (billing.Line) without
// forking it. Integer kobo throughout; AUM rates in integer basis points
// (DA-03 — no floats).
//
// The catalog is code-seeded (sane defaults benchmarked against Nigerian
// PFA admin-fee norms — see docs/PRICING-PARTNERS.md) and overridable per
// deployment via UpsertTier; overrides and assignments write through to
// the persist.Sink so a restart rehydrates them (NFR-10).
package pricing

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/billing"
	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/persist"
)

var (
	ErrUnknownTier  = errors.New("pricing: unknown tier code")
	ErrBadTier      = errors.New("pricing: tier code and positive rates required")
	ErrBadEffective = errors.New("pricing: effective_from must be YYYY-MM-DD")
	ErrBadPeriod    = errors.New("pricing: period must be YYYY-MM")
	ErrBadQuote     = errors.New("pricing: active_members and aum_kobo must be non-negative")
)

// Snapshot kinds owned by this engine.
const (
	KindTier       = "pricing.tier"       // catalog override (code-seeded defaults need no snapshot)
	KindAssignment = "pricing.assignment" // effective-dated tenant plan assignment
)

// Tier is one published pricing tier. Charges combine three integer
// components: a per-active-member monthly rate, an annual AUM platform
// fee in basis points (charged monthly at bps/10000/12, floored), and an
// overage rate applied to each active member beyond IncludedMembers (the
// cap nudges growing tenants toward the next tier).
type Tier struct {
	Code             string     `json:"code"` // STARTER | GROWTH | SCALE | ENTERPRISE | custom
	Name             string     `json:"name"`
	PerMemberMonthly money.Kobo `json:"per_member_monthly"` // per active member per month
	AUMBps           int64      `json:"aum_bps"`            // annual platform fee on AUM, basis points
	IncludedMembers  int        `json:"included_members"`   // members covered before overage applies
	OveragePerMember money.Kobo `json:"overage_per_member"` // per member per month beyond the cap
}

// Assignment binds a tenant to a tier from an effective date. History is
// kept so quotes for past periods stay reproducible (audited fields SetBy
// / SetAt).
type Assignment struct {
	ID            string    `json:"id"`
	TenantID      string    `json:"tenant_id"`
	TierCode      string    `json:"tier_code"`
	EffectiveFrom string    `json:"effective_from"` // YYYY-MM-DD
	SetBy         string    `json:"set_by"`
	SetAt         time.Time `json:"set_at"`
}

// Quote is a deterministic price quotation for one tenant and period. Its
// Lines are billing.Line values, ready to feed a billing.Invoice.
type Quote struct {
	TenantID      string         `json:"tenant_id"`
	TierCode      string         `json:"tier_code"`
	Period        string         `json:"period"` // YYYY-MM
	ActiveMembers int            `json:"active_members"`
	AUMKobo       money.Kobo     `json:"aum_kobo"`
	MemberCharge  money.Kobo     `json:"member_charge"`
	OverageCharge money.Kobo     `json:"overage_charge"`
	AUMCharge     money.Kobo     `json:"aum_charge"`
	Total         money.Kobo     `json:"total"`
	Lines         []billing.Line `json:"lines"`
	Assumed       bool           `json:"assumed"` // true when no plan is assigned and the default tier was used
}

// DefaultTier is used for quotes when a tenant has no plan assignment.
const DefaultTier = "STARTER"

// Engine holds the tier catalog and per-tenant plan assignments.
type Engine struct {
	mu          sync.Mutex
	tiers       map[string]Tier
	assignments map[string][]Assignment // tenantID -> history, any order
	seq         int
	sink        persist.Sink // nil in pure in-memory mode
}

// NewEngine returns an engine with the seeded standard catalog.
func NewEngine() *Engine {
	e := &Engine{tiers: map[string]Tier{}, assignments: map[string][]Assignment{}}
	for _, t := range standardCatalog() {
		e.tiers[t.Code] = t
	}
	return e
}

// standardCatalog is the published default pricing, benchmarked against
// Nigerian PFA admin-fee norms (see docs/PRICING-PARTNERS.md). Per-member
// rates are in kobo/month; AUM fees in annual basis points.
func standardCatalog() []Tier {
	return []Tier{
		{Code: "STARTER", Name: "Starter — emerging PFA", PerMemberMonthly: 5000, AUMBps: 3, IncludedMembers: 500, OveragePerMember: 6000},
		{Code: "GROWTH", Name: "Growth — mid-size PFA", PerMemberMonthly: 4500, AUMBps: 2, IncludedMembers: 5000, OveragePerMember: 5000},
		{Code: "SCALE", Name: "Scale — established PFA", PerMemberMonthly: 4000, AUMBps: 1, IncludedMembers: 25000, OveragePerMember: 4500},
		{Code: "ENTERPRISE", Name: "Enterprise — top-tier PFA", PerMemberMonthly: 3500, AUMBps: 1, IncludedMembers: 100000, OveragePerMember: 4000},
	}
}

// AttachSink wires write-through snapshot persistence (NFR-10).
func (e *Engine) AttachSink(s persist.Sink) { e.sink = s }

// Restore rehydrates catalog overrides and plan assignments from boot
// snapshots.
func (e *Engine) Restore(snaps []persist.Snapshot) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range snaps {
		switch s.Kind {
		case KindTier:
			var t Tier
			if err := json.Unmarshal(s.Payload, &t); err != nil {
				return err
			}
			if t.Code != "" {
				e.tiers[strings.ToUpper(t.Code)] = t
			}
		case KindAssignment:
			var a Assignment
			if err := json.Unmarshal(s.Payload, &a); err != nil {
				return err
			}
			e.assignments[a.TenantID] = append(e.assignments[a.TenantID], a)
			if n, err := strconv.Atoi(strings.TrimPrefix(a.ID, "pas-")); err == nil && n > e.seq {
				e.seq = n
			}
		}
	}
	return nil
}

// Catalog lists the tier catalog, sorted by per-member rate descending
// (cheapest tier last — the canonical published order STARTER…ENTERPRISE).
func (e *Engine) Catalog() []Tier {
	e.mu.Lock()
	out := make([]Tier, 0, len(e.tiers))
	for _, t := range e.tiers {
		out = append(out, t)
	}
	e.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].PerMemberMonthly != out[j].PerMemberMonthly {
			return out[i].PerMemberMonthly > out[j].PerMemberMonthly
		}
		return out[i].Code < out[j].Code
	})
	return out
}

// Tier returns one catalog tier by code (case-insensitive).
func (e *Engine) TierOf(code string) (Tier, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tiers[strings.ToUpper(strings.TrimSpace(code))]
	return t, ok
}

// UpsertTier adds or overrides a catalog tier (write-through persisted).
func (e *Engine) UpsertTier(t Tier) error {
	t.Code = strings.ToUpper(strings.TrimSpace(t.Code))
	if t.Code == "" || t.PerMemberMonthly < 0 || t.AUMBps < 0 || t.OveragePerMember < 0 || t.IncludedMembers < 0 {
		return ErrBadTier
	}
	e.mu.Lock()
	e.tiers[t.Code] = t
	e.mu.Unlock()
	persist.Save(e.sink, KindTier, "_global", t.Code, t)
	return nil
}

// SetPlan assigns a tier to a tenant effective from a YYYY-MM-DD date.
// Assignments are audited (setBy/setAt) and history-preserving: re-dating
// an existing (tier, effectiveFrom) pair is rejected to keep the audit
// trail append-only.
func (e *Engine) SetPlan(tenantID, tierCode, effectiveFrom, setBy string, at time.Time) (Assignment, error) {
	tierCode = strings.ToUpper(strings.TrimSpace(tierCode))
	if _, err := time.Parse("2006-01-02", effectiveFrom); err != nil {
		return Assignment{}, ErrBadEffective
	}
	e.mu.Lock()
	if _, ok := e.tiers[tierCode]; !ok {
		e.mu.Unlock()
		return Assignment{}, ErrUnknownTier
	}
	for _, a := range e.assignments[tenantID] {
		if a.TierCode == tierCode && a.EffectiveFrom == effectiveFrom {
			e.mu.Unlock()
			return a, nil // idempotent retry
		}
	}
	e.seq++
	a := Assignment{ID: fmt.Sprintf("pas-%06d", e.seq), TenantID: tenantID,
		TierCode: tierCode, EffectiveFrom: effectiveFrom, SetBy: setBy, SetAt: at}
	e.assignments[tenantID] = append(e.assignments[tenantID], a)
	e.mu.Unlock()
	persist.Save(e.sink, KindAssignment, tenantID, a.ID, a)
	return a, nil
}

// GetPlan returns the assignment in force for a tenant on a YYYY-MM-DD
// date: the latest assignment whose EffectiveFrom is on or before the
// date. Reports false when no assignment is effective yet.
func (e *Engine) GetPlan(tenantID, onDate string) (Assignment, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var best Assignment
	found := false
	for _, a := range e.assignments[tenantID] {
		if a.EffectiveFrom <= onDate && (!found || a.EffectiveFrom > best.EffectiveFrom ||
			(a.EffectiveFrom == best.EffectiveFrom && a.ID > best.ID)) {
			best, found = a, true
		}
	}
	return best, found
}

// Assignments returns the full assignment history for a tenant, ordered
// by effective date then ID.
func (e *Engine) Assignments(tenantID string) []Assignment {
	e.mu.Lock()
	out := append([]Assignment(nil), e.assignments[tenantID]...)
	e.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].EffectiveFrom != out[j].EffectiveFrom {
			return out[i].EffectiveFrom < out[j].EffectiveFrom
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Quote prices a tenant's usage for a period (YYYY-MM): per-member
// charges on all active members, an overage charge per member beyond the
// tier's included cap, and the monthly share of the annual AUM platform
// fee (aumKobo * bps / 10000 / 12, integer floor). Deterministic and
// kobo-exact. When the tenant has no plan effective for the period, the
// DefaultTier is used and the quote is flagged Assumed.
func (e *Engine) Quote(tenantID string, activeMembers int, aumKobo money.Kobo, period string) (Quote, error) {
	if _, err := time.Parse("2006-01", period); err != nil {
		return Quote{}, ErrBadPeriod
	}
	if activeMembers < 0 || aumKobo < 0 {
		return Quote{}, ErrBadQuote
	}
	assumed := false
	tierCode := DefaultTier
	if a, ok := e.GetPlan(tenantID, period+"-01"); ok {
		tierCode = a.TierCode
	} else {
		assumed = true
	}
	tier, ok := e.TierOf(tierCode)
	if !ok {
		return Quote{}, fmt.Errorf("%w: %s", ErrUnknownTier, tierCode)
	}
	memberCharge := tier.PerMemberMonthly * money.Kobo(activeMembers)
	overage := activeMembers - tier.IncludedMembers
	if overage < 0 {
		overage = 0
	}
	overageCharge := tier.OveragePerMember * money.Kobo(overage)
	// Monthly AUM fee: divide by 12 first to stay in integer range, then
	// floor the bps share. aumKobo is at most ~1e17 in practice; bps ≤
	// 10000, so (aum/12)*bps cannot overflow int64.
	aumCharge := aumKobo / 12 * money.Kobo(tier.AUMBps) / 10000
	q := Quote{
		TenantID:      tenantID,
		TierCode:      tier.Code,
		Period:        period,
		ActiveMembers: activeMembers,
		AUMKobo:       aumKobo,
		MemberCharge:  memberCharge,
		OverageCharge: overageCharge,
		AUMCharge:     aumCharge,
		Total:         memberCharge + overageCharge + aumCharge,
		Assumed:       assumed,
		Lines: []billing.Line{
			{Description: fmt.Sprintf("Active member charges (%s, %d members)", period, activeMembers), Amount: memberCharge},
			{Description: fmt.Sprintf("Overage charges (%d members above %d included)", overage, tier.IncludedMembers), Amount: overageCharge},
			{Description: fmt.Sprintf("AUM platform fee (%d bps p.a., monthly)", tier.AUMBps), Amount: aumCharge},
		},
	}
	return q, nil
}
