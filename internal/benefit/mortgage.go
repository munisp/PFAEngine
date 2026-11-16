package benefit

// Mortgage equity contribution (BR-21, PRA 2014 s.89(2) + guidelines):
// up to 25% of the RSA balance may be applied as equity contribution
// towards a residential mortgage through a CBN-licensed mortgage lender —
// a statutory exception to the s.89 prohibition on using RSA assets as
// security. Integer kobo throughout (DA-03).

import (
	"errors"

	"github.com/munisp/pfaengine/internal/money"
)

var (
	ErrMortgageEquityLimit = errors.New("benefit: mortgage equity exceeds 25% of RSA balance")
	ErrMortgageLender      = errors.New("benefit: mortgage lender must be CBN-licensed")
	ErrMortgageAmount      = errors.New("benefit: mortgage equity amount must be positive")
)

// MortgageEquityLimit returns the maximum mortgage equity contribution:
// 25% of the RSA balance (BR-21).
func MortgageEquityLimit(balance money.Kobo) money.Kobo {
	return money.MulKobo(balance, 25, 100)
}

// ValidateMortgageEquity enforces BR-21: the lender must be CBN-licensed
// and the amount must be positive and within 25% of the RSA balance.
func ValidateMortgageEquity(amount, balance money.Kobo, lenderCBNLicensed bool) error {
	if !lenderCBNLicensed {
		return ErrMortgageLender
	}
	if amount <= 0 {
		return ErrMortgageAmount
	}
	if amount > MortgageEquityLimit(balance) {
		return ErrMortgageEquityLimit
	}
	return nil
}
