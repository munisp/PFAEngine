package death

import (
	"errors"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

func TestRequiredDocs(t *testing.T) {
	tests := []struct {
		dt   DocType
		last string
	}{
		{WillProbate, "will_admitted_to_probate"},
		{LetterOfAdmin, "letter_of_administration"},
	}
	for _, tt := range tests {
		docs := RequiredDocs(tt.dt)
		if len(docs) != 4 {
			t.Fatalf("docs %v", docs)
		}
		for _, want := range []string{"death_certificate", "burial_warrant", "nok_details", tt.last} {
			found := false
			for _, d := range docs {
				if d == want {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %q in %v", want, docs)
			}
		}
	}
}

func TestClaimHappyPath(t *testing.T) {
	c, err := NewClaim("RSA-1", "Late Member", WillProbate, "maker1", now)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusSubmitted {
		t.Fatalf("status %s", c.Status)
	}
	if err := c.VerifyDocs("ops1", true); err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusDocsVerified || !c.DocsVerified || !c.NOKKYCPassed {
		t.Fatalf("after verify %+v", c)
	}
	if err := c.Approve("checker1"); err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusApproved || c.ApprovedBy != "checker1" {
		t.Fatalf("after approve %+v", c)
	}
}

func TestClaimMakerCheckerViolation(t *testing.T) {
	c, _ := NewClaim("RSA-2", "Late Member", LetterOfAdmin, "maker1", now)
	if err := c.VerifyDocs("ops1", true); err != nil {
		t.Fatal(err)
	}
	if err := c.Approve("maker1"); !errors.Is(err, ErrMakerChecker) {
		t.Fatalf("same maker/checker: %v", err)
	}
	if err := c.Approve(""); !errors.Is(err, ErrMakerChecker) {
		t.Fatalf("empty approver: %v", err)
	}
}

func TestClaimDocGating(t *testing.T) {
	// Approval blocked before docs verification.
	c, _ := NewClaim("RSA-3", "Late Member", WillProbate, "maker1", now)
	if err := c.Approve("checker1"); !errors.Is(err, ErrDocsNotVerified) {
		t.Fatalf("approve before docs: %v", err)
	}
	// Approval blocked when NOK KYC failed.
	if err := c.VerifyDocs("ops1", false); err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusSubmitted {
		t.Fatalf("status after failed NOK KYC: %s", c.Status)
	}
	if err := c.Approve("checker1"); !errors.Is(err, ErrDocsNotVerified) {
		t.Fatalf("approve with failed NOK KYC: %v", err)
	}
	// Re-verification is rejected.
	if err := c.VerifyDocs("ops1", true); !errors.Is(err, ErrAlreadyVerified) {
		t.Fatalf("re-verify: %v", err)
	}
}
