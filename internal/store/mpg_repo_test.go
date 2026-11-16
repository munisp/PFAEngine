package store

// Tranche 15 tests: validators run everywhere; DB round-trips are gated
// on TEST_DATABASE_URL (apply db/migrations/0001_init.sql … 0004_mpg.sql
// first). Run-unique IDs keep the tests idempotent across reruns.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/mpg"
)

func TestValidatePPFContributionAndAssessment(t *testing.T) {
	okC := mpg.Contribution{ID: "ppf-1", TenantID: "t1", Source: mpg.SourceFG, ContributorRef: "FG", Amount: money.NGN(1), Period: "2026-11"}
	if err := validatePPFContribution(okC); err != nil {
		t.Fatalf("valid contribution rejected: %v", err)
	}
	bad := okC
	bad.Amount = 0
	if err := validatePPFContribution(bad); err == nil {
		t.Fatal("zero amount must fail validation")
	}
	bad = okC
	bad.ContributorRef = ""
	if err := validatePPFContribution(bad); err == nil {
		t.Fatal("missing contributor ref must fail validation")
	}
	okA := mpg.Assessment{ID: "mpg-1", TenantID: "t1", RSAID: "r1", Sector: mpg.SectorPrivate, Period: "2026-11", Minimum: mpg.DefaultMinimumMonthly}
	if err := validateAssessment(okA); err != nil {
		t.Fatalf("valid assessment rejected: %v", err)
	}
	badA := okA
	badA.Period = ""
	if err := validateAssessment(badA); err == nil {
		t.Fatal("missing period must fail validation")
	}
}

func TestTranche15MPGRoundTrip(t *testing.T) {
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
	repo := NewMPGRepo(db)

	sfx := fmt.Sprintf("%d", time.Now().UnixNano())
	tenant := "tenant-mpg-" + sfx

	// PPF ledger: FG + industry + one state + one employer.
	in := []mpg.Contribution{
		{ID: "ppf-fg-" + sfx, TenantID: tenant, Source: mpg.SourceFG, ContributorRef: "FG", Amount: money.NGN(200_000_000_00), Period: "2026-11", ReceivedAt: time.Now().UTC()},
		{ID: "ppf-in-" + sfx, TenantID: tenant, Source: mpg.SourceIndustry, ContributorRef: "INDUSTRY", Amount: money.NGN(200_000_000_00), Period: "2026-11", ReceivedAt: time.Now().UTC()},
		{ID: "ppf-st-" + sfx, TenantID: tenant, Source: mpg.SourceState, ContributorRef: "LA", Amount: money.NGN(5_000_000_00), Period: "2026-11", ReceivedAt: time.Now().UTC()},
		{ID: "ppf-em-" + sfx, TenantID: tenant, Source: mpg.SourceEmployer, ContributorRef: "emp-" + sfx, Amount: money.NGN(50_000_00), Period: "2026-11", ReceivedAt: time.Now().UTC()},
	}
	for _, c := range in {
		if err := repo.InsertContribution(ctx, c); err != nil {
			t.Fatalf("insert contribution %s: %v", c.ID, err)
		}
	}
	// WORM: an UPDATE on the ledger must be rejected by the trigger (the
	// closure propagates the trigger error so WithTenant rolls back).
	err = db.WithTenant(ctx, tenant, func(tx *sql.Tx) error {
		_, uerr := tx.ExecContext(ctx,
			`UPDATE ppf_contribution SET amount_kobo = amount_kobo + 1 WHERE tenant_id = $1`,
			tenant)
		return uerr
	})
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("ppf_contribution UPDATE must be rejected (WORM), got %v", err)
	}
	agg, err := repo.PPFAggregates(ctx, tenant)
	if err != nil {
		t.Fatalf("aggregates: %v", err)
	}
	var sum money.Kobo
	for _, v := range agg {
		sum += v
	}
	if sum != money.NGN(405_050_000_00) {
		t.Fatalf("PPF total = %v, want ₦405.05bn", sum)
	}
	funded, err := repo.StateFunded(ctx, tenant, "LA")
	if err != nil || !funded {
		t.Fatalf("LA must be PPF-funded: funded=%v err=%v", funded, err)
	}
	funded, err = repo.StateFunded(ctx, tenant, "KD")
	if err != nil || funded {
		t.Fatalf("KD must NOT be PPF-funded: funded=%v err=%v", funded, err)
	}

	// MPG assessments for one payroll period.
	engine := mpg.NewEngine()
	for _, c := range in {
		if c.Source == mpg.SourceState {
			engine.RecordContribution(tenant, c.Source, c.ContributorRef, c.Amount, c.Period, c.ReceivedAt)
		}
	}
	rows := []mpg.PayrollRow{
		{RSAID: "rsa-a-" + sfx, Sector: mpg.SectorPrivate, MonthlyPension: money.NGN(30000)},
		{RSAID: "rsa-b-" + sfx, Sector: mpg.SectorState, StateCode: "LA", MonthlyPension: money.NGN(50000)},
		{RSAID: "rsa-c-" + sfx, Sector: mpg.SectorState, StateCode: "KD", MonthlyPension: money.NGN(10000)},
	}
	assessments, total := engine.AssessPayroll(tenant, "2026-11", rows, time.Now())
	if total != money.NGN(25000) { // 22,500 + 2,500 + 0 (gated)
		t.Fatalf("engine total top-up = %v, want ₦25,000", total)
	}
	for _, a := range assessments {
		a.ID = a.ID + "-" + sfx
		if err := repo.InsertAssessment(ctx, a); err != nil {
			t.Fatalf("insert assessment %s: %v", a.ID, err)
		}
	}
	got, err := repo.Assessments(ctx, tenant, "2026-11")
	if err != nil {
		t.Fatalf("list assessments: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 assessments, got %d", len(got))
	}
	var eligible, gated int
	for _, a := range got {
		if a.Eligible {
			eligible++
		}
		if a.Gated {
			gated++
		}
	}
	if eligible != 2 || gated != 1 {
		t.Fatalf("eligible=%d gated=%d, want 2/1", eligible, gated)
	}
}
