package benefit

import (
	"errors"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

func TestLumpSumBounds(t *testing.T) {
	min, max := LumpSumBounds(money.NGN(20000000))
	if min != money.NGN(5000000) || max != money.NGN(10000000) {
		t.Fatalf("bounds %s..%s", min.Naira(), max.Naira())
	}
}

func TestValidateLumpSum(t *testing.T) {
	balance := money.NGN(20000000)
	finalSalary := money.NGN(400000) // 50% => ₦200,000/month floor
	pensionOK := money.NGN(250000)
	cases := []struct {
		name    string
		lump    money.Kobo
		pension money.Kobo
		wantErr error
	}{
		{"at 25% lower bound", money.NGN(5000000), pensionOK, nil},
		{"at 50% upper bound", money.NGN(10000000), pensionOK, nil},
		{"below 25%", money.NGN(4999999), pensionOK, ErrLumpSumBounds},
		{"above 50%", money.NGN(10000001), pensionOK, ErrLumpSumBounds},
		{"residual pension below 50% salary", money.NGN(6000000), money.NGN(199999), ErrResidualPension},
		{"residual pension exactly 50% salary", money.NGN(6000000), money.NGN(200000), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateLumpSum(tc.lump, balance, finalSalary, tc.pension)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestEnBloc(t *testing.T) {
	const minWage = money.Kobo(7000000) // ₦70,000 -> one third is ₦23,333.33
	cases := []struct {
		name    string
		pension money.Kobo
		want    bool
	}{
		{"well below third", money.NGN(20000), true},
		// Exact integer boundary: 3 × 2,333,333 = 6,999,999 < 7,000,000.
		{"kobo below exact third", money.Kobo(2333333), true},
		// 3 × 2,333,334 = 7,000,002 >= 7,000,000.
		{"kobo above exact third", money.Kobo(2333334), false},
		{"well above third", money.NGN(30000), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EnBloc(tc.pension, minWage); got != tc.want {
				t.Fatalf("EnBloc(%s) = %v, want %v", tc.pension.Naira(), got, tc.want)
			}
		})
	}
}

func TestJobLossEligible(t *testing.T) {
	disengaged := time.Date(2026, 2, 10, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name         string
		age          int
		asOf         time.Time
		alreadyTaken bool
		want         bool
	}{
		{"under 50, 4 months served", 42, disengaged.AddDate(0, 4, 0), false, true},
		{"under 50, 6 months served", 49, disengaged.AddDate(0, 6, 0), false, true},
		{"3 months 29 days: too soon", 42, disengaged.AddDate(0, 4, -1), false, false},
		{"age 50 barred", 50, disengaged.AddDate(0, 6, 0), false, false},
		{"over 50 barred", 55, disengaged.AddDate(0, 6, 0), false, false},
		{"already taken barred", 42, disengaged.AddDate(0, 6, 0), true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := JobLossEligible(tc.age, disengaged, tc.asOf, tc.alreadyTaken); got != tc.want {
				t.Fatalf("JobLossEligible() = %v, want %v", got, tc.want)
			}
		})
	}
}
