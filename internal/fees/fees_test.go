package fees

import (
	"testing"

	"github.com/munisp/pfaengine/internal/money"
)

func testPack() RatePack {
	return RatePack{
		FundRates:          map[string]int64{"I": 165, "II": 155},
		AdminActiveKobo:    10000,
		AdminRetireeKobo:   5000,
		MicroHighKobo:      8000,
		MicroLowKobo:       2000,
		MicroHighThreshold: 400000,
	}
}

func TestAdminFee(t *testing.T) {
	rp := testPack()
	cases := []struct {
		name     string
		kind     MemberKind
		contrib  money.Kobo
		made     bool
		inactive int
		want     money.Kobo
	}{
		{"active", KindActive, money.NGN(50000), true, 0, 10000},
		{"retiree", KindRetiree, money.NGN(10000), true, 0, 5000},
		{"micro high", KindMicro, money.NGN(4000), true, 0, 8000}, // exactly ₦4,000 -> high tier
		{"micro low below threshold", KindMicro, money.NGN(3999), true, 0, 2000},
		{"micro low small", KindMicro, money.NGN(500), true, 0, 2000},
		{"no contribution", KindActive, 0, false, 0, 0},
		{"micro no contribution", KindMicro, 0, false, 0, 0},
		{"fee holiday 3 months", KindActive, 0, false, 3, 0},
		{"fee holiday beyond 3", KindRetiree, 0, false, 7, 0},
		{"2 inactive months still charged when contributing", KindActive, money.NGN(20000), true, 2, 10000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AdminFee(rp, tc.kind, tc.contrib, tc.made, tc.inactive); got != tc.want {
				t.Fatalf("AdminFee() = %s kobo, want %d", got.Naira(), tc.want)
			}
		})
	}
}

func TestRatePackFromParams(t *testing.T) {
	params := []byte(`{"fees":{
		"admin_active_kobo":10000,"admin_retiree_kobo":5000,
		"micro_high_kobo":8000,"micro_low_kobo":2000,
		"micro_high_threshold_kobo":400000,
		"asset_based_bps":{"I":165,"II":155,"III":140}}}`)
	rp, err := RatePackFromParams(params)
	if err != nil {
		t.Fatal(err)
	}
	if rp.AdminActiveKobo != 10000 || rp.AdminRetireeKobo != 5000 {
		t.Fatalf("admin fees: %+v", rp)
	}
	if rp.MicroHighKobo != 8000 || rp.MicroLowKobo != 2000 || rp.MicroHighThreshold != 400000 {
		t.Fatalf("micro tiers: %+v", rp)
	}
	if rp.FundRates["II"] != 155 {
		t.Fatalf("fund rates: %v", rp.FundRates)
	}
	if _, err := RatePackFromParams([]byte(`{bad`)); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestAccrueDaily(t *testing.T) {
	// ₦10,000,000 NAV at 155 bps/year => 1,000,000,000 * 155 / 3,650,000 ≈ 42,465.75 -> 42,466 kobo.
	got := AccrueDaily(money.NGN(10000000), 155)
	want := money.Kobo(42466)
	if got != want {
		t.Fatalf("AccrueDaily() = %d, want %d", got, want)
	}
	if AccrueDaily(0, 155) != 0 {
		t.Fatal("zero NAV must accrue zero")
	}
}
