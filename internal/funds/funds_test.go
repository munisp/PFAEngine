package funds

import (
	"testing"

	"github.com/munisp/pfaengine/internal/money"
)

func TestMultiFundDefaults(t *testing.T) {
	if DefaultFund(34, false, false, false) != FundII {
		t.Fatal("age 34 should default to Fund II")
	}
	if DefaultFund(52, false, false, false) != FundIII {
		t.Fatal("age 52 should default Fund III")
	}
	if DefaultFund(60, true, false, false) != FundIV {
		t.Fatal("retiree should be Fund IV")
	}
	if DefaultFund(30, false, true, false) != FundVI {
		t.Fatal("sharia opt-in should be Fund VI")
	}
}

func TestUnitization(t *testing.T) {
	r := NewRegistry()
	r.Register(Fund{ID: FundII, TenantID: "t1", Name: "Fund II", Currency: "NGN"})
	if err := r.PublishNAV(FundII, "2026-11-16", money.NGN(1000)); err != nil {
		t.Fatal(err)
	}
	u, err := r.Allocate("rsa-1", FundII, "2026-11-16", money.NGN(144000))
	if err != nil {
		t.Fatal(err)
	}
	if u != money.Unit(144*money.UnitScale) {
		t.Fatalf("expected 144 units, got %s", u)
	}
	// NAV rises 1%: value should follow.
	r.PublishNAV(FundII, "2026-11-17", money.NGN(1010))
	if v := r.Value("rsa-1", "2026-11-17"); v != money.NGN(145440) {
		t.Fatalf("value %s", v.Naira())
	}
}
