package store

// Tranche 20: Postgres repository for the unified case management engine
// and CRM artefacts (interaction timeline + campaigns). The case row keeps
// the full JSON snapshot so the engine rehydrates after restart; columnar
// status/queue/priority support queue queries. See
// db/migrations/0008_casemgmt_crm.sql.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/munisp/pfaengine/internal/casemgmt"
	"github.com/munisp/pfaengine/internal/crm"
)

// CaseRepo persists casemgmt + crm state, tenant-scoped via WithTenant.
type CaseRepo struct{ db *DB }

// NewCaseRepo returns the Postgres-backed repository.
func NewCaseRepo(db *DB) *CaseRepo { return &CaseRepo{db: db} }

// Save upserts a case (casemgmt.Repo implementation). Cases are mutable
// operational state — not a financial ledger — so upsert is correct.
func (r *CaseRepo) Save(c casemgmt.Case) error {
	if c.ID == "" || c.TenantID == "" {
		return errors.New("store: case id/tenant required")
	}
	j, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return r.db.WithTenant(context.Background(), c.TenantID, func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO ops_case (id, tenant_id, type, subject, member_id, employer_id, status, priority, queue, assignee, source, due_at, sla_breached, case_json, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14, now())
			ON CONFLICT (tenant_id, id) DO UPDATE SET
				status = EXCLUDED.status, priority = EXCLUDED.priority,
				queue = EXCLUDED.queue, assignee = EXCLUDED.assignee,
				sla_breached = EXCLUDED.sla_breached, case_json = EXCLUDED.case_json,
				updated_at = now()`,
			c.ID, c.TenantID, string(c.Type), c.Subject, c.MemberID, c.EmployerID,
			string(c.Status), string(c.Priority), string(c.Queue), c.Assignee,
			string(c.Source), c.DueAt, c.SLABreached, string(j))
		return err
	})
}

// Cases rehydrates all cases for a tenant (engine warm-up on boot).
func (r *CaseRepo) Cases(ctx context.Context, tenantID string) ([]casemgmt.Case, error) {
	var out []casemgmt.Case
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT case_json FROM ops_case WHERE tenant_id = $1`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var j string
			if err := rows.Scan(&j); err != nil {
				return err
			}
			var c casemgmt.Case
			if err := json.Unmarshal([]byte(j), &c); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// SaveInteraction persists a CRM timeline entry.
func (r *CaseRepo) SaveInteraction(ctx context.Context, ix crm.Interaction) error {
	return r.db.WithTenant(ctx, ix.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO crm_interaction (id, tenant_id, member_id, channel, direction, kind, summary, case_id, staff_id, at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			ix.ID, ix.TenantID, ix.MemberID, string(ix.Channel), string(ix.Direction),
			ix.Kind, ix.Summary, ix.CaseID, ix.StaffID, ix.At)
		return err
	})
}

// SaveCampaign upserts a campaign.
func (r *CaseRepo) SaveCampaign(ctx context.Context, c crm.Campaign) error {
	return r.db.WithTenant(ctx, c.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO crm_campaign (id, tenant_id, name, segment, channel, message, status, recipients, launched_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (tenant_id, id) DO UPDATE SET
				status = EXCLUDED.status, recipients = EXCLUDED.recipients,
				launched_at = EXCLUDED.launched_at`,
			c.ID, c.TenantID, c.Name, string(c.Segment), string(c.Channel),
			c.Message, c.Status, c.Recipients, c.LaunchedAt)
		return err
	})
}
