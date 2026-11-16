// Package micropension implements FR-504 / BR-17: the Micro Pension Plan /
// Personal Pension Plan account. Each contribution splits 40% contingent /
// 60% retirement (integer split, remainder to retirement). The contingent
// portion is first accessible 3 months after the first contribution and
// thereafter at most once every 7 days. All amounts are integer kobo.
package micropension

import (
	"errors"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var (
	ErrInsufficient      = errors.New("micropension: amount exceeds contingent balance")
	ErrSeasoning         = errors.New("micropension: contingent access only 3 months after first contribution")
	ErrWithdrawalCadence = errors.New("micropension: contingent withdrawals at most once every 7 days")
	ErrNonPositive       = errors.New("micropension: amount must be positive")
)

// SeasoningMonths is the wait before the first contingent withdrawal.
const SeasoningMonths = 3

// WithdrawalCadence is the minimum gap between contingent withdrawals.
const WithdrawalCadence = 7 * 24 * time.Hour

// Account is a micro-pension balance pair with seasoning timestamps.
type Account struct {
	Contingent               money.Kobo `json:"contingent_kobo"`
	Retirement               money.Kobo `json:"retirement_kobo"`
	FirstContribution        time.Time  `json:"first_contribution"`
	LastContingentWithdrawal time.Time  `json:"last_contingent_withdrawal,omitempty"`
}

// Contribute splits amount 40% contingent / 60% retirement; the integer
// remainder goes to retirement.
func (a *Account) Contribute(amount money.Kobo, at time.Time) {
	c := money.Kobo(int64(amount) * 40 / 100) // truncated; remainder to retirement
	a.Contingent += c
	a.Retirement += amount - c
	if a.FirstContribution.IsZero() {
		a.FirstContribution = at
	}
}

// WithdrawContingent pays amount out of the contingent balance. The first
// withdrawal requires SeasoningMonths since the first contribution;
// subsequent withdrawals require WithdrawalCadence since the last one.
func (a *Account) WithdrawContingent(amount money.Kobo, at time.Time) error {
	if amount <= 0 {
		return ErrNonPositive
	}
	if amount > a.Contingent {
		return ErrInsufficient
	}
	if a.LastContingentWithdrawal.IsZero() {
		if a.FirstContribution.IsZero() || at.Before(a.FirstContribution.AddDate(0, SeasoningMonths, 0)) {
			return ErrSeasoning
		}
	} else if at.Before(a.LastContingentWithdrawal.Add(WithdrawalCadence)) {
		return ErrWithdrawalCadence
	}
	a.Contingent -= amount
	a.LastContingentWithdrawal = at
	return nil
}
