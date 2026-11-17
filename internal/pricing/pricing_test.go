package pricing

import (
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/persist/persisttest"
)

var ts = time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

func TestCatalogSeededAndSorted(t *testing.T) {
	e := NewEngine()
	cat := e.Catalog()
	if len(cat) != 4 {
		t.Fatalf("catalog len = %d, want 4", len(cat))
	}
	// Canonical published order: STARTER (₦50/member) … ENTERPRISE (₦35).
	want := []string{"STARTER", "GROWTH", "SCALE", "ENTERPRISE"}
	for i, code := range want {
		if cat[i].Code != code {
			t.Fatalf("catalog[%d] = %s, want %s", i, cat[i].Code, code)
		}
	}
	if _, ok := e.TierOf("growth"); !ok {
		t.Fatal("TierOf should be case-insensitive")
	}
	if _, ok := e.TierOf("NOPE"); ok {
		t.Fatal("TierOf should miss unknown codes")
	}
}

func TestQuoteMathKoboExact(t *testing.T) {
	e := NewEngine()
	// GROWTH: ₦45/member, 2 bps AUM, 5,000 included, ₦50 overage.
	if _, err := e.SetPlan("t1", "GROWTH", "2025-01-01", "admin", ts); err != nil {
		t.Fatal(err)
	}
	// 7,200 members, ₦120,000,000.00 AUM = 12,000,000,000 kobo.
	q, err := e.Quote("t1", 7200, 12_000_000_000, "2025-06")
	if err != nil {
		t.Fatal(err)
	}
	if q.Assumed {
		t.Fatal("plan is assigned; quote must not be assumed")
	}
	if q.MemberCharge != 4500*7200 {
		t.Fatalf("member charge = %d, want %d", q.MemberCharge, 4500*7200)
	}
	if q.OverageCharge != 5000*2200 {
		t.Fatalf("overage = %d, want %d", q.OverageCharge, 5000*2200)
	}
	// AUM: 12_000_000_000 / 12 * 2 / 10000 = 200_000 kobo (₦2,000).
	if q.AUMCharge != 200_000 {
		t.Fatalf("aum charge = %d, want 200000", q.AUMCharge)
	}
	if q.Total != q.MemberCharge+q.OverageCharge+q.AUMCharge {
		t.Fatalf("total = %d does not sum components", q.Total)
	}
	if len(q.Lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(q.Lines))
	}
	var sum money.Kobo
	for _, l := range q.Lines {
		sum += l.Amount
	}
	if sum != q.Total {
		t.Fatalf("lines sum %d != total %d", sum, q.Total)
	}
}

func TestQuoteFloorRoundingAndBoundaries(t *testing.T) {
	e := NewEngine()
	// STARTER: 500 included — 500 members exactly means zero overage.
	q, err := e.Quote("t2", 500, 0, "2025-06")
	if err != nil {
		t.Fatal(err)
	}
	if !q.Assumed {
		t.Fatal("unassigned tenant should be quoted on the assumed default tier")
	}
	if q.TierCode != DefaultTier {
		t.Fatalf("tier = %s, want %s", q.TierCode, DefaultTier)
	}
	if q.OverageCharge != 0 {
		t.Fatalf("overage at boundary = %d, want 0", q.OverageCharge)
	}
	// 501 members → exactly one overage member at ₦60.
	q, _ = e.Quote("t2", 501, 0, "2025-06")
	if q.OverageCharge != 6000 {
		t.Fatalf("overage at boundary+1 = %d, want 6000", q.OverageCharge)
	}
	// Floor rounding: AUM 100 kobo at 3 bps → 100/12*3/10000 = 8*3/10000 = 0.
	q, _ = e.Quote("t2", 0, 100, "2025-06")
	if q.AUMCharge != 0 {
		t.Fatalf("aum charge = %d, want 0 (floor)", q.AUMCharge)
	}
	// 12_000_100 kobo AUM: /12 = 1_000_008 (floor), *3/10000 = 300 kobo.
	q, _ = e.Quote("t2", 0, 12_000_100, "2025-06")
	if q.AUMCharge != 300 {
		t.Fatalf("aum charge = %d, want 300", q.AUMCharge)
	}
	if _, err := e.Quote("t2", -1, 0, "2025-06"); err == nil {
		t.Fatal("negative members should fail")
	}
	if _, err := e.Quote("t2", 0, 0, "2025-13"); err == nil {
		t.Fatal("bad period should fail")
	}
}

func TestEffectiveDating(t *testing.T) {
	e := NewEngine()
	if _, err := e.SetPlan("t1", "STARTER", "2025-01-01", "admin", ts); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SetPlan("t1", "SCALE", "2025-06-15", "admin", ts); err != nil {
		t.Fatal(err)
	}
	// Before any effective date: nothing in force.
	if _, ok := e.GetPlan("t1", "2024-12-31"); ok {
		t.Fatal("no plan should be in force before first effective date")
	}
	a, ok := e.GetPlan("t1", "2025-06-14")
	if !ok || a.TierCode != "STARTER" {
		t.Fatalf("plan on 2025-06-14 = %+v, want STARTER", a)
	}
	a, ok = e.GetPlan("t1", "2025-06-15")
	if !ok || a.TierCode != "SCALE" {
		t.Fatalf("plan on 2025-06-15 = %+v, want SCALE", a)
	}
	// Quotes key off the period start: June quotes still use STARTER
	// (effective 2025-06-01), July quotes switch to SCALE.
	q, _ := e.Quote("t1", 100, 0, "2025-06")
	if q.TierCode != "STARTER" {
		t.Fatalf("june quote tier = %s, want STARTER", q.TierCode)
	}
	q, _ = e.Quote("t1", 100, 0, "2025-07")
	if q.TierCode != "SCALE" {
		t.Fatalf("july quote tier = %s, want SCALE", q.TierCode)
	}
	if got := len(e.Assignments("t1")); got != 2 {
		t.Fatalf("history len = %d, want 2", got)
	}
	// Idempotent retry of the same (tier, effectiveFrom) returns the original.
	dup, err := e.SetPlan("t1", "STARTER", "2025-01-01", "admin", ts)
	if err != nil || len(e.Assignments("t1")) != 2 {
		t.Fatalf("idempotent re-set: err=%v history=%d", err, len(e.Assignments("t1")))
	}
	if dup.TierCode != "STARTER" {
		t.Fatalf("dup = %+v", dup)
	}
	// Audit fields recorded.
	if dup.SetBy != "admin" || dup.SetAt.IsZero() {
		t.Fatalf("audit fields missing: %+v", dup)
	}
	if _, err := e.SetPlan("t1", "NOPE", "2025-01-01", "admin", ts); err == nil {
		t.Fatal("unknown tier should fail")
	}
	if _, err := e.SetPlan("t1", "GROWTH", "01/06/2025", "admin", ts); err == nil {
		t.Fatal("bad effective date should fail")
	}
}

func TestUpsertTierOverride(t *testing.T) {
	e := NewEngine()
	if err := e.UpsertTier(Tier{Code: "custom", Name: "Custom", PerMemberMonthly: 100, AUMBps: 0, IncludedMembers: 0, OveragePerMember: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SetPlan("t1", "CUSTOM", "2025-01-01", "admin", ts); err != nil {
		t.Fatal(err)
	}
	q, _ := e.Quote("t1", 10, 0, "2025-06")
	if q.Total != 1000 {
		t.Fatalf("custom tier quote total = %d, want 1000", q.Total)
	}
	if err := e.UpsertTier(Tier{Code: " "}); err == nil {
		t.Fatal("empty tier should fail")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	st := persisttest.New()
	a := NewEngine()
	a.AttachSink(st)
	if err := a.UpsertTier(Tier{Code: "LOYALTY", Name: "Loyalty", PerMemberMonthly: 250, IncludedMembers: 10}); err != nil {
		t.Fatal(err)
	}
	as, err := a.SetPlan("t1", "LOYALTY", "2025-03-01", "admin", ts)
	if err != nil {
		t.Fatal(err)
	}

	b := NewEngine()
	snaps, err := st.LoadSnapshots(nil, "t1")
	if err != nil {
		t.Fatal(err)
	}
	global, err := st.LoadSnapshots(nil, "_global")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Restore(append(snaps, global...)); err != nil {
		t.Fatal(err)
	}
	got, ok := b.GetPlan("t1", "2025-06-01")
	if !ok || got.TierCode != "LOYALTY" || got.SetBy != "admin" {
		t.Fatalf("restored plan = %+v ok=%v", got, ok)
	}
	q, err := b.Quote("t1", 12, 0, "2025-06")
	if err != nil || q.TierCode != "LOYALTY" {
		t.Fatalf("restored quote = %+v err=%v", q, err)
	}
	// Seq continuation: no ID collision after restore.
	as2, err := b.SetPlan("t1", "STARTER", "2025-07-01", "admin", ts)
	if err != nil {
		t.Fatal(err)
	}
	if as2.ID == as.ID {
		t.Fatalf("assignment ID collision after restore: %s", as2.ID)
	}
}
