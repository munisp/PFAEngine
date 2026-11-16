package store

// Tranche 14: Postgres repositories for the unified onboarding framework
// (FR-520) and member preferences (REG-06). onboarding_case/onboarding_step
// are updatable (the state machine mutates them); onboarding_document is
// WORM (see db/migrations/0003_onboarding.sql). The repos implement the
// onboarding.Repo and prefs.Repo interfaces consumed by the write-through
// persistent stores; store imports the domain packages, never vice versa.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/munisp/pfaengine/internal/i18n"
	"github.com/munisp/pfaengine/internal/onboarding"
	"github.com/munisp/pfaengine/internal/prefs"
)

// ---- onboarding cases ----

// OnboardingRepo persists onboarding cases (with steps and documents) in
// Postgres. All writes for one case run inside a single tenant-scoped
// transaction (WithTenant).
type OnboardingRepo struct{ db *DB }

// NewOnboardingRepo returns the Postgres-backed onboarding.Repo.
func NewOnboardingRepo(db *DB) *OnboardingRepo { return &OnboardingRepo{db: db} }

// validateOnboardingCase enforces the invariants the DB relies on; it is
// pure and unit-testable without a database.
func validateOnboardingCase(c onboarding.Case) error {
	switch {
	case c.ID == "":
		return errors.New("store: onboarding case id required")
	case c.TenantID == "":
		return errors.New("store: onboarding tenant id required")
	case c.Stakeholder == "":
		return errors.New("store: onboarding stakeholder required")
	case c.Status == "":
		return errors.New("store: onboarding status required")
	case c.SubmittedBy == "":
		return errors.New("store: onboarding submitted_by required")
	}
	for _, d := range c.Docs {
		if d.ID == "" || d.Kind == "" || d.Ref == "" {
			return fmt.Errorf("store: onboarding document id/kind/ref required (doc %q)", d.ID)
		}
	}
	return nil
}

// Save upserts the case and its steps and appends any new documents, all
// in one tenant-scoped transaction. Documents are WORM: an already-stored
// document ID is left untouched (ON CONFLICT DO NOTHING).
func (r *OnboardingRepo) Save(ctx context.Context, c onboarding.Case) error {
	if err := validateOnboardingCase(c); err != nil {
		return err
	}
	return r.db.WithTenant(ctx, c.TenantID, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO onboarding_case (id, tenant_id, stakeholder, status, ref_id, reject_reason, submitted_by, approved_by, created_at, updated_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			 ON CONFLICT (tenant_id, id) DO UPDATE SET
			   status=EXCLUDED.status, ref_id=EXCLUDED.ref_id,
			   reject_reason=EXCLUDED.reject_reason, approved_by=EXCLUDED.approved_by,
			   updated_at=EXCLUDED.updated_at`,
			c.ID, c.TenantID, string(c.Stakeholder), string(c.Status),
			nullString(c.RefID), nullString(c.RejectReason), c.SubmittedBy,
			nullString(c.ApprovedBy), c.CreatedAt, c.UpdatedAt); err != nil {
			return err
		}
		for _, s := range c.Steps {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO onboarding_step (tenant_id, case_id, name, required, done, done_at)
				 VALUES ($1,$2,$3,$4,$5,$6)
				 ON CONFLICT (tenant_id, case_id, name) DO UPDATE SET
				   done=EXCLUDED.done, done_at=EXCLUDED.done_at`,
				c.TenantID, c.ID, s.Name, s.Required, s.Done, nullTime(s.At, s.Done)); err != nil {
				return err
			}
		}
		for _, d := range c.Docs {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO onboarding_document (id, tenant_id, case_id, kind, uri, uploaded_at)
				 VALUES ($1,$2,$3,$4,$5,$6)
				 ON CONFLICT (tenant_id, id) DO NOTHING`,
				d.ID, c.TenantID, c.ID, d.Kind, d.Ref, d.UploadedAt); err != nil {
				return err
			}
		}
		return nil
	})
}

// Load returns the case (with steps in pipeline order and documents) for
// the tenant; onboarding.ErrCaseNotFound when absent or cross-tenant.
func (r *OnboardingRepo) Load(ctx context.Context, tenantID, id string) (onboarding.Case, error) {
	var c onboarding.Case
	var refID, reason, approvedBy sql.NullString
	err := r.db.sql.QueryRowContext(ctx,
		`SELECT id, tenant_id, stakeholder, status, ref_id, reject_reason, submitted_by, approved_by, created_at, updated_at
		 FROM onboarding_case WHERE tenant_id=$1 AND id=$2`, tenantID, id).
		Scan(&c.ID, &c.TenantID, (*string)(&c.Stakeholder), (*string)(&c.Status),
			&refID, &reason, &c.SubmittedBy, &approvedBy, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return onboarding.Case{}, onboarding.ErrCaseNotFound
	}
	if err != nil {
		return onboarding.Case{}, err
	}
	c.RefID, c.RejectReason, c.ApprovedBy = refID.String, reason.String, approvedBy.String
	steps, err := r.loadSteps(ctx, tenantID, id, c.Stakeholder)
	if err != nil {
		return onboarding.Case{}, err
	}
	c.Steps = steps
	docs, err := r.loadDocs(ctx, tenantID, id)
	if err != nil {
		return onboarding.Case{}, err
	}
	c.Docs = docs
	return c, nil
}

// loadSteps returns the case's steps re-ordered against the stakeholder's
// pipeline template: onboarding_step persists no ordinal (PK(case_id,
// name)), so the deterministic Pipeline template restores the order;
// persisted steps missing from the template keep name order at the end.
func (r *OnboardingRepo) loadSteps(ctx context.Context, tenantID, caseID string, sh onboarding.Stakeholder) ([]onboarding.Step, error) {
	rows, err := r.db.sql.QueryContext(ctx,
		`SELECT name, required, done, done_at FROM onboarding_step WHERE tenant_id=$1 AND case_id=$2`, tenantID, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byName := map[string]onboarding.Step{}
	for rows.Next() {
		var s onboarding.Step
		var doneAt sql.NullTime
		if err := rows.Scan(&s.Name, &s.Required, &s.Done, &doneAt); err != nil {
			return nil, err
		}
		if doneAt.Valid {
			s.At = doneAt.Time
		}
		byName[s.Name] = s
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]onboarding.Step, 0, len(byName))
	for _, t := range onboarding.Pipeline(sh) {
		if s, ok := byName[t.Name]; ok {
			out = append(out, s)
			delete(byName, t.Name)
		}
	}
	rest := make([]onboarding.Step, 0, len(byName))
	for _, s := range byName {
		rest = append(rest, s)
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].Name < rest[j].Name })
	return append(out, rest...), nil
}

func (r *OnboardingRepo) loadDocs(ctx context.Context, tenantID, caseID string) ([]onboarding.Document, error) {
	rows, err := r.db.sql.QueryContext(ctx,
		`SELECT id, kind, uri, uploaded_at FROM onboarding_document WHERE tenant_id=$1 AND case_id=$2
		 ORDER BY uploaded_at, id`, tenantID, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []onboarding.Document
	for rows.Next() {
		var d onboarding.Document
		if err := rows.Scan(&d.ID, &d.Kind, &d.Ref, &d.UploadedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// List returns the tenant's cases (with steps and documents), optionally
// filtered by stakeholder and/or status (nil = wildcard), oldest first.
func (r *OnboardingRepo) List(ctx context.Context, tenantID string, stakeholder, status *string) ([]onboarding.Case, error) {
	q := `SELECT id FROM onboarding_case WHERE tenant_id=$1`
	args := []any{tenantID}
	if stakeholder != nil {
		args = append(args, *stakeholder)
		q += fmt.Sprintf(" AND stakeholder=$%d", len(args))
	}
	if status != nil {
		args = append(args, *status)
		q += fmt.Sprintf(" AND status=$%d", len(args))
	}
	q += " ORDER BY created_at, id"
	rows, err := r.db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]onboarding.Case, 0, len(ids))
	for _, id := range ids {
		c, err := r.Load(ctx, tenantID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// ---- member preferences ----

// PrefsRepo persists member preferences (locale + E.164 phone binding) in
// Postgres. Phone numbers are normalised with prefs.NormalizePhone so
// "+234..." (member records) and "234..." (channel webhooks) match.
type PrefsRepo struct{ db *DB }

// NewPrefsRepo returns the Postgres-backed prefs.Repo.
func NewPrefsRepo(db *DB) *PrefsRepo { return &PrefsRepo{db: db} }

// Set records the member's preferred locale (upsert).
func (r *PrefsRepo) Set(ctx context.Context, tenantID, memberID string, loc i18n.Locale) error {
	if memberID == "" {
		return errors.New("store: prefs member id required")
	}
	return r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO member_prefs (tenant_id, member_id, locale, updated_at)
			 VALUES ($1,$2,$3,now())
			 ON CONFLICT (tenant_id, member_id) DO UPDATE SET
			   locale=EXCLUDED.locale, updated_at=now()`,
			tenantID, memberID, string(loc))
		return err
	})
}

// Get returns the member's stored locale; prefs.ErrNotFound when unset.
func (r *PrefsRepo) Get(ctx context.Context, tenantID, memberID string) (i18n.Locale, error) {
	var loc string
	err := r.db.sql.QueryRowContext(ctx,
		`SELECT locale FROM member_prefs WHERE tenant_id=$1 AND member_id=$2`,
		tenantID, memberID).Scan(&loc)
	if err == sql.ErrNoRows {
		return "", prefs.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return i18n.Locale(loc), nil
}

// LinkPhone binds a (normalised) phone number to the member (upsert).
func (r *PrefsRepo) LinkPhone(ctx context.Context, tenantID, phone, memberID string) error {
	phone = prefs.NormalizePhone(phone)
	if phone == "" || memberID == "" {
		return nil // matches prefs.Store.LinkPhone: empty input is a no-op
	}
	return r.db.WithTenant(ctx, tenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO member_prefs (tenant_id, member_id, phone_e164, updated_at)
			 VALUES ($1,$2,$3,now())
			 ON CONFLICT (tenant_id, member_id) DO UPDATE SET
			   phone_e164=EXCLUDED.phone_e164, updated_at=now()`,
			tenantID, memberID, phone)
		return err
	})
}

// MemberForPhone resolves a phone number to a member ID;
// prefs.ErrNotFound when the number is unbound.
func (r *PrefsRepo) MemberForPhone(ctx context.Context, tenantID, phone string) (string, error) {
	var memberID string
	err := r.db.sql.QueryRowContext(ctx,
		`SELECT member_id FROM member_prefs WHERE tenant_id=$1 AND phone_e164=$2`,
		tenantID, prefs.NormalizePhone(phone)).Scan(&memberID)
	if err == sql.ErrNoRows {
		return "", prefs.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return memberID, nil
}

// LocaleForPhone resolves the locale for an inbound channel sender:
// phone → member → locale, defaulting to prefs.DefaultLocale for unknown
// numbers (matches prefs.Store.LocaleForPhone semantics).
func (r *PrefsRepo) LocaleForPhone(ctx context.Context, tenantID, phone string) i18n.Locale {
	memberID, err := r.MemberForPhone(ctx, tenantID, phone)
	if err != nil {
		return prefs.DefaultLocale
	}
	loc, err := r.Get(ctx, tenantID, memberID)
	if err != nil {
		return prefs.DefaultLocale
	}
	return loc
}

// ---- helpers ----

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// nullTime maps a zero time (or an undone step) to NULL.
func nullTime(t time.Time, keep bool) any {
	if !keep || t.IsZero() {
		return nil
	}
	return t
}
