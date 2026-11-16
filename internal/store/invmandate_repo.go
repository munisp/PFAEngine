package store

// Tranche 16b: Postgres repository for the infrastructure-investment
// mandate registry — mandates (mutable status), commitments and
// deployments (both WORM; see db/migrations/0006_invmandate.sql).

import (
	"context"
	"database/sql"
	"errors"

	"github.com/munisp/pfaengine/internal/invmandate"
)

// InvMandateRepo persists mandates/commitments/deployments in Postgres.
// All queries are tenant-scoped via WithTenant (NFR-10).
type InvMandateRepo struct{ db *DB }

// NewInvMandateRepo returns the Postgres-backed mandate repository.
func NewInvMandateRepo(db *DB) *InvMandateRepo { return &InvMandateRepo{db: db} }

func validateMandate(m invmandate.Mandate) error {
	switch {
	case m.ID == "":
		return errors.New("store: mandate id required")
	case m.TenantID == "":
		return errors.New("store: mandate tenant id required")
	case m.Name == "":
		return errors.New("store: mandate name required")
	case m.AssetClass == "":
		return errors.New("store: mandate asset class required")
	case m.Currency == "":
		return errors.New("store: mandate currency required")
	case m.TargetMinor <= 0:
		return errors.New("store: mandate target must be positive")
	case m.CapBps <= 0 || m.CapBps > 10000:
		return errors.New("store: mandate cap out of range")
	case m.Status == "":
		return errors.New("store: mandate status required")
	}
	return nil
}

// InsertMandate registers a mandate.
func (r *InvMandateRepo) InsertMandate(ctx context.Context, m invmandate.Mandate) error {
	if err := validateMandate(m); err != nil {
		return err
	}
	return r.db.WithTenant(ctx, m.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO inv_mandate
			  (id, tenant_id, name, asset_class, partner, currency, target_minor, cap_bps, status, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			m.ID, m.TenantID, m.Name, string(m.AssetClass), m.Partner,
			m.Currency, m.TargetMinor, m.CapBps, string(m.Status), m.CreatedAt)
		return err
	})
}

// UpdateMandateStatus transitions a mandate (OPEN -> CLOSED).
func (r *InvMandateRepo) UpdateMandateStatus(ctx context.Context, tenantID, id string, st invmandate.MandateStatus) error {
	return r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE inv_mandate SET status = $3
			WHERE tenant_id = $1 AND id = $2`, tenantID, id, string(st))
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("store: mandate not found")
		}
		return nil
	})
}

// Mandates lists the tenant's mandates, oldest first.
func (r *InvMandateRepo) Mandates(ctx context.Context, tenantID string) ([]invmandate.Mandate, error) {
	var out []invmandate.Mandate
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id, tenant_id, name, asset_class, partner, currency, target_minor, cap_bps, status, created_at
			FROM inv_mandate WHERE tenant_id = $1
			ORDER BY created_at, id`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m invmandate.Mandate
			var class, st string
			if err := rows.Scan(&m.ID, &m.TenantID, &m.Name, &class, &m.Partner,
				&m.Currency, &m.TargetMinor, &m.CapBps, &st, &m.CreatedAt); err != nil {
				return err
			}
			m.AssetClass = invmandate.AssetClass(class)
			m.Status = invmandate.MandateStatus(st)
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, err
}

// InsertCommitment appends a commitment. WORM — updates/deletes are
// rejected by trigger; corrections are reversal entries.
func (r *InvMandateRepo) InsertCommitment(ctx context.Context, c invmandate.Commitment) error {
	if c.ID == "" || c.TenantID == "" || c.MandateID == "" || c.FundID == "" || c.AmountMinor <= 0 || c.Currency == "" {
		return errors.New("store: commitment id/tenant/mandate/fund/amount/currency required")
	}
	return r.db.WithTenant(ctx, c.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO inv_commitment
			  (id, tenant_id, mandate_id, fund_id, amount_minor, currency, ref, committed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			c.ID, c.TenantID, c.MandateID, c.FundID,
			c.AmountMinor, c.Currency, c.Ref, c.CommittedAt)
		return err
	})
}

// Commitments lists commitments, optionally filtered by mandate.
func (r *InvMandateRepo) Commitments(ctx context.Context, tenantID, mandateID string) ([]invmandate.Commitment, error) {
	var out []invmandate.Commitment
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		q := `
			SELECT id, tenant_id, mandate_id, fund_id, amount_minor, currency, ref, committed_at
			FROM inv_commitment WHERE tenant_id = $1`
		args := []any{tenantID}
		if mandateID != "" {
			q += ` AND mandate_id = $2`
			args = append(args, mandateID)
		}
		q += ` ORDER BY committed_at, id`
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c invmandate.Commitment
			if err := rows.Scan(&c.ID, &c.TenantID, &c.MandateID, &c.FundID,
				&c.AmountMinor, &c.Currency, &c.Ref, &c.CommittedAt); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// InsertDeployment appends a capital deployment. WORM.
func (r *InvMandateRepo) InsertDeployment(ctx context.Context, d invmandate.Deployment) error {
	if d.ID == "" || d.TenantID == "" || d.MandateID == "" || d.AmountMinor <= 0 || d.Currency == "" {
		return errors.New("store: deployment id/tenant/mandate/amount/currency required")
	}
	return r.db.WithTenant(ctx, d.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO inv_deployment
			  (id, tenant_id, mandate_id, amount_minor, currency, vehicle, deployed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			d.ID, d.TenantID, d.MandateID, d.AmountMinor,
			d.Currency, d.Vehicle, d.DeployedAt)
		return err
	})
}

// Deployments lists deployments, optionally filtered by mandate.
func (r *InvMandateRepo) Deployments(ctx context.Context, tenantID, mandateID string) ([]invmandate.Deployment, error) {
	var out []invmandate.Deployment
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		q := `
			SELECT id, tenant_id, mandate_id, amount_minor, currency, vehicle, deployed_at
			FROM inv_deployment WHERE tenant_id = $1`
		args := []any{tenantID}
		if mandateID != "" {
			q += ` AND mandate_id = $2`
			args = append(args, mandateID)
		}
		q += ` ORDER BY deployed_at, id`
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d invmandate.Deployment
			if err := rows.Scan(&d.ID, &d.TenantID, &d.MandateID,
				&d.AmountMinor, &d.Currency, &d.Vehicle, &d.DeployedAt); err != nil {
				return err
			}
			out = append(out, d)
		}
		return rows.Err()
	})
	return out, err
}
