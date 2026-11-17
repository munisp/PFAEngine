package tenancy

import (
	"testing"

	"github.com/munisp/pfaengine/internal/persist/persisttest"
)

func TestSnapshotRoundTrip(t *testing.T) {
	st := persisttest.New()
	a := NewRegistry()
	a.AttachSink(st)
	if _, err := a.Provision(Tenant{ID: "acme", Name: "Acme PFA", LicenseNo: "PFA-1234"}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetLicenseProfile("acme", LicenseProfile{LicenseNo: "PFA-1234", IssuedAt: "2020-01-01", ExpiresAt: "2030-01-01"}); err != nil {
		t.Fatal(err)
	}

	b := NewRegistry()
	if err := b.Restore(st.LoadAll()); err != nil {
		t.Fatal(err)
	}
	tn, ok := b.Get("acme")
	if !ok || tn.Name != "Acme PFA" || tn.LicenseNo != "PFA-1234" {
		t.Fatalf("tenant: %+v", tn)
	}
	lp, ok := b.LicenseProfileOf("acme")
	if !ok || lp.LicenseNo != "PFA-1234" || lp.Class != "PFA" || lp.Status != LicenseActive {
		t.Fatalf("license: %+v", lp)
	}
}
