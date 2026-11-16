package store

// Tranche 15: Postgres repository for the Minimum Pension Guarantee (MPG)
// engine — PPF contribution ledger (WORM, append-only) and MPG top-up
// assessments (see db/migrations/0004_mpg.sql; PRA 2014 s.84, PenCom
// ₦52,500 MPG directive November 2025).

import (
	"context"
	"database/sql"
	"errors"

	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/mpg"
)

// MPGRepo persists PPF contributions and MPG assessments in Postgres.
// All queries are tenant-scoped via WithTenant (NFR-10).
type MPGRepo struct{ db *DB }

// NewMPGRepo returns the Postgres-backed MPG repository.
func NewMPGRepo(db *DB) *MPGRepo { return &MPGRepo{db: db} }

func validatePPFContribution(c mpg.Contribution) error {
	switch {
	case c.ID == "":
		return errors.New("store: ppf contribution id required")
	case c.TenantID == "":
		return errors.New("store: ppf contribution tenant id required")
	case c.Source == "":
		return errors.New("store: ppf contribution source required")
	case c.ContributorRef == "":
		return errors.New("store: ppf contributor reference required")
	case c.Amount <= 0:
		return errors.New("store: ppf contribution amount must be positive")
	case c.Period == "":
		return errors.New("store: ppf contribution period required")
	}
	return nil
}

// InsertContribution appends a PPF receipt. The table is WORM — updates
// and deletes are rejected by trigger; corrections are reversal entries.
func (r *MPGRepo) InsertContribution(ctx context.Context, c mpg.Contribution) error {
	if err := validatePPFContribution(c); err != nil {
		return err
	}
	return r.db.WithTenant(ctx, c.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO ppf_contribution
			  (id, tenant_id, source, contributor_ref, amount_kobo, period, received_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			c.ID, c.TenantID, string(c.Source), c.ContributorRef,
			int64(c.Amount), c.Period, c.ReceivedAt)
		return err
	})
}

// Contributions lists the tenant's PPF receipts, oldest first.
func (r *MPGRepo) Contributions(ctx context.Context, tenantID string) ([]mpg.Contribution, error) {
	var out []mpg.Contribution
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id, tenant_id, source, contributor_ref, amount_kobo, period, received_at
			FROM ppf_contribution WHERE tenant_id = $1
			ORDER BY received_at, id`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c mpg.Contribution
			var src string
			if err := rows.Scan(&c.ID, &c.TenantID, &src, &c.ContributorRef,
				(*int64)(&c.Amount), &c.Period, &c.ReceivedAt); err != nil {
				return err
			}
			c.Source = mpg.PPFSource(src)
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// PPFAggregates returns the tenant's PPF receipts grouped by source.
func (r *MPGRepo) PPFAggregates(ctx context.Context, tenantID string) (map[mpg.PPFSource]money.Kobo, error) {
	out := map[mpg.PPFSource]money.Kobo{}
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT source, COALESCE(SUM(amount_kobo), 0)
			FROM ppf_contribution WHERE tenant_id = $1 GROUP BY source`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var src string
			var total int64
			if err := rows.Scan(&src, &total); err != nil {
				return err
			}
			out[mpg.PPFSource(src)] = money.Kobo(total)
		}
		return rows.Err()
	})
	return out, err
}

// StateFunded reports whether a state has any PPF contribution on record
// (the access precondition for STATE-sector MPG top-ups).
func (r *MPGRepo) StateFunded(ctx context.Context, tenantID, stateCode string) (bool, error) {
	var funded bool
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT EXISTS(
			  SELECT 1 FROM ppf_contribution
			  WHERE tenant_id = $1 AND source = 'STATE' AND contributor_ref = $2)`,
			tenantID, stateCode).Scan(&funded)
	})
	return funded, err
}

func validateAssessment(a mpg.Assessment) error {
	switch {
	case a.ID == "":
		return errors.New("store: mpg assessment id required")
	case a.TenantID == "":
		return errors.New("store: mpg assessment tenant id required")
	case a.RSAID == "":
		return errors.New("store: mpg assessment rsa id required")
	case a.Sector == "":
		return errors.New("store: mpg assessment sector required")
	case a.Period == "":
		return errors.New("store: mpg assessment period required")
	case a.Minimum <= 0:
		return errors.New("store: mpg assessment minimum must be positive")
	}
	return nil
}

// InsertAssessment persists one MPG assessment for a payroll period.
func (r *MPGRepo) InsertAssessment(ctx context.Context, a mpg.Assessment) error {
	if err := validateAssessment(a); err != nil {
		return err
	}
	return r.db.WithTenant(ctx, a.TenantID, func(tx *sql.Tx) error {
		var state *string
		if a.StateCode != "" {
			state = &a.StateCode
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO mpg_assessment
			  (id, tenant_id, rsa_id, sector, state_code, period,
			   computed_pension_kobo, minimum_kobo, topup_kobo, eligible, gated, assessed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			a.ID, a.TenantID, a.RSAID, string(a.Sector), state, a.Period,
			int64(a.ComputedPension), int64(a.Minimum), int64(a.TopUp),
			a.Eligible, a.Gated, a.AssessedAt)
		return err
	})
}

// Assessments returns the tenant's MPG assessments for one payroll period.
func (r *MPGRepo) Assessments(ctx context.Context, tenantID, period string) ([]mpg.Assessment, error) {
	var out []mpg.Assessment
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id, tenant_id, rsa_id, sector, COALESCE(state_code, ''),
			       period, computed_pension_kobo, minimum_kobo, topup_kobo,
			       eligible, gated, assessed_at
			FROM mpg_assessment WHERE tenant_id = $1 AND period = $2
			ORDER BY id`, tenantID, period)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a mpg.Assessment
			var sector string
			if err := rows.Scan(&a.ID, &a.TenantID, &a.RSAID, &sector, &a.StateCode,
				&a.Period, (*int64)(&a.ComputedPension), (*int64)(&a.Minimum),
				(*int64)(&a.TopUp), &a.Eligible, &a.Gated, &a.AssessedAt); err != nil {
				return err
			}
			a.Sector = mpg.Sector(sector)
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}
