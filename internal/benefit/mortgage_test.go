package benefit

import (
	"errors"
	"testing"

	"github.com/munisp/pfaengine/internal/money"
)

func TestMortgageEquityLimit(t *testing.T) {
	if got := MortgageEquityLimit(money.NGN(10_000_000)); got != money.NGN(2_500_000) {
		t.Fatalf("limit %s", got.Naira())
	}
}

func TestValidateMortgageEquity(t *testing.T) {
	balance := money.NGN(10_000_000)
	tests := []struct {
		name     string
		amount   money.Kobo
		licensed bool
		want     error
	}{
		{"within limit", money.NGN(2_000_000), true, nil},
		{"exactly 25%", money.NGN(2_500_000), true, nil},
		{"above 25%", money.NGN(2_500_000) + 1, true, ErrMortgageEquityLimit},
		{"unlicensed lender", money.NGN(1_000_000), false, ErrMortgageLender},
		{"zero amount", 0, true, ErrMortgageAmount},
		{"negative amount", -1, true, ErrMortgageAmount},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateMortgageEquity(tt.amount, balance, tt.licensed); !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
}
