package invmandate

import (
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

// fsdAfrica models the announced vehicle: $750m target, $250m first close,
// Fund II committing with a 5% AUM cap, FX ₦1,530/$.
func fsdAfrica(t *testing.T) (*Engine, Mandate) {
	t.Helper()
	e := NewEngine()
	e.SetFX(153000)                                    // ₦1,530.00 per USD
	e.SetFundAUM("t1", "II", money.NGN(2_000_000_000)) // ₦2bn fund
	m, err := e.CreateMandate("t1", "Pension-Backed Infrastructure Platform", ClassInfraFund,
		"FSD Africa", "USD", 750_000_000_00, 0, now) // $750m in cents
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.CapBps != DefaultInfraCapBps {
		t.Fatalf("default cap: %d", m.CapBps)
	}
	return e, m
}

func TestCommitmentHappyPath(t *testing.T) {
	e, m := fsdAfrica(t)
	// $100m = 10_000_000_000 cents -> ₦153bn at ₦1,530... no:
	// 10e9 cents * 153000 kobo/$ / 100 = 1.53e13 kobo = ₦153bn. Fund AUM
	// ₦2bn => cap ₦100m. Use $50k = 5_000_000 cents -> ₦76.5m < ₦100m cap.
	c, err := e.RecordCommitment("t1", m.ID, "II", 5_000_000, "USD", "ic-2026-041", now)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if c.FundID != "II" || c.AmountMinor != 5_000_000 {
		t.Fatalf("bad commitment: %+v", c)
	}
	ex, err := e.Exposure("t1", m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ex.CommittedMinor != 5_000_000 || ex.HeadroomMinor != 750_000_000_00-5_000_000 {
		t.Fatalf("exposure: %+v", ex)
	}
}

func TestCapExceeded(t *testing.T) {
	e, m := fsdAfrica(t)
	// Cap = 5% of ₦2bn = ₦100m = $65,359 at ₦1,530. $70k must fail.
	if _, err := e.RecordCommitment("t1", m.ID, "II", 7_000_000, "USD", "ic-1", now); err != ErrCapExceeded {
		t.Fatalf("expected cap breach, got %v", err)
	}
	// $60k ok; a further $10k breaches the cumulative cap.
	if _, err := e.RecordCommitment("t1", m.ID, "II", 6_000_000, "USD", "ic-1", now); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := e.RecordCommitment("t1", m.ID, "II", 1_000_000, "USD", "ic-2", now); err != ErrCapExceeded {
		t.Fatalf("cumulative cap: got %v", err)
	}
	// A bigger fund (₦100bn AUM => ₦5bn cap ≈ $3.27m) takes $3m fine.
	e.SetFundAUM("t1", "I", money.NGN(100_000_000_000))
	if _, err := e.RecordCommitment("t1", m.ID, "I", 300_000_000, "USD", "ic-3", now); err != nil {
		t.Fatalf("fund I: %v", err)
	}
}

func TestNGNMandate(t *testing.T) {
	e := NewEngine()
	e.SetFundAUM("t1", "II", money.NGN(10_000_000_000)) // ₦10bn, 5% cap = ₦500m
	m, err := e.CreateMandate("t1", "NGN Infra Sukuk", ClassInfraBond, "FGN", "NGN",
		int64(money.NGN(5_000_000_000)), 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.RecordCommitment("t1", m.ID, "II", int64(money.NGN(400_000_000)), "NGN", "ic-1", now); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := e.RecordCommitment("t1", m.ID, "II", int64(money.NGN(200_000_000)), "NGN", "ic-2", now); err != ErrCapExceeded {
		t.Fatalf("ngn cap: got %v", err)
	}
	// Currency mixing rejected.
	if _, err := e.RecordCommitment("t1", m.ID, "II", 100, "USD", "ic-3", now); err != ErrCurrencyMix {
		t.Fatalf("currency: got %v", err)
	}
}

func TestTargetOversubscription(t *testing.T) {
	e := NewEngine()
	e.SetFundAUM("t1", "I", money.NGN(1_000_000_000_000)) // ₦1trn, huge cap
	m, _ := e.CreateMandate("t1", "Small Mandate", ClassInfraFund, "x", "NGN",
		int64(money.NGN(1_000_000)), 9000, now) // ₦1m target, 90% cap
	if _, err := e.RecordCommitment("t1", m.ID, "I", int64(money.NGN(800_000)), "NGN", "a", now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RecordCommitment("t1", m.ID, "I", int64(money.NGN(300_000)), "NGN", "b", now); err != ErrOverSubscribed {
		t.Fatalf("oversubscribe: got %v", err)
	}
}

func TestDeploymentGates(t *testing.T) {
	e, m := fsdAfrica(t)
	// Cannot deploy before any commitment.
	if _, err := e.RecordDeployment("t1", m.ID, 1_000_000, "USD", "spv-1", now); err != ErrOverDeployed {
		t.Fatalf("pre-commit deploy: got %v", err)
	}
	if _, err := e.RecordCommitment("t1", m.ID, "II", 5_000_000, "USD", "ic-1", now); err != nil {
		t.Fatal(err)
	}
	d, err := e.RecordDeployment("t1", m.ID, 3_000_000, "USD", "spv-lagos-road", now)
	if err != nil || d.Vehicle != "spv-lagos-road" {
		t.Fatalf("deploy: %v %+v", err, d)
	}
	// Only $20k undrawn left; $30k must fail.
	if _, err := e.RecordDeployment("t1", m.ID, 3_000_000, "USD", "spv-2", now); err != ErrOverDeployed {
		t.Fatalf("over-deploy: got %v", err)
	}
	ex, _ := e.Exposure("t1", m.ID)
	if ex.DeployedMinor != 3_000_000 || ex.UndrawnMinor != 2_000_000 {
		t.Fatalf("exposure: %+v", ex)
	}
}

func TestClosedMandateRejectsCommitments(t *testing.T) {
	e, m := fsdAfrica(t)
	if err := e.CloseMandate("t1", m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RecordCommitment("t1", m.ID, "II", 1_000_000, "USD", "ic-1", now); err != ErrMandateClosed {
		t.Fatalf("closed: got %v", err)
	}
	// Unknown mandate / tenant isolation.
	if _, err := e.RecordCommitment("t2", m.ID, "II", 1_000_000, "USD", "ic-1", now); err != ErrNoMandate {
		t.Fatalf("tenant: got %v", err)
	}
}

func TestMissingAUMFailsClosed(t *testing.T) {
	e := NewEngine()
	e.SetFX(153000)
	m, _ := e.CreateMandate("t1", "X", ClassInfraFund, "y", "USD", 1_000_000_00, 0, now)
	// No AUM for fund "III": must refuse, not silently allow.
	if _, err := e.RecordCommitment("t1", m.ID, "III", 100, "USD", "ic-1", now); err != ErrNoAUM {
		t.Fatalf("no AUM: got %v", err)
	}
}
