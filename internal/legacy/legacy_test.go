package legacy

import (
	"errors"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

func TestSchemeCompliant(t *testing.T) {
	full := Scheme{Kind: AES, FullyFunded: true, CustodianLicensed: true, PFALicensed: true, Segregated: true}
	if m := full.Compliant(); len(m) != 0 {
		t.Fatalf("compliant AES: %v", m)
	}
	bare := Scheme{Kind: AES}
	if m := bare.Compliant(); len(m) != 4 {
		t.Fatalf("bare AES missing %v", m)
	}
	// ABS additionally requires the PenCom-approved Trust Deed.
	abs := Scheme{Kind: ABS, FullyFunded: true, CustodianLicensed: true, PFALicensed: true, Segregated: true}
	if m := abs.Compliant(); len(m) != 1 || m[0] != "trust deed not approved by PenCom" {
		t.Fatalf("ABS missing %v", m)
	}
	abs.TrustDeedApproved = true
	if m := abs.Compliant(); len(m) != 0 {
		t.Fatalf("compliant ABS: %v", m)
	}
}

func TestFeeModel(t *testing.T) {
	tests := []struct {
		k    SchemeKind
		want string
	}{
		{AES, "income-7.5pct"},
		{ABS, "income-7.5pct"},
		{CPFA, "capped-1.1pct"},
		{NSITF, "asset-based"},
	}
	for _, tt := range tests {
		if got := FeeModel(tt.k); got != tt.want {
			t.Fatalf("FeeModel(%v) = %s, want %s", tt.k, got, tt.want)
		}
	}
}

func TestPerformanceMethod(t *testing.T) {
	if got := PerformanceMethod(AES); got != "TWR" {
		t.Fatalf("AES method %s", got)
	}
	for _, k := range []SchemeKind{CPFA, ABS, NSITF} {
		if got := PerformanceMethod(k); got != "unit-price" {
			t.Fatalf("%v method %s", k, got)
		}
	}
}

func TestNSITFCredit(t *testing.T) {
	end := time.Date(2004, 5, 31, 0, 0, 0, 0, time.UTC)
	c, err := NewNSITFCredit("prs-1", money.NGN(250_000), end)
	if err != nil {
		t.Fatal(err)
	}
	if c.MemberID != "prs-1" || c.Amount != money.NGN(250_000) {
		t.Fatalf("credit %+v", c)
	}
	if _, err := NewNSITFCredit("prs-1", -1, end); !errors.Is(err, ErrNegativeCredit) {
		t.Fatalf("negative: %v", err)
	}
}
