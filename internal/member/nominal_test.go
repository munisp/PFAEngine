package member

import (
	"errors"
	"testing"
	"time"
)

func TestNominalDue(t *testing.T) {
	employed := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		asOf   time.Time
		hasRSA bool
		want   bool
	}{
		{"before 6 months", time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC), false, false},
		{"exactly 6 months", time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC), false, true},
		{"after 6 months", time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), false, true},
		{"has RSA already", time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NominalDue(employed, tt.asOf, tt.hasRSA); got != tt.want {
				t.Fatalf("NominalDue = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNominalRSAClaim(t *testing.T) {
	n := &NominalRSA{PIN: "PEN-NOM-1", EmployerID: "emp-1", OpenedAt: time.Now().UTC()}
	if n.Claimed {
		t.Fatal("new nominal RSA already claimed")
	}
	if err := n.Claim("prs-1"); err != nil {
		t.Fatal(err)
	}
	if !n.Claimed || n.ClaimedBy != "prs-1" {
		t.Fatalf("after claim %+v", n)
	}
	if err := n.Claim("prs-2"); !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("second claim: %v", err)
	}
}
