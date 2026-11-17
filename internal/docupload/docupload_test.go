package docupload

import (
	"testing"

	"github.com/munisp/pfaengine/internal/casemgmt"
)

func TestSubmitOpensReviewCaseAndReviewResolves(t *testing.T) {
	cm := casemgmt.New(nil)
	e := New(cm)
	d, err := e.Submit("t1", "m1", DocNINSlip, "nin.pdf", "application/pdf", 1024, "s3://kyc/nin.pdf", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if d.CaseID == "" {
		t.Fatal("no KYC_REVIEW case linked")
	}
	c, err := cm.Get("t1", d.CaseID)
	if err != nil || c.Type != casemgmt.TypeKYCReview || c.Queue != casemgmt.QueueCompliance {
		t.Fatalf("case: %v %+v", err, c)
	}
	if len(e.Pending("t1")) != 1 {
		t.Fatal("pending queue empty")
	}
	if _, err := e.StartReview("t1", d.ID, "rev-1"); err != nil {
		t.Fatal(err)
	}
	got, err := e.Review("t1", d.ID, "rev-1", true, "legible and valid")
	if err != nil || got.Status != Approved {
		t.Fatalf("review: %v %+v", err, got)
	}
	c, _ = cm.Get("t1", d.CaseID)
	if c.Status != casemgmt.Resolved {
		t.Fatalf("linked case not resolved: %s", c.Status)
	}
	if _, err := e.Review("t1", d.ID, "rev-1", false, "again"); err == nil {
		t.Fatal("double decision must fail")
	}
}

func TestSubmitValidation(t *testing.T) {
	e := New(nil)
	if _, err := e.Submit("t1", "m1", "COFFEE_RECEIPT", "x.pdf", "application/pdf", 10, "r", ""); err == nil {
		t.Fatal("bad kind must fail")
	}
	if _, err := e.Submit("t1", "m1", DocIDCard, "x.pdf", "application/pdf", 20<<20, "r", ""); err == nil {
		t.Fatal("oversize must fail")
	}
}
