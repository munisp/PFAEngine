package money

import "testing"

func TestParseKobo(t *testing.T) {
	k, err := ParseKobo("1,728,000.00")
	if err != nil || k != Kobo(172800000) {
		t.Fatalf("got %v err %v", k, err)
	}
	k, _ = ParseKobo("0.05")
	if k != Kobo(5) {
		t.Fatalf("got %v", k)
	}
}

func TestAllocateUnits8Decimals(t *testing.T) {
	u, err := AllocateUnits(NGN(144000), NGN(1000))
	if err != nil {
		t.Fatal(err)
	}
	if u != Unit(144*UnitScale) {
		t.Fatalf("got %s", u)
	}
	u, _ = AllocateUnits(Kobo(100), Kobo(300))
	if u.String() != "0.33333333" {
		t.Fatalf("got %s", u)
	}
}

func TestNoFloatRoundTrip(t *testing.T) {
	u, _ := ParseUnit("12.12345678")
	if u.String() != "12.12345678" {
		t.Fatalf("round trip failed: %s", u)
	}
}
