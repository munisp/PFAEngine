package store

// Tranche 6 tests: pure unit tests for row validators run everywhere;
// DB round-trips are gated on TEST_DATABASE_URL (apply
// db/migrations/0001_init.sql then 0002_tranche6.sql first).

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestValidateMemberRow(t *testing.T) {
	valid := MemberRow{ID: "m1", PIN: "PEN100", Status: "ACTIVE", FundCode: "FUND_II"}
	cases := []struct {
		name    string
		row     MemberRow
		wantErr bool
	}{
		{"valid", valid, false},
		{"missing id", MemberRow{PIN: "P", Status: "ACTIVE", FundCode: "FUND_II"}, true},
		{"missing pin", MemberRow{ID: "m", Status: "ACTIVE", FundCode: "FUND_II"}, true},
		{"missing status", MemberRow{ID: "m", PIN: "P", FundCode: "FUND_II"}, true},
		{"missing fund", MemberRow{ID: "m", PIN: "P", Status: "ACTIVE"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateMemberRow(tc.row); (err != nil) != tc.wantErr {
				t.Fatalf("ValidateMemberRow(%+v) err=%v wantErr=%v", tc.row, err, tc.wantErr)
			}
		})
	}
}

func TestValidateKYCRow(t *testing.T) {
	cases := []struct {
		name    string
		row     KYCRow
		wantErr bool
	}{
		{"valid enroll", KYCRow{ID: "k1", MemberID: "m1", Kind: KYCKindEnroll, Score: 9500, Outcome: "PASS"}, false},
		{"valid i-am-alive", KYCRow{ID: "k2", MemberID: "m1", Kind: KYCKindIAmAlive, Score: 0, Outcome: "FAIL"}, false},
		{"missing id", KYCRow{MemberID: "m1", Kind: KYCKindEnroll, Score: 100, Outcome: "PASS"}, true},
		{"missing member", KYCRow{ID: "k", Kind: KYCKindEnroll, Score: 100, Outcome: "PASS"}, true},
		{"bad kind", KYCRow{ID: "k", MemberID: "m", Kind: "SELFIE", Score: 100, Outcome: "PASS"}, true},
		{"score negative", KYCRow{ID: "k", MemberID: "m", Kind: KYCKindEnroll, Score: -1, Outcome: "PASS"}, true},
		{"score over 10000", KYCRow{ID: "k", MemberID: "m", Kind: KYCKindEnroll, Score: 10001, Outcome: "PASS"}, true},
		{"missing outcome", KYCRow{ID: "k", MemberID: "m", Kind: KYCKindEnroll, Score: 100}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateKYCRow(tc.row); (err != nil) != tc.wantErr {
				t.Fatalf("ValidateKYCRow(%+v) err=%v wantErr=%v", tc.row, err, tc.wantErr)
			}
		})
	}
}

func TestValidateStatementRow(t *testing.T) {
	cases := []struct {
		name    string
		row     StatementRow
		wantErr bool
	}{
		{"valid", StatementRow{ID: "s1", MemberID: "m1", Period: "2026-11", VerifyCodeHash: "abc"}, false},
		{"missing id", StatementRow{MemberID: "m1", Period: "2026-11", VerifyCodeHash: "abc"}, true},
		{"missing member", StatementRow{ID: "s1", Period: "2026-11", VerifyCodeHash: "abc"}, true},
		{"missing period", StatementRow{ID: "s1", MemberID: "m1", VerifyCodeHash: "abc"}, true},
		{"missing hash", StatementRow{ID: "s1", MemberID: "m1", Period: "2026-11"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateStatementRow(tc.row); (err != nil) != tc.wantErr {
				t.Fatalf("ValidateStatementRow(%+v) err=%v wantErr=%v", tc.row, err, tc.wantErr)
			}
		})
	}
}

func TestValidateReportArtifactRow(t *testing.T) {
	cases := []struct {
		name    string
		row     ReportArtifactRow
		wantErr bool
	}{
		{"valid", ReportArtifactRow{ReportType: "PENCOM_QUARTERLY", Period: "2026-Q4", PayloadHash: "h"}, false},
		{"missing type", ReportArtifactRow{Period: "2026-Q4", PayloadHash: "h"}, true},
		{"missing period", ReportArtifactRow{ReportType: "RMAS_MONTHLY", PayloadHash: "h"}, true},
		{"missing hash", ReportArtifactRow{ReportType: "RMAS_MONTHLY", Period: "2026-11"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateReportArtifactRow(tc.row); (err != nil) != tc.wantErr {
				t.Fatalf("ValidateReportArtifactRow(%+v) err=%v wantErr=%v", tc.row, err, tc.wantErr)
			}
		})
	}
}

// Integration test — runs only when TEST_DATABASE_URL is set.
func TestTranche6RoundTrip(t *testing.T) {
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

	tenant, memberID := "t6", "member-t6-1"
	m := MemberRow{ID: memberID, PIN: "PEN600", NINHash: "nin-h", BVNHash: "bvn-h", Status: "ACTIVE", FundCode: "FUND_II"}
	if err := db.UpsertMember(ctx, tenant, m); err != nil {
		t.Fatal(err)
	}
	// Upsert again with a new status to exercise the conflict path.
	m.Status = "SUSPENDED"
	if err := db.UpsertMember(ctx, tenant, m); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetMember(ctx, tenant, memberID)
	if err != nil || got.Status != "SUSPENDED" || got.PIN != "PEN600" {
		t.Fatalf("GetMember = %+v, err %v", got, err)
	}
	if _, err := db.GetMember(ctx, tenant, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	now := time.Now().UTC()
	old := KYCRow{ID: "kyc-t6-1", MemberID: memberID, Kind: KYCKindIAmAlive, Score: 7000, Outcome: "MANUAL_REVIEW", At: now.Add(-time.Hour)}
	latest := KYCRow{ID: "kyc-t6-2", MemberID: memberID, Kind: KYCKindIAmAlive, Score: 9500, Outcome: "PASS", At: now}
	for _, r := range []KYCRow{old, latest} {
		if err := db.InsertKYCRecord(ctx, tenant, r); err != nil {
			t.Fatal(err)
		}
	}
	kr, err := db.LatestKYC(ctx, tenant, memberID, KYCKindIAmAlive)
	if err != nil || kr.ID != latest.ID || kr.Score != 9500 {
		t.Fatalf("LatestKYC = %+v, err %v", kr, err)
	}
	if _, err := db.LatestKYC(ctx, tenant, memberID, KYCKindEnroll); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	s1 := StatementRow{ID: "stmt-t6-1", MemberID: memberID, Period: "2026-10", VerifyCodeHash: "h1", GeneratedAt: now.Add(-time.Hour)}
	s2 := StatementRow{ID: "stmt-t6-2", MemberID: memberID, Period: "2026-11", VerifyCodeHash: "h2", GeneratedAt: now}
	for _, s := range []StatementRow{s1, s2} {
		if err := db.InsertStatementRecord(ctx, tenant, s); err != nil {
			t.Fatal(err)
		}
	}
	stmts, err := db.ListStatements(ctx, tenant, memberID)
	if err != nil || len(stmts) != 2 || stmts[0].Period != "2026-11" {
		t.Fatalf("ListStatements = %+v, err %v", stmts, err)
	}

	art := ReportArtifactRow{ReportType: "PENCOM_QUARTERLY", Period: "2026-Q4", PayloadHash: "ph1", GeneratedAt: now}
	if err := db.UpsertReportArtifact(ctx, tenant, art); err != nil {
		t.Fatal(err)
	}
	art.PayloadHash = "ph2"
	if err := db.UpsertReportArtifact(ctx, tenant, art); err != nil {
		t.Fatal(err)
	}
	ga, err := db.GetReportArtifact(ctx, tenant, "PENCOM_QUARTERLY", "2026-Q4")
	if err != nil || ga.PayloadHash != "ph2" {
		t.Fatalf("GetReportArtifact = %+v, err %v", ga, err)
	}

	// Tenant isolation: another tenant sees nothing.
	if _, err := db.GetMember(ctx, "other-tenant", memberID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant read should be ErrNotFound, got %v", err)
	}
}
