package disclosure

import "testing"

func TestPublishWORM(t *testing.T) {
	r := New()
	p, err := r.Publish("t1", AuditedRSAFund, "RSA Fund Audited Accounts", "FY2025", "s3://dis/rsa2025.pdf", "sha", "cfo")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "" {
		t.Fatal("no id")
	}
	if _, err := r.Publish("t1", AuditedRSAFund, "RSA Fund Audited Accounts (restated)", "FY2025", "s3://dis/rsa2025b.pdf", "sha2", "cfo"); err == nil {
		t.Fatal("WORM: duplicate period must be rejected")
	}
	// new period is fine
	if _, err := r.Publish("t1", AuditedRSAFund, "RSA Fund Audited Accounts", "FY2026", "s3://dis/rsa2026.pdf", "sha3", "cfo"); err != nil {
		t.Fatal(err)
	}
	list := r.List("t1", AuditedRSAFund)
	if len(list) != 2 || list[0].Period != "FY2026" {
		t.Fatalf("list: %+v", list)
	}
	if _, err := r.Publish("t1", "BLOG", "x", "FY2026", "r", "", "u"); err == nil {
		t.Fatal("bad kind must fail")
	}
}
