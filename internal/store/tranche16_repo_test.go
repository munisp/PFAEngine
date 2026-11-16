package store

// Tranche 16 tests: validators run everywhere; DB round-trips are gated
// on TEST_DATABASE_URL (apply db/migrations/0001_init.sql …
// 0006_invmandate.sql first). Run-unique IDs keep tests idempotent.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/invmandate"
	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/pencare"
)

func TestValidatePenCareAndMandate(t *testing.T) {
	okEn := pencare.Enrollee{ID: "pc-enr-1", TenantID: "t1", RSAID: "r1", Floor: money.NGN(52500), Status: pencare.EnrolleeActive}
	if err := validateEnrollee(okEn); err != nil {
		t.Fatalf("valid enrollee rejected: %v", err)
	}
	bad := okEn
	bad.Floor = 0
	if err := validateEnrollee(bad); err == nil {
		t.Fatal("zero floor must fail validation")
	}
	okM := invmandate.Mandate{ID: "im-1", TenantID: "t1", Name: "X", AssetClass: invmandate.ClassInfraFund,
		Currency: "USD", TargetMinor: 100, CapBps: 500, Status: invmandate.MandateOpen}
	if err := validateMandate(okM); err != nil {
		t.Fatalf("valid mandate rejected: %v", err)
	}
	badM := okM
	badM.CapBps = 20000
	if err := validateMandate(badM); err == nil {
		t.Fatal("cap > 100% must fail validation")
	}
}

func TestTranche16PenCareRoundTrip(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	repo := NewPenCareRepo(db)

	sfx := fmt.Sprintf("%d", time.Now().UnixNano())
	tenant := "tenant-pc-" + sfx
	now := time.Now().UTC()

	// Fund: ₦7bn industry pool + ₦500m PFA.
	for i, fc := range []pencare.FundContribution{
		{ID: "pcf-" + sfx + "-1", TenantID: tenant, Source: pencare.SourceIndustry, ContributorRef: "penop-pool", Amount: money.NGN(7_000_000_000), Period: "2026-11", ReceivedAt: now},
		{ID: "pcf-" + sfx + "-2", TenantID: tenant, Source: pencare.SourcePFA, ContributorRef: "pfa-001", Amount: money.NGN(500_000_000), Period: "2026-11", ReceivedAt: now},
	} {
		if err := repo.InsertFundContribution(ctx, fc); err != nil {
			t.Fatalf("fund %d: %v", i, err)
		}
	}
	agg, err := repo.FundAggregates(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if agg[pencare.SourceIndustry] != money.NGN(7_000_000_000) || agg[pencare.SourcePFA] != money.NGN(500_000_000) {
		t.Fatalf("aggregates: %v", agg)
	}

	// WORM: updating a fund contribution must fail.
	err = db.WithTenant(ctx, tenant, func(tx *sql.Tx) error {
		_, uerr := tx.ExecContext(ctx,
			`UPDATE pencare_fund_contribution SET amount_kobo = 1 WHERE tenant_id = $1 AND id = $2`,
			tenant, "pcf-"+sfx+"-1")
		return uerr
	})
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("WORM update must be rejected, got %v", err)
	}

	// Enrollee + card lifecycle + premium.
	en := pencare.Enrollee{ID: "pce-" + sfx, TenantID: tenant, RSAID: "RSA-" + sfx,
		MonthlyPension: money.NGN(20000), Floor: money.NGN(52500), Status: pencare.EnrolleeActive, EnrolledAt: now}
	if err := repo.InsertEnrollee(ctx, en); err != nil {
		t.Fatalf("enrollee: %v", err)
	}
	c := pencare.Card{ID: "pcc-" + sfx, TenantID: tenant, EnrolleeID: en.ID, Status: pencare.CardRequested, RequestedAt: now}
	if err := repo.InsertCard(ctx, c); err != nil {
		t.Fatalf("card: %v", err)
	}
	c.Status = pencare.CardActive
	c.CardNumber = "PC-" + sfx
	if err := repo.UpdateCard(ctx, c); err != nil {
		t.Fatalf("card update: %v", err)
	}
	p := pencare.Premium{ID: "pcp-" + sfx, TenantID: tenant, EnrolleeID: en.ID,
		InsurerRef: "HMO-X", Amount: money.NGN(4500), Period: "2026-11", PaidAt: now}
	if err := repo.InsertPremium(ctx, p); err != nil {
		t.Fatalf("premium: %v", err)
	}

	ens, err := repo.Enrollees(ctx, tenant)
	if err != nil || len(ens) != 1 || ens[0].RSAID != en.RSAID {
		t.Fatalf("enrollees: %v %v", ens, err)
	}
	cs, err := repo.Cards(ctx, tenant, en.ID)
	if err != nil || len(cs) != 1 || cs[0].Status != pencare.CardActive {
		t.Fatalf("cards: %v %v", cs, err)
	}
	ps, err := repo.Premiums(ctx, tenant, en.ID)
	if err != nil || len(ps) != 1 || ps[0].Amount != money.NGN(4500) {
		t.Fatalf("premiums: %v %v", ps, err)
	}
	// Status transition persists.
	if err := repo.UpdateEnrolleeStatus(ctx, tenant, en.ID, pencare.EnrolleeSuspended); err != nil {
		t.Fatal(err)
	}
	ens, _ = repo.Enrollees(ctx, tenant)
	if ens[0].Status != pencare.EnrolleeSuspended {
		t.Fatalf("status: %+v", ens[0])
	}
	// Premium WORM.
	err = db.WithTenant(ctx, tenant, func(tx *sql.Tx) error {
		_, uerr := tx.ExecContext(ctx,
			`DELETE FROM pencare_premium WHERE tenant_id = $1 AND id = $2`, tenant, p.ID)
		return uerr
	})
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("premium WORM delete must be rejected, got %v", err)
	}
}

func TestTranche16InvMandateRoundTrip(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	repo := NewInvMandateRepo(db)

	sfx := fmt.Sprintf("%d", time.Now().UnixNano())
	tenant := "tenant-im-" + sfx
	now := time.Now().UTC()

	m := invmandate.Mandate{ID: "im-" + sfx, TenantID: tenant,
		Name: "Pension-Backed Infrastructure Platform", AssetClass: invmandate.ClassInfraFund,
		Partner: "FSD Africa", Currency: "USD", TargetMinor: 750_000_000_00,
		CapBps: 500, Status: invmandate.MandateOpen, CreatedAt: now}
	if err := repo.InsertMandate(ctx, m); err != nil {
		t.Fatalf("mandate: %v", err)
	}
	c := invmandate.Commitment{ID: "ic-" + sfx, TenantID: tenant, MandateID: m.ID,
		FundID: "II", AmountMinor: 5_000_000, Currency: "USD", Ref: "ic-2026-041", CommittedAt: now}
	if err := repo.InsertCommitment(ctx, c); err != nil {
		t.Fatalf("commitment: %v", err)
	}
	d := invmandate.Deployment{ID: "idp-" + sfx, TenantID: tenant, MandateID: m.ID,
		AmountMinor: 3_000_000, Currency: "USD", Vehicle: "spv-lagos-road", DeployedAt: now}
	if err := repo.InsertDeployment(ctx, d); err != nil {
		t.Fatalf("deployment: %v", err)
	}

	ms, err := repo.Mandates(ctx, tenant)
	if err != nil || len(ms) != 1 || ms[0].Partner != "FSD Africa" {
		t.Fatalf("mandates: %v %v", ms, err)
	}
	cs, err := repo.Commitments(ctx, tenant, m.ID)
	if err != nil || len(cs) != 1 || cs[0].AmountMinor != 5_000_000 {
		t.Fatalf("commitments: %v %v", cs, err)
	}
	ds, err := repo.Deployments(ctx, tenant, m.ID)
	if err != nil || len(ds) != 1 || ds[0].Vehicle != "spv-lagos-road" {
		t.Fatalf("deployments: %v %v", ds, err)
	}

	// WORM on both ledgers.
	err = db.WithTenant(ctx, tenant, func(tx *sql.Tx) error {
		_, uerr := tx.ExecContext(ctx,
			`UPDATE inv_commitment SET amount_minor = 1 WHERE tenant_id = $1 AND id = $2`, tenant, c.ID)
		return uerr
	})
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("commitment WORM must reject update, got %v", err)
	}
	err = db.WithTenant(ctx, tenant, func(tx *sql.Tx) error {
		_, uerr := tx.ExecContext(ctx,
			`DELETE FROM inv_deployment WHERE tenant_id = $1 AND id = $2`, tenant, d.ID)
		return uerr
	})
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("deployment WORM must reject delete, got %v", err)
	}

	// Mandate lifecycle persists.
	if err := repo.UpdateMandateStatus(ctx, tenant, m.ID, invmandate.MandateClosed); err != nil {
		t.Fatal(err)
	}
	ms, _ = repo.Mandates(ctx, tenant)
	if ms[0].Status != invmandate.MandateClosed {
		t.Fatalf("mandate status: %+v", ms[0])
	}
}
