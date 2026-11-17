package partners

import (
	"errors"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/persist/persisttest"
)

var ts = time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

func onboardActive(t *testing.T, e *Engine, tenant, name, code string, bps int64) Partner {
	t.Helper()
	p, err := e.Onboard(tenant, name, Bank, code, bps, "ops", ts)
	if err != nil {
		t.Fatal(err)
	}
	p, err = e.Approve(tenant, p.ID, "admin", ts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLifecycle(t *testing.T) {
	e := NewEngine()
	p, err := e.Onboard("t1", "Sterling Bank", Bank, "sterling-ng", 250, "ops", ts)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != Pending || p.ReferralCode != "STERLING-NG" {
		t.Fatalf("onboarded partner = %+v", p)
	}
	if p.OnboardedBy != "ops" || p.OnboardedAt.IsZero() {
		t.Fatalf("audit fields missing: %+v", p)
	}
	// PENDING partners cannot take attributions.
	if _, err := e.AttributeJoin("t1", "m1", "STERLING-NG", ts); !errors.Is(err, ErrNotActive) {
		t.Fatalf("pending attribution err = %v, want ErrNotActive", err)
	}
	// Approve → ACTIVE with audit fields.
	p, err = e.Approve("t1", p.ID, "admin", ts)
	if err != nil || p.Status != Active || p.ApprovedBy != "admin" {
		t.Fatalf("approve = %+v err=%v", p, err)
	}
	// Approve again is an illegal transition.
	if _, err := e.Approve("t1", p.ID, "admin", ts); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("double approve err = %v, want ErrBadTransition", err)
	}
	// Suspend → audit fields; suspend again fails.
	p, err = e.Suspend("t1", p.ID, "admin", "AML review", ts)
	if err != nil || p.Status != Suspended || p.SuspendReason != "AML review" || p.SuspendedBy != "admin" {
		t.Fatalf("suspend = %+v err=%v", p, err)
	}
	if _, err = e.Suspend("t1", p.ID, "admin", "again", ts); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("double suspend err = %v", err)
	}
	// Reinstate from SUSPENDED.
	p, err = e.Approve("t1", p.ID, "admin", ts)
	if err != nil || p.Status != Active || p.SuspendReason != "" {
		t.Fatalf("reinstate = %+v err=%v", p, err)
	}
	// Unknown partner.
	if _, err := e.Approve("t1", "prt-999999", "admin", ts); !errors.Is(err, ErrNoPartner) {
		t.Fatalf("approve unknown err = %v", err)
	}
}

func TestOnboardValidation(t *testing.T) {
	e := NewEngine()
	if _, err := e.Onboard("t1", "X", Type("NGO"), "ABC-123", 100, "ops", ts); !errors.Is(err, ErrBadType) {
		t.Fatalf("bad type err = %v", err)
	}
	if _, err := e.Onboard("t1", "X", Bank, "ab", 100, "ops", ts); !errors.Is(err, ErrBadCode) {
		t.Fatalf("short code err = %v", err)
	}
	if _, err := e.Onboard("t1", "X", Bank, "bad code!", 100, "ops", ts); !errors.Is(err, ErrBadCode) {
		t.Fatalf("slug charset err = %v", err)
	}
	if _, err := e.Onboard("t1", "X", Bank, "ABC-123", 10001, "ops", ts); !errors.Is(err, ErrBadBps) {
		t.Fatalf("bps err = %v", err)
	}
	if _, err := e.Onboard("t1", "", Bank, "ABC-123", 100, "ops", ts); err == nil {
		t.Fatal("empty name should fail")
	}
	onboardActive(t, e, "t1", "MTN", "mtn-ng", 100)
	// Referral codes are unique per tenant, case-insensitively…
	if _, err := e.Onboard("t1", "MTN Clone", Telco, "MTN-NG", 100, "ops", ts); !errors.Is(err, ErrCodeTaken) {
		t.Fatalf("dup code err = %v", err)
	}
	// …but reusable across tenants.
	if _, err := e.Onboard("t2", "MTN NG", Telco, "mtn-ng", 100, "ops", ts); err != nil {
		t.Fatalf("cross-tenant code reuse: %v", err)
	}
}

func TestAttributionFirstWriteWinsAndIdempotent(t *testing.T) {
	e := NewEngine()
	bank := onboardActive(t, e, "t1", "Sterling", "sterling", 250)
	telco := onboardActive(t, e, "t1", "MTN", "mtn", 100)

	a1, err := e.AttributeJoin("t1", "m1", "sterling", ts)
	if err != nil {
		t.Fatal(err)
	}
	if a1.PartnerID != bank.ID || a1.ReferralCode != "STERLING" {
		t.Fatalf("attribution = %+v", a1)
	}
	// Retry with the same code → same record.
	a2, err := e.AttributeJoin("t1", "m1", "STERLING", ts.Add(time.Hour))
	if err != nil || a2.ID != a1.ID || !a2.At.Equal(a1.At) {
		t.Fatalf("idempotent retry = %+v err=%v", a2, err)
	}
	// First write wins: a different code does not rebind.
	a3, err := e.AttributeJoin("t1", "m1", "MTN", ts.Add(2*time.Hour))
	if err != nil || a3.PartnerID != bank.ID {
		t.Fatalf("first-write-wins violated: %+v err=%v", a3, err)
	}
	if got := e.Attributions("t1", telco.ID); len(got) != 0 {
		t.Fatalf("telco attributions = %+v, want none", got)
	}
	if got := e.Attributions("t1", bank.ID); len(got) != 1 || got[0].MemberID != "m1" {
		t.Fatalf("bank attributions = %+v", got)
	}
	// Unknown code and empty member rejected.
	if _, err := e.AttributeJoin("t1", "m2", "NOPE", ts); !errors.Is(err, ErrUnknownCode) {
		t.Fatalf("unknown code err = %v", err)
	}
	if _, err := e.AttributeJoin("t1", "", "STERLING", ts); err == nil {
		t.Fatal("empty member should fail")
	}
	// AttributionOf round-trip.
	got, ok := e.AttributionOf("t1", "m1")
	if !ok || got.PartnerID != bank.ID {
		t.Fatalf("AttributionOf = %+v ok=%v", got, ok)
	}
}

func TestAccrualMathAndSettlement(t *testing.T) {
	e := NewEngine()
	p := onboardActive(t, e, "t1", "Sterling", "sterling", 250) // 2.5%
	if _, err := e.AttributeJoin("t1", "m1", "STERLING", ts); err != nil {
		t.Fatal(err)
	}
	// ₦10,000.00 = 1,000,000 kobo at 250 bps → 25,000 kobo (₦250).
	ac, attributed, err := e.AccrueContribution("t1", "m1", 1_000_000, "2025-06", ts)
	if err != nil || !attributed {
		t.Fatalf("accrual = %+v attributed=%v err=%v", ac, attributed, err)
	}
	if ac.Amount != 25_000 || ac.Bps != 250 || ac.Contribution != 1_000_000 {
		t.Fatalf("accrual math wrong: %+v", ac)
	}
	// Floor rounding: 999 kobo at 250 bps → 999*250/10000 = 24 kobo.
	ac2, _, err := e.AccrueContribution("t1", "m1", 999, "2025-06", ts)
	if err != nil || ac2.Amount != 24 {
		t.Fatalf("floor accrual = %+v err=%v", ac2, err)
	}
	// Unattributed member: no accrual, no error.
	if _, attributed, err := e.AccrueContribution("t1", "m-direct", 1_000_000, "2025-06", ts); err != nil || attributed {
		t.Fatalf("unattributed accrual attributed=%v err=%v", attributed, err)
	}
	// Bad amount.
	if _, _, err := e.AccrueContribution("t1", "m1", 0, "2025-06", ts); !errors.Is(err, ErrBadAmount) {
		t.Fatalf("zero contribution err = %v", err)
	}
	// Balance and settlement.
	if bal := e.PartnerBalance("t1", p.ID); bal != 25_024 {
		t.Fatalf("balance = %d, want 25024", bal)
	}
	rep, err := e.SettlementReport("t1", p.ID, "2025-06", ts)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Lines) != 2 || rep.Total != 25_024 {
		t.Fatalf("settlement = %+v", rep)
	}
	rep, err = e.SettlementReport("t1", p.ID, "2025-07", ts)
	if err != nil || len(rep.Lines) != 0 || rep.Total != 0 {
		t.Fatalf("empty-period settlement = %+v err=%v", rep, err)
	}
	if _, err := e.SettlementReport("t1", "prt-999999", "2025-06", ts); !errors.Is(err, ErrNoPartner) {
		t.Fatalf("unknown partner settlement err = %v", err)
	}
}

func TestSuspensionBlocksAccrual(t *testing.T) {
	e := NewEngine()
	p := onboardActive(t, e, "t1", "Sterling", "sterling", 250)
	if _, err := e.AttributeJoin("t1", "m1", "STERLING", ts); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Suspend("t1", p.ID, "admin", "audit", ts); err != nil {
		t.Fatal(err)
	}
	if _, attributed, err := e.AccrueContribution("t1", "m1", 1_000_000, "2025-06", ts); !errors.Is(err, ErrSuspended) || !attributed {
		t.Fatalf("suspended accrual attributed=%v err=%v, want ErrSuspended", attributed, err)
	}
	if bal := e.PartnerBalance("t1", p.ID); bal != 0 {
		t.Fatalf("balance after blocked accrual = %d, want 0", bal)
	}
	// New joins are blocked too while suspended.
	if _, err := e.AttributeJoin("t1", "m2", "STERLING", ts); !errors.Is(err, ErrNotActive) {
		t.Fatalf("suspended join err = %v", err)
	}
	// Reinstatement resumes accrual.
	if _, err := e.Approve("t1", p.ID, "admin", ts); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.AccrueContribution("t1", "m1", 1_000_000, "2025-07", ts); err != nil {
		t.Fatalf("post-reinstatement accrual: %v", err)
	}
	if bal := e.PartnerBalance("t1", p.ID); bal != 25_000 {
		t.Fatalf("balance = %d, want 25000", bal)
	}
}

func TestPartnersListing(t *testing.T) {
	e := NewEngine()
	onboardActive(t, e, "t1", "MTN", "mtn", 100)
	if _, err := e.Onboard("t1", "Access Bank", Bank, "access", 200, "ops", ts); err != nil {
		t.Fatal(err)
	}
	all := e.Partners("t1", "")
	if len(all) != 2 || all[0].Name != "Access Bank" {
		t.Fatalf("list = %+v", all)
	}
	if act := e.Partners("t1", Active); len(act) != 1 || act[0].Name != "MTN" {
		t.Fatalf("active list = %+v", act)
	}
	if other := e.Partners("t2", ""); len(other) != 0 {
		t.Fatalf("tenant isolation violated: %+v", other)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	st := persisttest.New()
	a := NewEngine()
	a.AttachSink(st)
	p := onboardActive(t, a, "t1", "Sterling", "sterling", 250)
	if _, err := a.AttributeJoin("t1", "m1", "STERLING", ts); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.AccrueContribution("t1", "m1", 1_000_000, "2025-06", ts); err != nil {
		t.Fatal(err)
	}

	b := NewEngine()
	snaps, err := st.LoadSnapshots(nil, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Restore(snaps); err != nil {
		t.Fatal(err)
	}
	if got, ok := b.Partner("t1", p.ID); !ok || got.Status != Active || got.ReferralCode != "STERLING" {
		t.Fatalf("restored partner = %+v ok=%v", got, ok)
	}
	// Code uniqueness survives restore.
	if _, err := b.Onboard("t1", "Clone", Bank, "STERLING", 100, "ops", ts); !errors.Is(err, ErrCodeTaken) {
		t.Fatalf("code reuse after restore err = %v", err)
	}
	// Attribution + balance survive; first-write-wins still holds.
	if bal := b.PartnerBalance("t1", p.ID); bal != 25_000 {
		t.Fatalf("restored balance = %d, want 25000", bal)
	}
	att, err := b.AttributeJoin("t1", "m1", "STERLING", ts)
	if err != nil || att.PartnerID != p.ID {
		t.Fatalf("restored attribution = %+v err=%v", att, err)
	}
	// Seq continuation: no ID collisions after restore.
	p2, err := b.Onboard("t1", "MTN", Telco, "mtn", 100, "ops", ts)
	if err != nil || p2.ID == p.ID {
		t.Fatalf("post-restore onboard = %+v err=%v", p2, err)
	}
	ac, _, err := b.AccrueContribution("t1", "m1", 1_000_000, "2025-07", ts)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := b.SettlementReport("t1", p.ID, "2025-06", ts)
	if err != nil || rep.Total != 25_000 || len(rep.Lines) != 1 {
		t.Fatalf("restored settlement = %+v err=%v", rep, err)
	}
	_ = ac
}
