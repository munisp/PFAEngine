package employer

import (
	"errors"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var (
	now    = time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	expiry = time.Date(2027, 6, 30, 0, 0, 0, 0, time.UTC)
)

func goodPolicy() GLIP {
	return GLIP{EmployerID: "emp-1", Insurer: "Leadway", PolicyNo: "GL-123", CoverMultiple: 300, Expiry: expiry}
}

func TestGLIPValid(t *testing.T) {
	emolument := money.NGN(120_000_000) // ₦120m total emolument
	cover := money.NGN(360_000_000)     // exactly 3x
	if err := goodPolicy().Valid(emolument, cover, now); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		g    GLIP
		cov  money.Kobo
		at   time.Time
		want error
	}{
		{"cover multiple below 3x", GLIP{EmployerID: "e", Insurer: "L", PolicyNo: "P", CoverMultiple: 250, Expiry: expiry}, cover, now, ErrCoverMultiple},
		{"cover amount below 3x emolument", goodPolicy(), money.NGN(360_000_000) - 1, now, ErrCoverAmount},
		{"expired policy", goodPolicy(), cover, expiry, ErrPolicyExpired},
		{"missing policy number", GLIP{EmployerID: "e", Insurer: "L", CoverMultiple: 300, Expiry: expiry}, cover, now, ErrMissingPolicyNo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.g.Valid(emolument, tt.cov, tt.at); !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestClearanceEvidence(t *testing.T) {
	if m := ClearanceEvidence(goodPolicy(), 3); len(m) != 0 {
		t.Fatalf("complete evidence: %v", m)
	}
	m := ClearanceEvidence(goodPolicy(), 1)
	if len(m) != 2 || m[0] != "remittance_evidence_year_2" {
		t.Fatalf("missing %v", m)
	}
	m = ClearanceEvidence(GLIP{EmployerID: "e"}, 0)
	if len(m) != 4 || m[0] != "glip_policy_document" {
		t.Fatalf("missing %v", m)
	}
}
