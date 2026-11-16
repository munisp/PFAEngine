package store

import (
	"context"
	"os"
	"testing"

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

	s := &contribution.Schedule{
		ID: "sched-test-1", TenantID: "t1", EmployerID: "emp1", Period: "2026-11",
		Status: contribution.SchedValidated,
		Lines:  []contribution.Line{contribution.SplitLine("PEN100", money.NGN(800000), 0, false)},
	}
	if err := db.InsertScheduleWithLines(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkSchedulePosted(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	lines, total, err := db.MemberContributions(ctx, "t1", "PEN100")
	if err != nil || len(lines) != 1 || total != money.NGN(144000) {
		t.Fatalf("got %v lines, %s total, err %v", len(lines), total.Naira(), err)
	}
}
