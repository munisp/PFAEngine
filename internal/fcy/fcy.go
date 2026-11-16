// Package fcy implements FR-508 / BR-22: the foreign-currency (Fund VII)
// USD RSA. Amounts are integer US cents (DA-03 analog — never floats) via
// the local Cents type. Contributions split 60% retirement / 40% contingent
// (integer split, remainder to retirement). Withdrawals come from the
// contingent balance only, are capped at 2 per rolling year, and the first
// withdrawal requires 6 months of seasoning. Contributions strictly above
// $10,000 are reportable to the NFIU.
package fcy

import (
	"errors"
	"time"
)

// Cents is an integer USD minor-unit amount (1 USD = 100 cents).
type Cents int64

var (
	ErrInsufficient  = errors.New("fcy: amount exceeds contingent balance")
	ErrSeasoning     = errors.New("fcy: first withdrawal only 6 months after account opening")
	ErrWithdrawalCap = errors.New("fcy: maximum 2 withdrawals per rolling year")
	ErrNonPositive   = errors.New("fcy: amount must be positive")
)

// SeasoningMonths is the wait before the first withdrawal.
const SeasoningMonths = 6

// MaxWithdrawalsPerYear caps contingent withdrawals per rolling year.
const MaxWithdrawalsPerYear = 2

// NFIUReportCents is the NFIU reporting threshold ($10,000; strictly
// greater amounts are reportable).
const NFIUReportCents = Cents(1_000_000)

// Account is a Fund VII USD balance pair with withdrawal tracking.
type Account struct {
	Retirement          Cents     `json:"retirement_cents"`
	Contingent          Cents     `json:"contingent_cents"`
	OpenedAt            time.Time `json:"opened_at"`
	WithdrawalsThisYear int       `json:"withdrawals_this_year"`
	YearStart           time.Time `json:"year_start"`
	TotalWithdrawals    int       `json:"total_withdrawals"`
}

// NewAccount opens a Fund VII account.
func NewAccount(openedAt time.Time) *Account {
	return &Account{OpenedAt: openedAt, YearStart: openedAt}
}

// Contribute splits amount 60% retirement / 40% contingent; the integer
// remainder goes to retirement.
func (a *Account) Contribute(amount Cents, at time.Time) {
	r := Cents(int64(amount) * 60 / 100)
	a.Retirement += r
	a.Contingent += amount - r
}

// Withdraw pays amount out of the contingent balance, enforcing the
// 6-month first-withdrawal seasoning and the 2-per-rolling-year cap.
func (a *Account) Withdraw(amount Cents, at time.Time) error {
	if amount <= 0 {
		return ErrNonPositive
	}
	if amount > a.Contingent {
		return ErrInsufficient
	}
	if a.TotalWithdrawals == 0 && at.Before(a.OpenedAt.AddDate(0, SeasoningMonths, 0)) {
		return ErrSeasoning
	}
	// Rolling-year window: reset once a full year has elapsed.
	if !at.Before(a.YearStart.AddDate(1, 0, 0)) {
		a.YearStart = at
		a.WithdrawalsThisYear = 0
	}
	if a.WithdrawalsThisYear >= MaxWithdrawalsPerYear {
		return ErrWithdrawalCap
	}
	a.Contingent -= amount
	a.WithdrawalsThisYear++
	a.TotalWithdrawals++
	return nil
}

// NFIUReportable reports whether a contribution exceeds $10,000 and must
// be reported to the NFIU within 24 hours.
func NFIUReportable(amount Cents) bool {
	return amount > NFIUReportCents
}
