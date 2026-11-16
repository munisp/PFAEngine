// Package returns implements FR-511: regulatory return computations and
// reports — the daily valuation report (NAV, unit price), rolling
// annualized returns, the Sharpe ratio (risk-free = 3-yr avg 10-yr FGN
// yield), time-weighted returns for AES/non-unitised schemes (BR-23),
// and monthly/quarterly RMAS operational returns.
// All money is integer kobo; returns are basis points — never floats (DA-03).
package returns

import (
	"errors"
	"fmt"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var ErrNegativeValue = errors.New("returns: valuation figures must be non-negative")

// ValuationReport is the daily fund valuation return (FR-511).
type ValuationReport struct {
	TenantID   string     `json:"tenant_id"`
	FundCode   string     `json:"fund_code"`
	Date       time.Time  `json:"date"`
	NAV        money.Kobo `json:"nav_kobo"`
	UnitPrice  money.Kobo `json:"unit_price_kobo"` // kobo per whole unit
	UnitsTotal money.Unit `json:"units_total"`
}

// DailyValuation builds a ValuationReport, rejecting negative figures.
func DailyValuation(tenantID, fundCode string, date time.Time, nav, unitPrice money.Kobo, units money.Unit) (ValuationReport, error) {
	if nav < 0 || unitPrice < 0 || units < 0 {
		return ValuationReport{}, ErrNegativeValue
	}
	return ValuationReport{
		TenantID: tenantID, FundCode: fundCode, Date: date,
		NAV: nav, UnitPrice: unitPrice, UnitsTotal: units,
	}, nil
}

// RollingReturn computes the annualized return of a daily unit-price
// series in basis points. Approximation: the holding-period return
// (last−first)/first is scaled linearly by 365/len(prices) — simple
// annualization without compounding, matching the regulator's published
// 36-month rolling convention to integer precision.
func RollingReturn(prices []money.Kobo) int64 {
	if len(prices) < 2 {
		return 0
	}
	first, last := prices[0], prices[len(prices)-1]
	if first <= 0 {
		return 0
	}
	periodBps := int64(last-first) * 10000 / int64(first)
	return periodBps * 365 / int64(len(prices))
}

// Sharpe computes (mean excess return / stddev of excess returns) scaled
// ×100, i.e. a Sharpe of 1.25 is returned as 125. Integer math throughout:
// the standard deviation uses integer square root (truncating), so the
// result is an approximation good to ~1/100 of a Sharpe point.
func Sharpe(returnsBps []int64, riskFreeBps int64) int64 {
	n := int64(len(returnsBps))
	if n == 0 {
		return 0
	}
	var sum int64
	for _, r := range returnsBps {
		sum += r - riskFreeBps
	}
	mean := sum / n
	var sq int64
	for _, r := range returnsBps {
		d := r - riskFreeBps - mean
		sq += d * d
	}
	sd := isqrt(sq / n)
	if sd == 0 {
		return 0
	}
	return mean * 100 / sd
}

// isqrt returns floor(sqrt(v)) for v >= 0 via binary search.
func isqrt(v int64) int64 {
	if v <= 0 {
		return 0
	}
	lo, hi := int64(1), v
	if hi > 1<<32 {
		hi = 1 << 32
	}
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if mid <= v/mid {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// TWR compounds sub-period returns (basis points) into a time-weighted
// return in basis points. Fixed-point accumulation at scale 1e4:
// acc ← acc × (10000 + r) / 10000 each period, truncating per step — an
// integer approximation that avoids math/big and floats (DA-03).
func TWR(subPeriodReturnsBps []int64) int64 {
	if len(subPeriodReturnsBps) == 0 {
		return 0
	}
	acc := int64(10000)
	for _, r := range subPeriodReturnsBps {
		acc = acc * (10000 + r) / 10000
	}
	return acc - 10000
}

// RMASReturn is a monthly or quarterly operational return to PenCom via
// the Risk Management & Analysis System (FR-511).
type RMASReturn struct {
	Period        string     `json:"period"` // "2026-11" (monthly) or "2026-Q4" (quarterly)
	EmployerCount int        `json:"employer_count"`
	MemberCount   int        `json:"member_count"`
	Contributions money.Kobo `json:"contributions_kobo"`
	Penalties     money.Kobo `json:"penalties_kobo"`
	GeneratedAt   time.Time  `json:"generated_at"`
}

// MonthlyRMAS builds a monthly RMAS operational return.
func MonthlyRMAS(period string, employers, members int, contributions, penalties money.Kobo, at time.Time) (RMASReturn, error) {
	if contributions < 0 || penalties < 0 || employers < 0 || members < 0 {
		return RMASReturn{}, ErrNegativeValue
	}
	return RMASReturn{Period: period, EmployerCount: employers, MemberCount: members,
		Contributions: contributions, Penalties: penalties, GeneratedAt: at}, nil
}

// QuarterlyRMAS builds a quarterly RMAS return; period is "YYYY-Qn".
func QuarterlyRMAS(year, quarter, employers, members int, contributions, penalties money.Kobo, at time.Time) (RMASReturn, error) {
	if quarter < 1 || quarter > 4 {
		return RMASReturn{}, errors.New("returns: quarter must be 1..4")
	}
	period := fmt.Sprintf("%04d-Q%d", year, quarter)
	return MonthlyRMAS(period, employers, members, contributions, penalties, at)
}
