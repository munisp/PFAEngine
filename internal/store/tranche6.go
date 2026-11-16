package store

// Tranche 6: persistence for member registry, KYC evidence, statement
// metadata, and regulatory report artifacts. All tables are tenant-scoped;
// kyc_record and statement_record are WORM (see db/migrations/0002_tranche6.sql).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ---- member registry ----

// MemberRow mirrors the member table: the PII-minimal member registry
// record. NIN/BVN are stored as salted hashes only (FR-101/FR-102).
type MemberRow struct {
	ID        string
	PIN       string
	NINHash   string
	BVNHash   string
	Status    string // ACTIVE | SUSPENDED | CLOSED
	FundCode  string
	CreatedAt time.Time
}

// ValidateMemberRow enforces the invariants the DB relies on; it is pure
// and unit-testable without a database.
func ValidateMemberRow(m MemberRow) error {
	switch {
	case m.ID == "":
		return errors.New("store: member id required")
	case m.PIN == "":
		return errors.New("store: member PIN required")
	case m.Status == "":
		return errors.New("store: member status required")
	case m.FundCode == "":
		return errors.New("store: member fund code required")
	}
	return nil
}

// UpsertMember inserts or updates the member registry row for the tenant.
func (d *DB) UpsertMember(ctx context.Context, tenantID string, m MemberRow) error {
	if err := ValidateMemberRow(m); err != nil {
		return err
	}
	return d.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO member (id, tenant_id, pin, nin_hash, bvn_hash, status, fund_code)
			 VALUES ($1,$2,$3,$4,$5,$6,$7)
			 ON CONFLICT (id) DO UPDATE SET
			   pin=EXCLUDED.pin, nin_hash=EXCLUDED.nin_hash, bvn_hash=EXCLUDED.bvn_hash,
			   status=EXCLUDED.status, fund_code=EXCLUDED.fund_code`,
			m.ID, tenantID, m.PIN, m.NINHash, m.BVNHash, m.Status, m.FundCode)
		return err
	})
}

// GetMember loads one member row; ErrNotFound when absent or cross-tenant.
func (d *DB) GetMember(ctx context.Context, tenantID, memberID string) (MemberRow, error) {
	var m MemberRow
	err := d.sql.QueryRowContext(ctx,
		`SELECT id, pin, nin_hash, bvn_hash, status, fund_code, created_at
		 FROM member WHERE tenant_id=$1 AND id=$2`, tenantID, memberID).
		Scan(&m.ID, &m.PIN, &m.NINHash, &m.BVNHash, &m.Status, &m.FundCode, &m.CreatedAt)
	if err == sql.ErrNoRows {
		return MemberRow{}, ErrNotFound
	}
	return m, err
}

// ---- KYC evidence (WORM) ----

// KYC kinds recorded in kyc_record.
const (
	KYCKindEnroll   = "ENROLL"
	KYCKindIAmAlive = "IAM_ALIVE"
)

// KYCRow mirrors the kyc_record WORM table. Score is basis points
// (0..10000; 0.95 liveness = 9500) — no floats (DA-03).
type KYCRow struct {
	ID       string
	MemberID string
	Kind     string // ENROLL | IAM_ALIVE
	Score    int    // basis points
	Outcome  string // PASS | MANUAL_REVIEW | FAIL
	At       time.Time
}

// ValidateKYCRow enforces kyc_record invariants (pure, unit-testable).
func ValidateKYCRow(r KYCRow) error {
	switch {
	case r.ID == "":
		return errors.New("store: kyc record id required")
	case r.MemberID == "":
		return errors.New("store: kyc member id required")
	case r.Kind != KYCKindEnroll && r.Kind != KYCKindIAmAlive:
		return fmt.Errorf("store: invalid kyc kind %q", r.Kind)
	case r.Score < 0 || r.Score > 10000:
		return fmt.Errorf("store: kyc score %d outside 0..10000 bps", r.Score)
	case r.Outcome == "":
		return errors.New("store: kyc outcome required")
	}
	return nil
}

// InsertKYCRecord appends one KYC evidence row. The table is WORM:
// corrections are new records, never updates (matches 0001 audit style).
func (d *DB) InsertKYCRecord(ctx context.Context, tenantID string, r KYCRow) error {
	if err := ValidateKYCRow(r); err != nil {
		return err
	}
	return d.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO kyc_record (id, tenant_id, member_id, kind, score_bps, outcome, at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			r.ID, tenantID, r.MemberID, r.Kind, r.Score, r.Outcome, r.At)
		return err
	})
}

// LatestKYC returns the most recent KYC record of a kind for a member;
// ErrNotFound when none.
func (d *DB) LatestKYC(ctx context.Context, tenantID, memberID, kind string) (KYCRow, error) {
	var r KYCRow
	err := d.sql.QueryRowContext(ctx,
		`SELECT id, member_id, kind, score_bps, outcome, at
		 FROM kyc_record WHERE tenant_id=$1 AND member_id=$2 AND kind=$3
		 ORDER BY at DESC LIMIT 1`, tenantID, memberID, kind).
		Scan(&r.ID, &r.MemberID, &r.Kind, &r.Score, &r.Outcome, &r.At)
	if err == sql.ErrNoRows {
		return KYCRow{}, ErrNotFound
	}
	return r, err
}

// ---- statement metadata (WORM) ----

// StatementRow mirrors the statement_record WORM table: generation
// metadata only; the PDF/payload itself lives in object storage.
type StatementRow struct {
	ID             string
	MemberID       string
	Period         string // YYYY-MM or YYYY-Qn
	VerifyCodeHash string // hash of the HMAC verify code (FR-501)
	GeneratedAt    time.Time
}

// ValidateStatementRow enforces statement_record invariants (pure).
func ValidateStatementRow(r StatementRow) error {
	switch {
	case r.ID == "":
		return errors.New("store: statement id required")
	case r.MemberID == "":
		return errors.New("store: statement member id required")
	case r.Period == "":
		return errors.New("store: statement period required")
	case r.VerifyCodeHash == "":
		return errors.New("store: statement verify code hash required")
	}
	return nil
}

// InsertStatementRecord appends statement generation metadata (WORM).
func (d *DB) InsertStatementRecord(ctx context.Context, tenantID string, r StatementRow) error {
	if err := ValidateStatementRow(r); err != nil {
		return err
	}
	return d.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO statement_record (id, tenant_id, member_id, period, verify_code_hash, generated_at)
			 VALUES ($1,$2,$3,$4,$5,$6)`,
			r.ID, tenantID, r.MemberID, r.Period, r.VerifyCodeHash, r.GeneratedAt)
		return err
	})
}

// ListStatements returns a member's statement metadata, newest first.
func (d *DB) ListStatements(ctx context.Context, tenantID, memberID string) ([]StatementRow, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT id, member_id, period, verify_code_hash, generated_at
		 FROM statement_record WHERE tenant_id=$1 AND member_id=$2
		 ORDER BY generated_at DESC`, tenantID, memberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatementRow
	for rows.Next() {
		var r StatementRow
		if err := rows.Scan(&r.ID, &r.MemberID, &r.Period, &r.VerifyCodeHash, &r.GeneratedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- regulatory report artifacts ----

// ReportArtifactRow mirrors report_artifact: metadata for a generated
// regulatory return (FR-502); the payload lives in object storage.
type ReportArtifactRow struct {
	ReportType  string // e.g. PENCOM_QUARTERLY, RMAS_MONTHLY
	Period      string
	PayloadHash string
	GeneratedAt time.Time
}

// ValidateReportArtifactRow enforces report_artifact invariants (pure).
func ValidateReportArtifactRow(r ReportArtifactRow) error {
	switch {
	case r.ReportType == "":
		return errors.New("store: report type required")
	case r.Period == "":
		return errors.New("store: report period required")
	case r.PayloadHash == "":
		return errors.New("store: report payload hash required")
	}
	return nil
}

// UpsertReportArtifact records (or refreshes) the artifact for
// (tenant, report_type, period) — regeneration replaces the hash.
func (d *DB) UpsertReportArtifact(ctx context.Context, tenantID string, r ReportArtifactRow) error {
	if err := ValidateReportArtifactRow(r); err != nil {
		return err
	}
	return d.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO report_artifact (tenant_id, report_type, period, payload_hash, generated_at)
			 VALUES ($1,$2,$3,$4,$5)
			 ON CONFLICT (tenant_id, report_type, period) DO UPDATE SET
			   payload_hash=EXCLUDED.payload_hash, generated_at=EXCLUDED.generated_at`,
			tenantID, r.ReportType, r.Period, r.PayloadHash, r.GeneratedAt)
		return err
	})
}

// GetReportArtifact loads one artifact; ErrNotFound when absent.
func (d *DB) GetReportArtifact(ctx context.Context, tenantID, reportType, period string) (ReportArtifactRow, error) {
	var r ReportArtifactRow
	err := d.sql.QueryRowContext(ctx,
		`SELECT report_type, period, payload_hash, generated_at
		 FROM report_artifact WHERE tenant_id=$1 AND report_type=$2 AND period=$3`,
		tenantID, reportType, period).
		Scan(&r.ReportType, &r.Period, &r.PayloadHash, &r.GeneratedAt)
	if err == sql.ErrNoRows {
		return ReportArtifactRow{}, ErrNotFound
	}
	return r, err
}
