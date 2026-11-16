package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/contribution"
	"github.com/munisp/pfaengine/internal/money"
)

// Integration test — runs only when TEST_DATABASE_URL is set
// (docker compose up postgres; psql -f db/migrations/0001_init.sql).
func TestPostgresRoundTrip(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Unique run suffix keeps the integration test idempotent across
	// repeated runs against a persistent database.
	run := fmt.Sprintf("%d", time.Now().UnixNano())
	tenant := "t1-" + run

	// contribution_schedule.employer_id references employer(id) — seed the
	// parent row first so the FK holds on a live database.
	employerID := "emp-" + run
	if err := db.WithTenant(ctx, tenant, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO employer (id, tenant_id, rc_number, name) VALUES ($1,$2,'RC0001','Test Employer')
			 ON CONFLICT (id) DO NOTHING`, employerID, tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	memberPIN := "PEN" + run
	s := &contribution.Schedule{
		ID: "sched-" + run, TenantID: tenant, EmployerID: employerID, Period: "2026-11",
		Status: contribution.SchedValidated,
		Lines:  []contribution.Line{contribution.SplitLine(memberPIN, money.NGN(800000), 0, false)},
	}
	if err := db.InsertScheduleWithLines(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkSchedulePosted(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	lines, total, err := db.MemberContributions(ctx, tenant, memberPIN)
	if err != nil || len(lines) != 1 || total != money.NGN(144000) {
		t.Fatalf("got %v lines, %s total, err %v", len(lines), total.Naira(), err)
	}
}
