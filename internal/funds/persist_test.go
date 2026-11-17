package funds

import (
	"testing"

	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/persist/persisttest"
)

func TestSnapshotRoundTrip(t *testing.T) {
	st := persisttest.New()
	a := NewRegistry()
	a.AttachSink(st)
	a.SeedStandard("t1")
	if err := a.PublishNAV(FundII, "2026-11-15", money.NGN(4)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Allocate("rsa-1", FundII, "2026-11-15", money.NGN(4)); err != nil {
		t.Fatal(err)
	}

	b := NewRegistry()
	snaps, _ := st.LoadSnapshots(nil, "t1")
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	p, err := b.Price(FundII, "2026-11-15")
	if err != nil || p != money.NGN(4) {
		t.Fatalf("price: %v %v", p, err)
	}
	hs := b.Holdings("rsa1")
	if len(hs) != 0 {
		t.Fatalf("unexpected holdings for rsa-1: %+v", hs)
	}
	hs = b.Holdings("rsa-1")
	if len(hs) != 1 || hs[0].FundID != FundII || hs[0].Units <= 0 {
		t.Fatalf("holdings not restored: %+v", hs)
	}
	if v := b.Value("rsa-1", "2026-11-15"); v != money.NGN(4) {
		t.Fatalf("value = %v, want 400 kobo", v)
	}
	if got := len(b.FundIDs()); got != 6 {
		t.Fatalf("funds restored = %d, want 6", got)
	}
}
