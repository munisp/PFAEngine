// Package advisory implements the retirement-adequacy advisor: projects a
// member's RSA balance at retirement, converts it to an expected monthly
// pension, compares it against a replacement-income target, and computes
// the additional monthly contribution needed to close the gap.
//
// All money is integer kobo; growth is compounded monthly with basis-point
// annual returns using integer arithmetic (no floats — DA-03).
package advisory

import (
	"errors"

	"github.com/munisp/pfaengine/internal/money"
)

var (
	ErrBadHorizon = errors.New("advisory: years to retirement must be positive")
	ErrBadRate    = errors.New("advisory: annual return must be in (0, 10000] bps")
)

// Projection is the adequacy result for one member.
type Projection struct {
	BalanceNow       money.Kobo `json:"balance_now_kobo"`
	MonthlyContrib   money.Kobo `json:"monthly_contrib_kobo"`
	YearsToRetire    int        `json:"years_to_retire"`
	AnnualReturnBps  int64      `json:"annual_return_bps"`
	ProjectedBalance money.Kobo `json:"projected_balance_kobo"`
	// ExpectedPension = projected balance spread over annuityMonths.
	ExpectedPension  money.Kobo `json:"expected_pension_kobo"`
	TargetPension    money.Kobo `json:"target_pension_kobo"` // replacementBps of salary
	Gap              money.Kobo `json:"gap_kobo"`            // max(0, target − expected)
	ExtraMonthly     money.Kobo `json:"extra_monthly_kobo"`  // added contribution closing the gap
	OnTrack          bool       `json:"on_track"`
}

// compound grows a balance with fixed monthly contributions over n months
// at annualBps/12 per month, all integer.
func compound(balance, monthly money.Kobo, months int, annualBps int64) money.Kobo {
	// monthly rate in bps of bps precision: r = annualBps/12; multiplier
	// per month = (120000 + annualBps*100/12)/120000 scaled — use exact
	// integer recurrence: b = b + b*r/1_200_000 + monthly, r in bps*100.
	r := annualBps * 100 // bps * 100 for /12/10000 precision
	b := int64(balance)
	for i := 0; i < months; i++ {
		b = b + b*r/12/1_000_000 + int64(monthly)
	}
	return money.Kobo(b)
}

// Project computes the adequacy projection. annuityMonths is the drawdown
// horizon (e.g. 240 for a 20-year programmed withdrawal).
func Project(balance, monthly money.Kobo, years int, annualBps int64,
	salary money.Kobo, replacementBps int64, annuityMonths int) (Projection, error) {
	if years <= 0 {
		return Projection{}, ErrBadHorizon
	}
	if annualBps <= 0 || annualBps > 10000 {
		return Projection{}, ErrBadRate
	}
	months := years * 12
	proj := compound(balance, monthly, months, annualBps)
	expected := proj
	if annuityMonths > 0 {
		expected = proj / money.Kobo(annuityMonths)
	}
	target := salary * money.Kobo(replacementBps) / 10000
	p := Projection{
		BalanceNow: balance, MonthlyContrib: monthly, YearsToRetire: years,
		AnnualReturnBps: annualBps, ProjectedBalance: proj,
		ExpectedPension: expected, TargetPension: target,
		OnTrack: expected >= target,
	}
	if expected < target {
		p.Gap = target - expected
		// Binary search the extra monthly contribution that closes the gap.
		lo, hi := money.Kobo(0), target*money.Kobo(annuityMonths)/money.Kobo(months)+1
		for lo < hi {
			mid := (lo + hi) / 2
			if compound(balance, monthly+mid, months, annualBps)/money.Kobo(annuityMonths) >= target {
				hi = mid
			} else {
				lo = mid + 1
			}
		}
		p.ExtraMonthly = lo
	}
	return p, nil
}
