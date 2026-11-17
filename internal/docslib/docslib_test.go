package docslib

import "testing"

func TestPublishVersionsAndList(t *testing.T) {
	l := New()
	l.SeedStandard("t1")
	forms := l.List("t1", "")
	if len(forms) != 9 {
		t.Fatalf("seed: %d forms", len(forms))
	}
	// publish a new version of RSA-REG-01 → old deactivates
	f2, err := l.Publish("t1", "RSA-REG-01", "RSA Registration Form v2", CatRSARegistration, "updated", "s3://forms/rsa2.pdf", "sha")
	if err != nil {
		t.Fatal(err)
	}
	if f2.Version != 2 {
		t.Fatalf("version: %d", f2.Version)
	}
	active := l.List("t1", CatRSARegistration)
	if len(active) != 1 || active[0].Version != 2 {
		t.Fatalf("active: %+v", active)
	}
	v := l.Versions("t1", "RSA-REG-01")
	if len(v) != 2 || v[0].Active {
		t.Fatalf("versions: %+v", v)
	}
	if n := len(l.List("t2", "")); n != 0 {
		t.Fatalf("tenant isolation: %d", n)
	}
}

func TestPublishValidation(t *testing.T) {
	l := New()
	if _, err := l.Publish("", "X", "T", CatKYC, "", "", ""); err == nil {
		t.Fatal("tenant required")
	}
}
