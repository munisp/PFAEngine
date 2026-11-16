package claimsfast

import (
	"testing"
	"time"
)

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func TestPackToSTP(t *testing.T) {
	e := NewEngine()
	p := e.Open("t1", "RSA-1", now)
	if p.Score != 0 || p.STP || len(p.Missing) != 5 {
		t.Fatalf("open: %+v", p)
	}
	for _, key := range []string{"KYC", "DOCS", "EMPLOYER_CERT", "BANK_VERIFIED"} {
		p, _ = e.Complete("t1", "RSA-1", key)
		if p.STP {
			t.Fatal("STP before all items")
		}
	}
	p, err := e.Complete("t1", "RSA-1", "MPG_SCREEN")
	if err != nil || !p.STP || p.Score != 100 || len(p.Missing) != 0 {
		t.Fatalf("stp: %v %+v", err, p)
	}
}

func TestValidation(t *testing.T) {
	e := NewEngine()
	if _, err := e.Complete("t1", "ghost", "KYC"); err == nil {
		t.Fatal("ghost pack")
	}
	e.Open("t1", "RSA-1", now)
	if _, err := e.Complete("t1", "RSA-1", "BOGUS"); err == nil {
		t.Fatal("bogus item")
	}
}
