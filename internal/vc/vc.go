// Package vc implements FR-505 / BR-18: Voluntary Contributions under the
// revised (June 2024) VC Guidelines. Each lodgment splits 50% contingent /
// 50% fixed (integer split, remainder to fixed). The contingent portion is
// accessible only from 1 year after the VC account was opened. Income
// earned on VC is taxable if withdrawn before 5 years. Single lodgments of
// ₦5,000,000 or more are reportable to the EFCC. Integer kobo throughout.
package vc

import (
	"errors"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var (
	ErrInsufficient = errors.New("vc: amount exceeds contingent balance")
	ErrAccessLock   = errors.New("vc: contingent access only 1 year after account opening")
	ErrNonPositive  = errors.New("vc: amount must be positive")
)

// AccessLockYears is the minimum holding period before contingent access.
const AccessLockYears = 1

// TaxFreeHoldYears: income withdrawn before this many years is taxable.
const TaxFreeHoldYears = 5

// EFCCReportKobo is the single-lodgment EFCC reporting threshold (₦5m).
const EFCCReportKobo = money.Kobo(500_000_000)

// Lodgment is one contribution into the VC account.
type Lodgment struct {
	Amount money.Kobo `json:"amount_kobo"`
	At     time.Time  `json:"at"`
}

// Account is a voluntary-contribution balance pair with an audit ledger.
type Account struct {
	Contingent money.Kobo `json:"contingent_kobo"`
	Fixed      money.Kobo `json:"fixed_kobo"`
	OpenedAt   time.Time  `json:"opened_at"`
	Ledger     []Lodgment `json:"ledger,omitempty"`
}

// NewAccount opens a VC account.
func NewAccount(openedAt time.Time) *Account { return &Account{OpenedAt: openedAt} }

// Contribute splits amount 50% contingent / 50% fixed; the integer
// remainder goes to fixed. The lodgment is appended to the ledger.
func (a *Account) Contribute(amount money.Kobo, at time.Time) {
	c := money.Kobo(int64(amount) * 50 / 100) // truncated; remainder to fixed
	a.Contingent += c
	a.Fixed += amount - c
	a.Ledger = append(a.Ledger, Lodgment{Amount: amount, At: at})
}

// WithdrawContingent pays amount out of the contingent balance only, and
// only from AccessLockYears after the account was opened.
func (a *Account) WithdrawContingent(amount money.Kobo, at time.Time) error {
	if amount <= 0 {
		return ErrNonPositive
	}
	if amount > a.Contingent {
		return ErrInsufficient
	}
	if at.Before(a.OpenedAt.AddDate(AccessLockYears, 0, 0)) {
		return ErrAccessLock
	}
	a.Contingent -= amount
	return nil
}

// TaxableIncome returns the taxable portion of VC income: the full income
// if withdrawn before TaxFreeHoldYears, zero otherwise.
func TaxableIncome(income money.Kobo, heldYears int) money.Kobo {
	if heldYears < TaxFreeHoldYears {
		return income
	}
	return 0
}

// EFCCReportable reports whether a single lodgment meets the ₦5,000,000
// EFCC reporting threshold.
func EFCCReportable(amount money.Kobo) bool {
	return amount >= EFCCReportKobo
}
