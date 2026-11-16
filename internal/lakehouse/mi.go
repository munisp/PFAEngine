package lakehouse

import "github.com/munisp/pfaengine/internal/money"

// MI aggregations (BR-09). All functions are pure over []Event and
// tenant-agnostic: callers filter by TenantID first (see FilterTenant)
// to build a per-tenant dashboard.

// FilterTenant returns the events belonging to one tenant (multi-tenant
// isolation for dashboards).
func FilterTenant(ev []Event, tenantID string) []Event {
	out := make([]Event, 0, len(ev))
	for _, e := range ev {
		if e.TenantID == tenantID {
			out = append(out, e)
		}
	}
	return out
}

// ContributionsByMonth sums CONTRIBUTION_POSTED amounts per calendar
// month ("2006-01").
func ContributionsByMonth(ev []Event) map[string]money.Kobo {
	out := map[string]money.Kobo{}
	for _, e := range ev {
		if e.Kind != KindContribution {
			continue
		}
		out[e.At.Format("2006-01")] += e.AmountKobo
	}
	return out
}

// EmployerDelinquency sums PENALTY_APPLIED amounts per employer
// (Meta["employer_id"]); events without an employer_id are skipped.
func EmployerDelinquency(ev []Event) map[string]money.Kobo {
	out := map[string]money.Kobo{}
	for _, e := range ev {
		if e.Kind != KindPenaltyApplied {
			continue
		}
		emp := e.Meta["employer_id"]
		if emp == "" {
			continue
		}
		out[emp] += e.AmountKobo
	}
	return out
}

// CohortRetention counts MEMBER_ACTIVE events per join-year cohort
// (Meta["join_year"], falling back to the event year when absent).
func CohortRetention(ev []Event) map[string]int {
	out := map[string]int{}
	for _, e := range ev {
		if e.Kind != KindMemberActive {
			continue
		}
		cohort := e.Meta["join_year"]
		if cohort == "" {
			cohort = e.At.Format("2006")
		}
		out[cohort]++
	}
	return out
}

// Dashboard is the headline MI view for one tenant.
type Dashboard struct {
	AUM                  money.Kobo            `json:"aum_kobo"`
	MonthlyContributions money.Kobo            `json:"monthly_contributions_kobo"` // latest month with activity
	PenaltiesOutstanding money.Kobo            `json:"penalties_outstanding_kobo"`
	ActiveMembers        int                   `json:"active_members"`
	FundMix              map[string]money.Kobo `json:"fund_mix_kobo"`
}

// BuildDashboard derives the MI dashboard from an event slice (already
// filtered to a single tenant). AUM = contributions − benefits − fees;
// MonthlyContributions = the latest month's contribution total;
// PenaltiesOutstanding = all PENALTY_APPLIED amounts; ActiveMembers =
// distinct members (Meta["member_id"]) with MEMBER_ACTIVE events;
// FundMix = UNIT_ALLOCATION amounts per Meta["fund_code"].
func BuildDashboard(ev []Event) Dashboard {
	d := Dashboard{FundMix: map[string]money.Kobo{}}
	byMonth := ContributionsByMonth(ev)
	latest := ""
	for m, amt := range byMonth {
		if m > latest {
			latest, d.MonthlyContributions = m, amt
		}
	}
	members := map[string]bool{}
	for _, e := range ev {
		switch e.Kind {
		case KindContribution:
			d.AUM += e.AmountKobo
		case KindBenefitPaid, KindFeeCharged:
			d.AUM -= e.AmountKobo
		case KindPenaltyApplied:
			d.PenaltiesOutstanding += e.AmountKobo
		case KindMemberActive:
			if id := e.Meta["member_id"]; id != "" {
				members[id] = true
			}
		case KindUnitAllocation:
			if fc := e.Meta["fund_code"]; fc != "" {
				d.FundMix[fc] += e.AmountKobo
			}
		}
	}
	d.ActiveMembers = len(members)
	return d
}
