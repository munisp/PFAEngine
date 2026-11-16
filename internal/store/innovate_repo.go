package store

// Tranche 17: Postgres repository for the innovation tranche's stateful
// domains — fraud cases, embedded API keys + webhooks, agent network
// (commission ledger WORM), succession nominations (WORM version rows;
// reconfirm/supersede are new versions, never updates), mortgage equity
// applications and escalation rules. See db/migrations/0007_innovations.sql.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/munisp/pfaengine/internal/agents"
	"github.com/munisp/pfaengine/internal/embedded"
	"github.com/munisp/pfaengine/internal/escalation"
	"github.com/munisp/pfaengine/internal/fraud"
	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/mortgage"
	"github.com/munisp/pfaengine/internal/succession"
)

// InnovateRepo persists Tranche 17 state in Postgres, tenant-scoped via
// WithTenant (NFR-10).
type InnovateRepo struct{ db *DB }

// NewInnovateRepo returns the Postgres-backed repository.
func NewInnovateRepo(db *DB) *InnovateRepo { return &InnovateRepo{db: db} }

// ---- fraud cases -------------------------------------------------------

// InsertFraudCase persists an opened case.
func (r *InnovateRepo) InsertFraudCase(ctx context.Context, c fraud.Case) error {
	if c.ID == "" || c.TenantID == "" || c.MemberID == "" {
		return errors.New("store: fraud case id/tenant/member required")
	}
	txJSON, err := json.Marshal(c.Tx)
	if err != nil {
		return err
	}
	return r.db.WithTenant(ctx, c.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO fraud_case (id, tenant_id, member_id, score, signals, tx_json, status, opened_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			c.ID, c.TenantID, c.MemberID, c.Score, strings.Join(c.Signals, ","),
			string(txJSON), string(c.Status), c.OpenedAt)
		return err
	})
}

// ResolveFraudCase updates a case status (fraud_case is mutable — the
// case record is investigative state, not a financial ledger).
func (r *InnovateRepo) ResolveFraudCase(ctx context.Context, tenantID, caseID string, st fraud.CaseStatus) error {
	return r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE fraud_case SET status = $3 WHERE tenant_id = $1 AND id = $2`,
			tenantID, caseID, string(st))
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return errors.New("store: fraud case not found")
		}
		return nil
	})
}

// FraudCases lists cases, optionally only OPEN.
func (r *InnovateRepo) FraudCases(ctx context.Context, tenantID string, openOnly bool) ([]fraud.Case, error) {
	var out []fraud.Case
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		q := `SELECT id, tenant_id, member_id, score, signals, tx_json, status, opened_at
			FROM fraud_case WHERE tenant_id = $1`
		if openOnly {
			q += ` AND status = 'OPEN'`
		}
		q += ` ORDER BY opened_at, id`
		rows, err := tx.QueryContext(ctx, q, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c fraud.Case
			var signals, txJSON, st string
			if err := rows.Scan(&c.ID, &c.TenantID, &c.MemberID, &c.Score, &signals, &txJSON, &st, &c.OpenedAt); err != nil {
				return err
			}
			if signals != "" {
				c.Signals = strings.Split(signals, ",")
			}
			if err := json.Unmarshal([]byte(txJSON), &c.Tx); err != nil {
				return err
			}
			c.Status = fraud.CaseStatus(st)
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// ---- embedded keys & webhooks ------------------------------------------

// InsertAPIKey persists an issued key (hash only).
func (r *InnovateRepo) InsertAPIKey(ctx context.Context, k embedded.Key) error {
	scopes := make([]string, 0, len(k.Scopes))
	for _, s := range k.Scopes {
		scopes = append(scopes, string(s))
	}
	return r.db.WithTenant(ctx, k.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO api_key (id, tenant_id, label, hash, scopes, sandbox, revoked, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			k.ID, k.TenantID, k.Label, k.Hash, strings.Join(scopes, ","), k.Sandbox, k.Revoked, k.CreatedAt)
		return err
	})
}

// APIKeys lists a tenant's keys (hashes included for auth reload).
func (r *InnovateRepo) APIKeys(ctx context.Context, tenantID string) ([]embedded.Key, error) {
	var out []embedded.Key
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id, tenant_id, label, hash, scopes, sandbox, revoked, created_at
			FROM api_key WHERE tenant_id = $1 ORDER BY created_at, id`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k embedded.Key
			var scopes string
			if err := rows.Scan(&k.ID, &k.TenantID, &k.Label, &k.Hash, &scopes, &k.Sandbox, &k.Revoked, &k.CreatedAt); err != nil {
				return err
			}
			for _, s := range strings.Split(scopes, ",") {
				k.Scopes = append(k.Scopes, embedded.Scope(s))
			}
			out = append(out, k)
		}
		return rows.Err()
	})
	return out, err
}

// RevokeAPIKey marks a key revoked.
func (r *InnovateRepo) RevokeAPIKey(ctx context.Context, tenantID, keyID string) error {
	return r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE api_key SET revoked = true WHERE tenant_id = $1 AND id = $2`, tenantID, keyID)
		return err
	})
}

// InsertWebhook persists a subscription.
func (r *InnovateRepo) InsertWebhook(ctx context.Context, w embedded.Webhook) error {
	return r.db.WithTenant(ctx, w.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO webhook_sub (id, tenant_id, url, events, secret, active, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			w.ID, w.TenantID, w.URL, strings.Join(w.Events, ","), w.Secret, w.Active, w.CreatedAt)
		return err
	})
}

// ---- agents ------------------------------------------------------------

// InsertAgent accredits an agent.
func (r *InnovateRepo) InsertAgent(ctx context.Context, a agents.Agent) error {
	return r.db.WithTenant(ctx, a.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO agent (id, tenant_id, name, phone, state_code, commission_bps, suspended, accredited_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			a.ID, a.TenantID, a.Name, a.Phone, a.StateCode, a.CommissionBps, a.Suspended, a.AccreditedAt)
		return err
	})
}

// InsertAgentEnrolment records an agent-originated enrolment.
func (r *InnovateRepo) InsertAgentEnrolment(ctx context.Context, en agents.Enrolment) error {
	return r.db.WithTenant(ctx, en.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO agent_enrolment (id, tenant_id, agent_id, rsa_id, name, phone, trade, at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			en.ID, en.TenantID, en.AgentID, en.RSAID, en.Name, en.Phone, en.Trade, en.At)
		return err
	})
}

// InsertCommission appends a commission. WORM — trigger rejects
// updates/deletes.
func (r *InnovateRepo) InsertCommission(ctx context.Context, c agents.Commission) error {
	return r.db.WithTenant(ctx, c.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO agent_commission (id, tenant_id, agent_id, contribution_kobo, amount_kobo, period, at)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			c.ID, c.TenantID, c.AgentID, int64(c.Contribution), int64(c.Amount), c.Period, c.At)
		return err
	})
}

// CommissionTotal sums an agent's commissions.
func (r *InnovateRepo) CommissionTotal(ctx context.Context, tenantID, agentID string) (money.Kobo, error) {
	var total int64
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(amount_kobo),0) FROM agent_commission
			WHERE tenant_id = $1 AND agent_id = $2`, tenantID, agentID).Scan(&total)
	})
	return money.Kobo(total), err
}

// ---- succession nominations (WORM version rows) -------------------------

// InsertNomination appends a new version row. Reconfirmations are new
// rows with version+1 and identical beneficiaries — the table is WORM.
func (r *InnovateRepo) InsertNomination(ctx context.Context, n succession.Nomination) error {
	bens, err := json.Marshal(n.Beneficiaries)
	if err != nil {
		return err
	}
	return r.db.WithTenant(ctx, n.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO nomination (id, tenant_id, member_id, version, beneficiaries_json, confirmed_at, next_reconfirm_due, superseded)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			n.ID, n.TenantID, n.MemberID, n.Version, string(bens), n.ConfirmedAt, n.NextReconfirmDue, n.Superseded)
		return err
	})
}

// CurrentNomination returns the highest-version row for a member.
func (r *InnovateRepo) CurrentNomination(ctx context.Context, tenantID, memberID string) (succession.Nomination, error) {
	var n succession.Nomination
	var bens string
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT id, tenant_id, member_id, version, beneficiaries_json, confirmed_at, next_reconfirm_due, superseded
			FROM nomination WHERE tenant_id = $1 AND member_id = $2
			ORDER BY version DESC LIMIT 1`, tenantID, memberID).
			Scan(&n.ID, &n.TenantID, &n.MemberID, &n.Version, &bens, &n.ConfirmedAt, &n.NextReconfirmDue, &n.Superseded)
	})
	if err != nil {
		return n, err
	}
	if err := json.Unmarshal([]byte(bens), &n.Beneficiaries); err != nil {
		return n, err
	}
	return n, nil
}

// ---- mortgage applications ----------------------------------------------

// InsertMortgageApp persists an application.
func (r *InnovateRepo) InsertMortgageApp(ctx context.Context, a mortgage.Application) error {
	return r.db.WithTenant(ctx, a.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO mortgage_application (id, tenant_id, member_id, rsa_balance_kobo, amount_kobo, lender_ref, status, reason, submitted_at, decided_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			a.ID, a.TenantID, a.MemberID, int64(a.RSABalance), int64(a.Amount), a.LenderRef,
			string(a.Status), a.Reason, a.SubmittedAt, a.DecidedAt)
		return err
	})
}

// UpdateMortgageStatus persists a status transition.
func (r *InnovateRepo) UpdateMortgageStatus(ctx context.Context, a mortgage.Application) error {
	return r.db.WithTenant(ctx, a.TenantID, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE mortgage_application SET status = $3, reason = $4, decided_at = $5
			WHERE tenant_id = $1 AND id = $2`,
			a.TenantID, a.ID, string(a.Status), a.Reason, a.DecidedAt)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return errors.New("store: mortgage application not found")
		}
		return nil
	})
}

// MortgageApps lists applications, optionally filtered by member.
func (r *InnovateRepo) MortgageApps(ctx context.Context, tenantID, memberID string) ([]mortgage.Application, error) {
	var out []mortgage.Application
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		q := `SELECT id, tenant_id, member_id, rsa_balance_kobo, amount_kobo, lender_ref, status, reason, submitted_at, decided_at
			FROM mortgage_application WHERE tenant_id = $1`
		args := []any{tenantID}
		if memberID != "" {
			q += ` AND member_id = $2`
			args = append(args, memberID)
		}
		q += ` ORDER BY submitted_at, id`
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a mortgage.Application
			var st string
			if err := rows.Scan(&a.ID, &a.TenantID, &a.MemberID, (*int64)(&a.RSABalance),
				(*int64)(&a.Amount), &a.LenderRef, &st, &a.Reason, &a.SubmittedAt, &a.DecidedAt); err != nil {
				return err
			}
			a.Status = mortgage.AppStatus(st)
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

// ---- escalation rules ----------------------------------------------------

// UpsertEscalationRule inserts or updates a member's rule (rules are
// mutable config, not a ledger).
func (r *InnovateRepo) UpsertEscalationRule(ctx context.Context, ru escalation.Rule) error {
	return r.db.WithTenant(ctx, ru.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO escalation_rule (id, tenant_id, member_id, base_rate_bps, step_bps, max_bps, current_bps, opted_out, opt_out_reason, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (tenant_id, member_id) DO UPDATE SET
			  current_bps = EXCLUDED.current_bps, opted_out = EXCLUDED.opted_out,
			  opt_out_reason = EXCLUDED.opt_out_reason`,
			ru.ID, ru.TenantID, ru.MemberID, ru.BaseRateBps, ru.StepBps, ru.MaxBps,
			ru.CurrentBps, ru.OptedOut, ru.OptOutReason, ru.CreatedAt)
		return err
	})
}

// EscalationRules lists a tenant's rules.
func (r *InnovateRepo) EscalationRules(ctx context.Context, tenantID string) ([]escalation.Rule, error) {
	var out []escalation.Rule
	err := r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id, tenant_id, member_id, base_rate_bps, step_bps, max_bps, current_bps, opted_out, opt_out_reason, created_at
			FROM escalation_rule WHERE tenant_id = $1 ORDER BY created_at, id`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ru escalation.Rule
			if err := rows.Scan(&ru.ID, &ru.TenantID, &ru.MemberID, &ru.BaseRateBps, &ru.StepBps,
				&ru.MaxBps, &ru.CurrentBps, &ru.OptedOut, &ru.OptOutReason, &ru.CreatedAt); err != nil {
				return err
			}
			out = append(out, ru)
		}
		return rows.Err()
	})
	return out, err
}

// touched keeps time imported for future scheduling helpers.
var _ = time.Now
