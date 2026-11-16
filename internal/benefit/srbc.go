package benefit

// SRBC — PenCom Standard Retirement Benefit Computation (FR-506 / FR-507 /
// BR-19, PRA 2014 s.7): lump-sum bounds, residual-pension test, en-bloc
// threshold, and job-loss 25% access eligibility. Integer kobo throughout.

import (
	"errors"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var (
	ErrLumpSumBounds   = errors.New("benefit: lump sum outside 25-50% of RSA balance (SRBC)")
	ErrResidualPension = errors.New("benefit: residual monthly pension below 50% of final salary")
)

// LumpSumBounds returns the permitted lump-sum band: 25%–50% of the RSA
// balance at retirement (BR-19).
func LumpSumBounds(balance money.Kobo) (min, max money.Kobo) {
	return money.MulKobo(balance, 25, 100), money.MulKobo(balance, 50, 100)
}

// ValidateLumpSum enforces the SRBC checks: the lump sum must lie within
// 25–50% of the balance AND the resulting monthly pension must be at least
// 50% of final monthly salary.
func ValidateLumpSum(lump, balance, finalSalaryMonthly, monthlyPension money.Kobo) error {
	min, max := LumpSumBounds(balance)
	if lump < min || lump > max {
		return ErrLumpSumBounds
	}
	if monthlyPension < money.MulKobo(finalSalaryMonthly, 50, 100) {
		return ErrResidualPension
	}
	return nil
}

// EnBloc reports whether the RSA may be paid en bloc: the computed monthly
// pension is less than one third of the national minimum monthly wage
// (₦70,000 → threshold ₦23,333.33). Compared in integer kobo:
// pension < wage/3  ⟺  3 × pension < wage.
func EnBloc(monthlyPension, minimumWageMonthly money.Kobo) bool {
	return int64(monthlyPension)*3 < int64(minimumWageMonthly)
}

// JobLossWaitMonths is the continuous-unemployment wait before the 25%
// job-loss withdrawal (PRA s.7(2)).
const JobLossWaitMonths = 4

// JobLossEligible reports whether a member qualifies for the one-time 25%
// job-loss withdrawal: under 50, disengaged and unemployed for at least
// JobLossWaitMonths calendar months, and not previously taken.
func JobLossEligible(ageYears int, disengagedAt, asOf time.Time, alreadyTaken bool) bool {
	if alreadyTaken || ageYears >= 50 {
		return false
	}
	return !asOf.Before(disengagedAt.AddDate(0, JobLossWaitMonths, 0))
}
