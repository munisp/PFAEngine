// Package billing implements BR-08: SaaS tenant billing for the
// PFA-as-a-Service platform. Plans charge a monthly base plus a
// per-active-member fee with a free allowance; payment status drives
// platform access. Integer kobo throughout — no floats (DA-03).
package billing

import (
	"fmt"

	"github.com/munisp/pfaengine/internal/money"
)

// Plan is a tenant pricing plan (BR-08).
type Plan struct {
	Code                string     `json:"code"`
	BaseMonthly         money.Kobo `json:"base_monthly"`
	PerActiveMember     money.Kobo `json:"per_active_member"`
	PerActiveMemberFree int        `json:"per_active_member_free"` // allowance included in base
}

// Line is one invoice line item.
type Line struct {
	Description string     `json:"description"`
	Amount      money.Kobo `json:"amount"`
}

// Invoice is the monthly charge for one tenant.
type Invoice struct {
	Period       string     `json:"period"` // YYYY-MM
	Base         money.Kobo `json:"base"`
	MemberCharge money.Kobo `json:"member_charge"`
	Total        money.Kobo `json:"total"`
}

// Invoice computes the monthly invoice for this plan: base fee plus
// per-member charge on active members beyond the free allowance.
func (p Plan) Invoice(activeMembers int, period string) Invoice {
	billable := activeMembers - p.PerActiveMemberFree
	if billable < 0 {
		billable = 0
	}
	memberCharge := p.PerActiveMember * money.Kobo(billable)
	return Invoice{
		Period:       period,
		Base:         p.BaseMonthly,
		MemberCharge: memberCharge,
		Total:        p.BaseMonthly + memberCharge,
	}
}

// Lines itemises the invoice for presentation.
func (inv Invoice) Lines() []Line {
	return []Line{
		{Description: fmt.Sprintf("Platform base fee (%s)", inv.Period), Amount: inv.Base},
		{Description: "Active member charges", Amount: inv.MemberCharge},
	}
}

// Status is a tenant's payment standing.
type Status int

const (
	Current Status = iota
	Grace
	Overdue
	Suspended
)

func (s Status) String() string {
	switch s {
	case Current:
		return "CURRENT"
	case Grace:
		return "GRACE"
	case Overdue:
		return "OVERDUE"
	case Suspended:
		return "SUSPENDED"
	}
	return "UNKNOWN"
}

// Next returns the payment status for an invoice daysOverdue days past due:
// 0–14 Current, 15–30 Grace, 31–60 Overdue, >60 Suspended (BR-08 dunning).
// Negative values are treated as not overdue.
func (s Status) Next(daysOverdue int) Status {
	switch {
	case daysOverdue <= 14:
		return Current
	case daysOverdue <= 30:
		return Grace
	case daysOverdue <= 60:
		return Overdue
	default:
		return Suspended
	}
}

// PlatformAccess reports whether the tenant's users may use the platform.
// Suspended tenants are fully cut off. Grace and Overdue retain access;
// operators SHOULD enforce read-only semantics for Overdue tenants at the
// gateway (writes blocked, statements/reports still readable) — this
// function only answers the coarse access question.
func PlatformAccess(s Status) bool {
	return s != Suspended
}
