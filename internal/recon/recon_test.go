package recon

import (
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/contribution"
	"github.com/munisp/pfaengine/internal/money"
)

func cleanSchedule() *contribution.Schedule {
	return &contribution.Schedule{
		ID: "s1", TenantID: "t1", EmployerID: "emp1", Period: "2026-11",
		Lines: []contribution.Line{
			contribution.SplitLine("PEN100", money.NGN(800000), 0, false),
			contribution.SplitLine("PEN200", money.NGN(500000), 0, false),
		},
	}
}

func TestAutoMatch100OnCleanFiles(t *testing.T) {
	e := NewEngine()
	s := cleanSchedule()
	receipts := []Receipt{
		{ID: "r1", TenantID: "t1", EmployerID: "emp1", RSAPIN: "PEN100", Amount: money.NGN(144000), ValueDate: "2026-11-05", Source: "PCRS"},
		{ID: "r2", TenantID: "t1", EmployerID: "emp1", RSAPIN: "PEN200", Amount: money.NGN(90000), ValueDate: "2026-11-05", Source: "PCRS"},
	}
	res := e.Reconcile(s, receipts)
	if res.AutoMatched != 2 || res.ExceptionCnt != 0 {
		t.Fatalf("expected full auto-match, got %+v", res)
	}
	if res.AutoMatchRate() < 0.95 {
		t.Fatalf("BR-03 target missed: %.2f", res.AutoMatchRate())
	}
	if e.Suspense("emp1") != 0 {
		t.Fatal("suspense should be zero on clean match")
	}
}

func TestAmountMismatchRaisesExceptionAndSuspense(t *testing.T) {
	e := NewEngine()
	s := cleanSchedule()
	receipts := []Receipt{
		{ID: "r1", TenantID: "t1", EmployerID: "emp1", RSAPIN: "PEN100", Amount: money.NGN(140000), ValueDate: "2026-11-05", Source: "CUSTODIAN"},
		{ID: "r2", TenantID: "t1", EmployerID: "emp1", RSAPIN: "PEN200", Amount: money.NGN(90000), ValueDate: "2026-11-05", Source: "CUSTODIAN"},
	}
	res := e.Reconcile(s, receipts)
	if res.ExceptionCnt != 1 {
		t.Fatalf("expected 1 exception, got %d", res.ExceptionCnt)
	}
	if e.Suspense("emp1") != money.NGN(4000) {
		t.Fatalf("suspense %s", e.Suspense("emp1").Naira())
	}
	excs := e.Exceptions("t1")
	if len(excs) != 1 || excs[0].Reason != ReasonAmountMismatch {
		t.Fatalf("bad exception: %+v", excs)
	}
	if err := e.Resolve(excs[0].ID, "RECLASSIFY", "ops-user"); err != nil {
		t.Fatal(err)
	}
	if e.Suspense("emp1") != 0 {
		t.Fatal("resolution should release suspense")
	}
}

func TestEmployerDefaultDetection(t *testing.T) {
	s := cleanSchedule()
	receipts := []Receipt{} // employer deducted, never remitted
	alerts := DetectDefaults([]*contribution.Schedule{s}, receipts, 10)
	if len(alerts) != 1 || alerts[0].ShortfallPct != 100 {
		t.Fatalf("expected 100%% shortfall alert, got %+v", alerts)
	}
}

func TestAgedBuckets(t *testing.T) {
	e := NewEngine()
	s := cleanSchedule()
	e.Reconcile(s, nil)
	b := e.AgedBuckets("t1", time.Now())
	if b["0-7"] != 2 {
		t.Fatalf("expected 2 fresh exceptions, got %v", b)
	}
}
