package benefit

import (
	"errors"
	"testing"

	"github.com/munisp/pfaengine/internal/money"
)

func TestApplyIncrease(t *testing.T) {
	cases := []struct {
		name    string
		pension money.Kobo
		bps     int64
		want    money.Kobo
	}{
		{"five percent", money.NGN(80000), 500, money.NGN(84000)},
		{"zero", money.NGN(80000), 0, money.NGN(80000)},
		{"ten percent", money.NGN(33333), 1000, money.NGN(36666) + money.Kobo(30)}, // 3333300 + 333330
		{"rounding half away", money.NGN(1), 50, money.NGN(1) + money.Kobo(1)},     // 0.5 kobo rounds away from zero
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ApplyIncrease(tc.pension, tc.bps); got != tc.want {
				t.Fatalf("ApplyIncrease(%s, %d) = %s, want %s",
					tc.pension.Naira(), tc.bps, got.Naira(), tc.want.Naira())
			}
		})
	}
}

func TestEligibleIncrease(t *testing.T) {
	open := IncreasePolicy{FundCode: "FUND_II", MaxBps: 1000}
	guarded := IncreasePolicy{FundCode: "FUND_III", RequiresSustainedSurplus: true, MaxBps: 500}
	cases := []struct {
		name    string
		policy  IncreasePolicy
		surplus int
		propose int64
		want    int64
		wantErr error
	}{
		{"within cap", open, 0, 700, 700, nil},
		{"capped at max", open, 0, 1500, 1000, nil},
		{"zero proposal", open, 0, 0, 0, nil},
		{"negative rejected", open, 5, -100, 0, ErrNegativeIncrease},
		{"surplus required, 2 years ok", guarded, 2, 300, 300, nil},
		{"surplus required, 3 years ok", guarded, 3, 600, 500, nil},
		{"surplus required, 1 year fails", guarded, 1, 300, 0, ErrInsufficientSurplus},
		{"surplus required, 0 years fails", guarded, 0, 300, 0, ErrInsufficientSurplus},
		{"no cap configured", IncreasePolicy{FundCode: "FUND_IV"}, 0, 99999, 99999, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EligibleIncrease(tc.policy, tc.surplus, tc.propose)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("granted = %d, want %d", got, tc.want)
			}
		})
	}
}
