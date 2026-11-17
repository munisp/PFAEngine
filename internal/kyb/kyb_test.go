package kyb

import (
	"testing"

	"github.com/munisp/pfaengine/internal/aml"
	"github.com/munisp/pfaengine/internal/casemgmt"
)

func newEngine() (*Engine, *casemgmt.Engine, *aml.Service) {
	a := aml.NewService()
	ce := casemgmt.New(nil)
	return New(a, ce), ce, a
}

var goodDirs = []Person{{Name: "Amara Okafor", DOB: "1980-05-05", Role: "MD"}}
var goodUBOs = []UBO{{Person: Person{Name: "Amara Okafor", DOB: "1980-05-05"}, OwnershipPct: 60}}

func TestVerifiedHappyPath(t *testing.T) {
	e, _, _ := newEngine()
	b, err := e.Submit("t1", "emp1", "Acme Ltd", "RC123456", "12345678", goodDirs, goodUBOs, []string{"ref://cac"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != StatusVerified {
		t.Fatalf("expected VERIFIED, got %s risk=%d flags=%v", b.Status, b.RiskScore, b.RiskFlags)
	}
	if b.CaseID != "" {
		t.Fatal("verified business must not open a case")
	}
}

func TestRCValidation(t *testing.T) {
	e, _, _ := newEngine()
	if _, err := e.Submit("t1", "e", "X", "123456", "", goodDirs, goodUBOs, nil, "t"); err != ErrBadRC {
		t.Fatalf("expected ErrBadRC, got %v", err)
	}
	if _, err := e.Submit("t1", "e", "X", " rc 1234567 ", "", goodDirs, goodUBOs, nil, "t"); err != nil {
		t.Fatalf("RC with spaces must normalise, got %v", err)
	}
}

func TestTINValidation(t *testing.T) {
	e, _, _ := newEngine()
	if _, err := e.Submit("t1", "e", "X", "RC12345", "ABC", goodDirs, goodUBOs, nil, "t"); err != ErrBadTIN {
		t.Fatalf("expected ErrBadTIN, got %v", err)
	}
}

func TestNoUBO25TriggersManualReview(t *testing.T) {
	e, ce, _ := newEngine()
	ubos := []UBO{
		{Person: Person{Name: "A B", DOB: "1970-01-01"}, OwnershipPct: 15},
		{Person: Person{Name: "C D", DOB: "1971-01-01"}, OwnershipPct: 15},
	}
	b, err := e.Submit("t1", "emp1", "Fragile Ltd", "RC99999", "", goodDirs, ubos, []string{"ref://cac"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != StatusManualReview {
		t.Fatalf("expected MANUAL_REVIEW, got %s risk=%d", b.Status, b.RiskScore)
	}
	if b.CaseID == "" {
		t.Fatal("manual review must open a KYC_REVIEW case")
	}
	c, err := ce.Get("t1", b.CaseID)
	if err != nil || c.Type != casemgmt.TypeKYCReview {
		t.Fatalf("expected KYC_REVIEW case, got %+v", c)
	}
}

func TestSanctionHitRejected(t *testing.T) {
	e, _, a := newEngine()
	a.LoadSanctions([][2]string{{"Bad Actor", "1960-01-01"}})
	dirs := []Person{{Name: "Bad Actor", DOB: "1960-01-01"}}
	b, err := e.Submit("t1", "emp1", "Shady Ltd", "RC55555", "", dirs, goodUBOs, []string{"ref://cac"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != StatusRejected {
		t.Fatalf("sanction hit must be rejected, got %s", b.Status)
	}
}

func TestReviewResolvesCase(t *testing.T) {
	e, ce, _ := newEngine()
	b, _ := e.Submit("t1", "emp1", "NoDocs Ltd", "RC77777", "", goodDirs, goodUBOs, nil, "t")
	if b.Status != StatusManualReview || b.CaseID == "" {
		t.Fatalf("expected manual review with case, got %s", b.Status)
	}
	b, err := e.Review("t1", b.ID, true, "staff1", "docs sighted")
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != StatusVerified {
		t.Fatalf("expected VERIFIED after approve, got %s", b.Status)
	}
	c, _ := ce.Get("t1", b.CaseID)
	if c.Status != casemgmt.Resolved {
		t.Fatalf("linked case must be resolved, got %s", c.Status)
	}
}

func TestOwnershipOver100(t *testing.T) {
	e, _, _ := newEngine()
	ubos := []UBO{
		{Person: Person{Name: "A B", DOB: "1970-01-01"}, OwnershipPct: 80},
		{Person: Person{Name: "C D", DOB: "1971-01-01"}, OwnershipPct: 40},
	}
	b, _ := e.Submit("t1", "emp1", "Over Ltd", "RC88888", "", goodDirs, ubos, []string{"ref://cac"}, "t")
	found := false
	for _, f := range b.RiskFlags {
		if f == "ownership_exceeds_100pct" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected ownership flag, got %v", b.RiskFlags)
	}
}
