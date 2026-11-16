package store

// Tranche 16a: Postgres repository for the PenCare retiree-healthcare
// registry — enrollees, health-access cards (mutable lifecycle), premium
// payments and fund contributions (both WORM; see
// db/migrations/0005_pencare.sql).

import (
	"context"
	"database/sql"
	"errors"

	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/pencare"
)

// PenCareRepo persists PenCare state in Postgres. All queries are
// tenant-scoped via WithTenant (NFR-10).
type PenCareRepo struct{ db *DB }

// NewPenCareRepo returns the Postgres-backed PenCare repository.
func NewPenCareRepo(db *DB) *PenCareRepo { return &PenCareRepo{db: db} }

func validateEnrollee(en pencare.Enrollee) error {
	switch {
	case en.ID == "":
		return errors.New("store: pencare enrollee id required")
	case en.TenantID == "":
		return errors.New("store: pencare enrollee tenant id required")
	case en.RSAID == "":
		return errors.New("store: pencare enrollee rsa id required")
	case en.MonthlyPension < 0:
		return errors.New("store: pencare pension must be non-negative")
	case en.Floor <= 0:
		return errors.New("store: pencare floor must be positive")
	case en.Status == "":
		return errors.New("store: pencare status required")
	}
	return nil
}

// InsertEnrollee registers a low-income retiree.
func (r *PenCareRepo) InsertEnrollee(ctx context.Context, en pencare.Enrollee) error {
	if err := validateEnrollee(en); err != nil {
		return err
	}
	return r.db.WithTenant(ctx, en.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO pencare_enrollee
			  (id, tenant_id, rsa_id, monthly_pension_kobo, floor_kobo, status, enrolled_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			en.ID, en.TenantID, en.RSAID, int64(en.MonthlyPension),
			int64(en.Floor), string(en.Status), en.EnrolledAt)
		return err
	})
}

// UpdateEnrolleeStatus moves an enrollee between ACTIVE/SUSPENDED/EXITED.
func (r *PenCareRepo) UpdateEnrolleeStatus(ctx context.Context, tenantID, id string, st pencare.EnrolleeStatus) error {
	return r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE pencare_enrollee SET status = $3
			WHERE tenant_id = $1 AND id = $2`, tenantID, id, string(st))
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("store: pencare enrollee not found")
		}
		return nil
	})
}

// Enrollees lists the tenant's enrollees, oldest first.
func (r *PenCareRepo) Enrollees(ctx context.Context, tenantID string) ([]pencare.Enrollee, error) {
	var out []pencare.Enrollee
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id, tenant_id, rsa_id, monthly_pension_kobo, floor_kobo, status, enrolled_at
			FROM pencare_enrollee WHERE tenant_id = $1
			ORDER BY enrolled_at, id`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var en pencare.Enrollee
			var st string
			if err := rows.Scan(&en.ID, &en.TenantID, &en.RSAID,
				(*int64)(&en.MonthlyPension), (*int64)(&en.Floor), &st, &en.EnrolledAt); err != nil {
				return err
			}
			en.Status = pencare.EnrolleeStatus(st)
			out = append(out, en)
		}
		return rows.Err()
	})
	return out, err
}

// InsertCard records a card request.
func (r *PenCareRepo) InsertCard(ctx context.Context, c pencare.Card) error {
	if c.ID == "" || c.TenantID == "" || c.EnrolleeID == "" || c.Status == "" {
		return errors.New("store: pencare card id/tenant/enrollee/status required")
	}
	return r.db.WithTenant(ctx, c.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO pencare_card
			  (id, tenant_id, enrollee_id, card_number, status, requested_at, issued_at)
			VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7)`,
			c.ID, c.TenantID, c.EnrolleeID, c.CardNumber,
			string(c.Status), c.RequestedAt, c.IssuedAt)
		return err
	})
}

// UpdateCard persists a card state transition (issue/activate/suspend).
func (r *PenCareRepo) UpdateCard(ctx context.Context, c pencare.Card) error {
	return r.db.WithTenant(ctx, c.TenantID, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE pencare_card SET card_number = NULLIF($3, ''), status = $4, issued_at = $5
			WHERE tenant_id = $1 AND id = $2`,
			c.TenantID, c.ID, c.CardNumber, string(c.Status), c.IssuedAt)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("store: pencare card not found")
		}
		return nil
	})
}

// Cards lists cards, optionally filtered by enrollee.
func (r *PenCareRepo) Cards(ctx context.Context, tenantID, enrolleeID string) ([]pencare.Card, error) {
	var out []pencare.Card
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		q := `
			SELECT id, tenant_id, enrollee_id, COALESCE(card_number, ''), status, requested_at, issued_at
			FROM pencare_card WHERE tenant_id = $1`
		args := []any{tenantID}
		if enrolleeID != "" {
			q += ` AND enrollee_id = $2`
			args = append(args, enrolleeID)
		}
		q += ` ORDER BY requested_at, id`
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c pencare.Card
			var st string
			if err := rows.Scan(&c.ID, &c.TenantID, &c.EnrolleeID, &c.CardNumber,
				&st, &c.RequestedAt, &c.IssuedAt); err != nil {
				return err
			}
			c.Status = pencare.CardStatus(st)
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// InsertPremium appends a premium payment. WORM — the table rejects
// updates and deletes by trigger.
func (r *PenCareRepo) InsertPremium(ctx context.Context, p pencare.Premium) error {
	if p.ID == "" || p.TenantID == "" || p.EnrolleeID == "" || p.Amount <= 0 || p.Period == "" {
		return errors.New("store: pencare premium id/tenant/enrollee/amount/period required")
	}
	return r.db.WithTenant(ctx, p.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO pencare_premium
			  (id, tenant_id, enrollee_id, insurer_ref, amount_kobo, period, paid_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			p.ID, p.TenantID, p.EnrolleeID, p.InsurerRef,
			int64(p.Amount), p.Period, p.PaidAt)
		return err
	})
}

// Premiums lists premium payments, optionally filtered by enrollee.
func (r *PenCareRepo) Premiums(ctx context.Context, tenantID, enrolleeID string) ([]pencare.Premium, error) {
	var out []pencare.Premium
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		q := `
			SELECT id, tenant_id, enrollee_id, insurer_ref, amount_kobo, period, paid_at
			FROM pencare_premium WHERE tenant_id = $1`
		args := []any{tenantID}
		if enrolleeID != "" {
			q += ` AND enrollee_id = $2`
			args = append(args, enrolleeID)
		}
		q += ` ORDER BY paid_at, id`
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p pencare.Premium
			if err := rows.Scan(&p.ID, &p.TenantID, &p.EnrolleeID, &p.InsurerRef,
				(*int64)(&p.Amount), &p.Period, &p.PaidAt); err != nil {
				return err
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	return out, err
}

// InsertFundContribution appends a PenCare fund receipt. WORM.
func (r *PenCareRepo) InsertFundContribution(ctx context.Context, fc pencare.FundContribution) error {
	if fc.ID == "" || fc.TenantID == "" || fc.Source == "" || fc.ContributorRef == "" || fc.Amount <= 0 || fc.Period == "" {
		return errors.New("store: pencare fund contribution id/tenant/source/ref/amount/period required")
	}
	return r.db.WithTenant(ctx, fc.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO pencare_fund_contribution
			  (id, tenant_id, source, contributor_ref, amount_kobo, period, received_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			fc.ID, fc.TenantID, string(fc.Source), fc.ContributorRef,
			int64(fc.Amount), fc.Period, fc.ReceivedAt)
		return err
	})
}

// FundContributions lists the tenant's fund receipts, oldest first.
func (r *PenCareRepo) FundContributions(ctx context.Context, tenantID string) ([]pencare.FundContribution, error) {
	var out []pencare.FundContribution
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id, tenant_id, source, contributor_ref, amount_kobo, period, received_at
			FROM pencare_fund_contribution WHERE tenant_id = $1
			ORDER BY received_at, id`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var fc pencare.FundContribution
			var src string
			if err := rows.Scan(&fc.ID, &fc.TenantID, &src, &fc.ContributorRef,
				(*int64)(&fc.Amount), &fc.Period, &fc.ReceivedAt); err != nil {
				return err
			}
			fc.Source = pencare.FundSource(src)
			out = append(out, fc)
		}
		return rows.Err()
	})
	return out, err
}

// FundAggregates totals fund receipts by source for a tenant.
func (r *PenCareRepo) FundAggregates(ctx context.Context, tenantID string) (map[pencare.FundSource]money.Kobo, error) {
	out := map[pencare.FundSource]money.Kobo{}
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT source, COALESCE(SUM(amount_kobo), 0)
			FROM pencare_fund_contribution WHERE tenant_id = $1 GROUP BY source`, tenantID)
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
			out[pencare.FundSource(src)] = money.Kobo(total)
		}
		return rows.Err()
	})
	return out, err
}
