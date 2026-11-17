package disclosure

import (
	"testing"

	"github.com/munisp/pfaengine/internal/persist/persisttest"
)

func TestSnapshotRoundTrip(t *testing.T) {
	st := persisttest.New()
	a := New()
	a.AttachSink(st)
	p, err := a.Publish("t1", AuditedRSAFund, "Audited RSA Fund Accounts", "FY2025", "s3://x", "sha", "cfo")
	if err != nil {
		t.Fatal(err)
	}
	b := New()
	snaps, _ := st.LoadSnapshots(nil, "t1")
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	if got := b.List("t1", AuditedRSAFund); len(got) != 1 || got[0].Title != "Audited RSA Fund Accounts" {
		t.Fatalf("list: %+v", got)
	}
	// WORM survives restore: duplicate kind+period still rejected.
	if _, err := b.Publish("t1", AuditedRSAFund, "dupe", "FY2025", "s3://y", "sha", "cfo"); err == nil {
		t.Fatal("WORM duplicate accepted after restore")
	}
	// Seq continuation.
	p2, err := b.Publish("t1", AnnualReport, "Annual Report", "FY2025", "s3://z", "sha", "cfo")
	if err != nil {
		t.Fatal(err)
	}
	if p2.ID == p.ID {
		t.Fatalf("ID collision: %s", p2.ID)
	}
}
