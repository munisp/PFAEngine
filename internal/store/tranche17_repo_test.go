package store

// Tranche 17 integration test: gated on TEST_DATABASE_URL (apply
// migrations 0001–0007 first). Covers the stateful innovation domains —
// WORM rejection on agent commissions and nomination history, mortgage
// transitions, fraud case resolution, escalation upsert idempotency.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/agents"
	"github.com/munisp/pfaengine/internal/embedded"
	"github.com/munisp/pfaengine/internal/escalation"
	"github.com/munisp/pfaengine/internal/fraud"
	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/mortgage"
	"github.com/munisp/pfaengine/internal/succession"
)

func TestTranche17InnovateRoundTrip(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	repo := NewInnovateRepo(db)

	sfx := fmt.Sprintf("%d", time.Now().UnixNano())
	tenant := "tenant-t17-" + sfx
	now := time.Now().UTC()

	// Fraud case: open, list, resolve.
	c := fraud.Case{ID: "fc-" + sfx, TenantID: tenant, MemberID: "m1", Score: 75,
		Signals: []string{"VELOCITY_SPIKE", "CHANNEL_FLIP"}, Status: fraud.CaseOpen, OpenedAt: now,
		Tx: fraud.Tx{TenantID: tenant, MemberID: "m1", Kind: "WITHDRAWAL", Amount: money.NGN(600_000), Channel: "USSD", StateCode: "KD", At: now}}
	if err := repo.InsertFraudCase(ctx, c); err != nil {
		t.Fatalf("fraud insert: %v", err)
	}
	cases, err := repo.FraudCases(ctx, tenant, true)
	if err != nil || len(cases) != 1 || cases[0].Tx.Amount != money.NGN(600_000) || len(cases[0].Signals) != 2 {
		t.Fatalf("fraud list: %v %v", cases, err)
	}
	if err := repo.ResolveFraudCase(ctx, tenant, c.ID, fraud.CaseCleared); err != nil {
		t.Fatal(err)
	}
	if open, _ := repo.FraudCases(ctx, tenant, true); len(open) != 0 {
		t.Fatalf("still open: %v", open)
	}

	// Embedded: key + webhook.
	k := embedded.Key{ID: "key-" + sfx, TenantID: tenant, Label: "payroll-acme", Hash: "deadbeef",
		Scopes: []embedded.Scope{embedded.ScopeContribute}, Sandbox: true, CreatedAt: now}
	if err := repo.InsertAPIKey(ctx, k); err != nil {
		t.Fatalf("key: %v", err)
	}
	keys, _ := repo.APIKeys(ctx, tenant)
	if len(keys) != 1 || keys[0].Scopes[0] != embedded.ScopeContribute {
		t.Fatalf("keys: %v", keys)
	}
	if err := repo.RevokeAPIKey(ctx, tenant, k.ID); err != nil {
		t.Fatal(err)
	}
	w := embedded.Webhook{ID: "wh-" + sfx, TenantID: tenant, URL: "https://acme.example/h",
		Events: []string{"contribution.posted"}, Secret: "s", Active: true, CreatedAt: now}
	if err := repo.InsertWebhook(ctx, w); err != nil {
		t.Fatalf("webhook: %v", err)
	}

	// Agents: accredit, enrol, commission, WORM.
	a := agents.Agent{ID: "agt-" + sfx, TenantID: tenant, Name: "Musa", Phone: "0801",
		StateCode: "KD", CommissionBps: 250, AccreditedAt: now}
	if err := repo.InsertAgent(ctx, a); err != nil {
		t.Fatalf("agent: %v", err)
	}
	en := agents.Enrolment{ID: "age-" + sfx, TenantID: tenant, AgentID: a.ID, RSAID: "RSA-A1",
		Name: "Amara", Phone: "0802", Trade: "trader", At: now}
	if err := repo.InsertAgentEnrolment(ctx, en); err != nil {
		t.Fatalf("enrolment: %v", err)
	}
	cm := agents.Commission{ID: "agc-" + sfx, TenantID: tenant, AgentID: a.ID,
		Contribution: money.NGN(20_000), Amount: money.NGN(500), Period: "2026-11", At: now}
	if err := repo.InsertCommission(ctx, cm); err != nil {
		t.Fatalf("commission: %v", err)
	}
	total, _ := repo.CommissionTotal(ctx, tenant, a.ID)
	if total != money.NGN(500) {
		t.Fatalf("commission total: %v", total)
	}
	err = db.WithTenant(ctx, tenant, func(tx *sql.Tx) error {
		_, uerr := tx.ExecContext(ctx,
			`UPDATE agent_commission SET amount_kobo = 1 WHERE tenant_id = $1 AND id = $2`, tenant, cm.ID)
		return uerr
	})
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("commission WORM: %v", err)
	}

	// Succession: two versions, current = latest, WORM blocks updates.
	n1 := succession.Nomination{ID: "nom-" + sfx + "-1", TenantID: tenant, MemberID: "m1", Version: 1,
		Beneficiaries: []succession.Beneficiary{{Name: "Ada", ShareBps: 10000}},
		ConfirmedAt:   now, NextReconfirmDue: now.AddDate(1, 0, 0)}
	n2 := n1
	n2.ID = "nom-" + sfx + "-2"
	n2.Version = 2
	n2.Beneficiaries = []succession.Beneficiary{{Name: "Ada", ShareBps: 6000}, {Name: "Bola", ShareBps: 4000}}
	if err := repo.InsertNomination(ctx, n1); err != nil {
		t.Fatalf("nom1: %v", err)
	}
	if err := repo.InsertNomination(ctx, n2); err != nil {
		t.Fatalf("nom2: %v", err)
	}
	cur, err := repo.CurrentNomination(ctx, tenant, "m1")
	if err != nil || cur.Version != 2 || len(cur.Beneficiaries) != 2 {
		t.Fatalf("current: %v %+v", err, cur)
	}
	err = db.WithTenant(ctx, tenant, func(tx *sql.Tx) error {
		_, uerr := tx.ExecContext(ctx,
			`DELETE FROM nomination WHERE tenant_id = $1 AND id = $2`, tenant, n1.ID)
		return uerr
	})
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("nomination WORM: %v", err)
	}

	// Mortgage: insert + transitions.
	app := mortgage.Application{ID: "mtg-" + sfx, TenantID: tenant, MemberID: "m1",
		RSABalance: money.NGN(8_000_000), Amount: money.NGN(2_000_000), LenderRef: "lender-x",
		Status: mortgage.AppSubmitted, SubmittedAt: now}
	if err := repo.InsertMortgageApp(ctx, app); err != nil {
		t.Fatalf("mortgage: %v", err)
	}
	app.Status = mortgage.AppApproved
	app.DecidedAt = &now
	if err := repo.UpdateMortgageStatus(ctx, app); err != nil {
		t.Fatalf("approve: %v", err)
	}
	apps, _ := repo.MortgageApps(ctx, tenant, "m1")
	if len(apps) != 1 || apps[0].Status != mortgage.AppApproved {
		t.Fatalf("apps: %v", apps)
	}

	// Escalation: upsert idempotent.
	ru := escalation.Rule{ID: "esc-" + sfx, TenantID: tenant, MemberID: "m1",
		BaseRateBps: 1800, StepBps: 100, MaxBps: 2500, CurrentBps: 1800, CreatedAt: now}
	if err := repo.UpsertEscalationRule(ctx, ru); err != nil {
		t.Fatalf("esc upsert: %v", err)
	}
	ru.CurrentBps = 1900
	if err := repo.UpsertEscalationRule(ctx, ru); err != nil {
		t.Fatalf("esc re-upsert: %v", err)
	}
	rules, _ := repo.EscalationRules(ctx, tenant)
	if len(rules) != 1 || rules[0].CurrentBps != 1900 {
		t.Fatalf("rules: %v", rules)
	}
}
