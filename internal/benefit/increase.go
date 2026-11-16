package benefit

// SCM-09: pension increases. Increases are expressed in basis points and
// applied with integer kobo arithmetic only (DA-03). Funds may only grant
// increases when policy conditions hold (e.g. sustained actuarial surplus).

import (
	"errors"

	"github.com/munisp/pfaengine/internal/money"
)

var (
	// ErrInsufficientSurplus is returned when the fund's policy requires a
	// sustained surplus but the surplus run is shorter than two years.
	ErrInsufficientSurplus = errors.New("benefit: increase requires >= 2 consecutive surplus years")
	// ErrNegativeIncrease rejects negative (pension-cutting) proposals.
	ErrNegativeIncrease = errors.New("benefit: increase bps must be >= 0")
)

// SustainedSurplusYears is the minimum consecutive-surplus run a flagged
// fund needs before granting increases (SCM-09).
const SustainedSurplusYears = 2

// ApplyIncrease returns the new monthly pension after an increase of
// increaseBps basis points (e.g. 500 bps = 5%). Integer math only.
func ApplyIncrease(monthlyPension money.Kobo, increaseBps int64) money.Kobo {
	return monthlyPension + money.MulKobo(monthlyPension, increaseBps, 10000)
}

// IncreasePolicy gates pension increases for a fund (SCM-09).
type IncreasePolicy struct {
	FundCode                 string
	RequiresSustainedSurplus bool
	MaxBps                   int64 // annual increase cap
}

// EligibleIncrease validates a proposed increase against the policy and
// returns the granted rate: negative proposals are rejected, proposals
// above MaxBps are capped, and a floor of 0 bps applies. When the policy
// requires a sustained surplus, fewer than two consecutive surplus years
// is an error.
func EligibleIncrease(p IncreasePolicy, surplusConsecutiveYears int, proposedBps int64) (int64, error) {
	if proposedBps < 0 {
		return 0, ErrNegativeIncrease
	}
	if p.RequiresSustainedSurplus && surplusConsecutiveYears < SustainedSurplusYears {
		return 0, ErrInsufficientSurplus
	}
	granted := proposedBps
	if p.MaxBps > 0 && granted > p.MaxBps {
		granted = p.MaxBps
	}
	if granted < 0 {
		granted = 0
	}
	return granted, nil
}
