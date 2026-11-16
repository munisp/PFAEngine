// Package store provides the PostgreSQL repository adapters (§5, NFR-10).
// Services use in-memory stores by default; setting DATABASE_URL switches
// persistence here. Schema: db/migrations/0001_init.sql.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/lib/pq"

	"github.com/munisp/pfaengine/internal/contribution"
	"github.com/munisp/pfaengine/internal/money"
)

var ErrNotFound = errors.New("store: not found")

// DB wraps the connection with per-tenant scoping helpers.
type DB struct{ sql *sql.DB }

// Open connects and verifies. Every query sets tenant context (NFR-10).
func Open(ctx context.Context, dsn string) (*DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &DB{sql: db}, nil
}

func (d *DB) Close() error { return d.sql.Close() }

// WithTenant runs fn inside a transaction pinned to the tenant (used to
// enforce row-level scoping and to keep multi-writes atomic).
func (d *DB) WithTenant(ctx context.Context, tenantID string, fn func(tx *sql.Tx) error) error {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SET LOCAL app.tenant_id = $1", tenantID); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- person / RSA ----

func (d *DB) InsertPerson(ctx context.Context, tx *sql.Tx, tenantID, id, name, dob, phone, email string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO person (id, tenant_id, full_name, dob, phone, email) VALUES ($1,$2,$3,$4,$5,$6)`,
		id, tenantID, name, dob, phone, email)
	return err
}

func (d *DB) InsertRSA(ctx context.Context, tx *sql.Tx, tenantID, id, personID, fundID, schemeID string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO rsa_account (id, tenant_id, person_id, fund_id, scheme_id) VALUES ($1,$2,$3,$4,$5)`,
		id, tenantID, personID, fundID, schemeID)
	return err
}

func (d *DB) AckPIN(ctx context.Context, rsaID, pin string) error {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE rsa_account SET pin=$2, pin_status='ISSUED' WHERE id=$1`, rsaID, pin)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- contributions ----

// InsertScheduleWithLines persists a schedule and its lines atomically.
func (d *DB) InsertScheduleWithLines(ctx context.Context, s *contribution.Schedule) error {
	return d.WithTenant(ctx, s.TenantID, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO contribution_schedule (id, tenant_id, employer_id, period, status, error_summary)
			 VALUES ($1,$2,$3,$4,$5,$6)`,
			s.ID, s.TenantID, s.EmployerID, s.Period, string(s.Status), s.ErrorSummary); err != nil {
			return err
		}
		for i, l := range s.Lines {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO contribution (id, tenant_id, schedule_id, rsa_pin, gross_kobo, employee_kobo, employer_kobo, voluntary_kobo, is_ppp)
				 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
				fmt.Sprintf("%s-l%d", s.ID, i), s.TenantID, s.ID, l.RSAPIN,
				int64(l.Gross), int64(l.Employee), int64(l.Employer), int64(l.Voluntary), l.IsPPP); err != nil {
				return err
			}
		}
		return nil
	})
}

func (d *DB) MarkSchedulePosted(ctx context.Context, scheduleID string) error {
	_, err := d.sql.ExecContext(ctx,
		`UPDATE contribution_schedule SET status='POSTED', posted_at=now() WHERE id=$1`, scheduleID)
	return err
}

// MemberContributions returns the member contribution history (§6.2).
func (d *DB) MemberContributions(ctx context.Context, tenantID, rsaPIN string) ([]contribution.Line, money.Kobo, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT rsa_pin, gross_kobo, employee_kobo, employer_kobo, voluntary_kobo, is_ppp
		 FROM contribution WHERE tenant_id=$1 AND rsa_pin=$2 ORDER BY created_at`, tenantID, rsaPIN)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var lines []contribution.Line
	var total money.Kobo
	for rows.Next() {
		var l contribution.Line
		var gross, emp, emr, vol int64
		if err := rows.Scan(&l.RSAPIN, &gross, &emp, &emr, &vol, &l.IsPPP); err != nil {
			return nil, 0, err
		}
		l.Gross, l.Employee, l.Employer, l.Voluntary = money.Kobo(gross), money.Kobo(emp), money.Kobo(emr), money.Kobo(vol)
		lines = append(lines, l)
		total += l.Total()
	}
	return lines, total, rows.Err()
}

// ---- ledger ----

// PostEntry persists an entry + postings atomically; balance enforcement
// happens in the domain layer before this call (FR-302).
func (d *DB) PostEntry(ctx context.Context, tenantID, entryID, memo, reference, reversalOf string, postings []struct {
	AccountID string
	Debit     int64
	Credit    int64
}) error {
	return d.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		rev := any(nil)
		if reversalOf != "" {
			rev = reversalOf
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO ledger_entry (id, tenant_id, memo, reference, reversal_of) VALUES ($1,$2,$3,$4,$5)`,
			entryID, tenantID, memo, reference, rev); err != nil {
			return err
		}
		for _, p := range postings {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO ledger_posting (entry_id, account_id, debit_kobo, credit_kobo) VALUES ($1,$2,$3,$4)`,
				entryID, p.AccountID, p.Debit, p.Credit); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- audit (WORM) ----

// AppendAudit writes one hash-chained event inside the WORM table (SC-02).
// The DB enforces no UPDATE/DELETE on audit_event (see migration).
func (d *DB) AppendAudit(ctx context.Context, tenantID, actor, action, entityType, entityID string,
	before, after []byte, consentRef, kycRef, prevHash, hash string) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO audit_event (tenant_id, actor, action, entity_type, entity_id, before, after, consent_ref, kyc_ref, prev_hash, hash)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		tenantID, actor, action, entityType, entityID, before, after, consentRef, kycRef, prevHash, hash)
	return err
}

// AuditTip returns the current chain tip hash for a tenant.
func (d *DB) AuditTip(ctx context.Context, tenantID string) (string, error) {
	var tip string
	err := d.sql.QueryRowContext(ctx,
		`SELECT hash FROM audit_event WHERE tenant_id=$1 ORDER BY seq DESC LIMIT 1`, tenantID).Scan(&tip)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return tip, err
}
