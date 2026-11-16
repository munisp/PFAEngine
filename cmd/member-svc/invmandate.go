package main

// Tranche 16b: infrastructure-investment mandate API surface (e.g. the
// FSD Africa–backed $750m pension infrastructure platform). Handlers
// operate on the in-memory invmandate.Engine and write through to
// Postgres (store.InvMandateRepo) when DATABASE_URL is set.

import (
	"net/http"
	"strings"
	"time"

	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/invmandate"
	"github.com/munisp/pfaengine/internal/money"
)

// createMandate registers an investment mandate (asset class, partner,
// target, currency, optional per-fund cap override).
func (d *deps) createMandate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string `json:"name"`
		AssetClass  string `json:"asset_class"`
		Partner     string `json:"partner"`
		Currency    string `json:"currency"`
		TargetMinor int64  `json:"target_minor"`
		CapBps      int64  `json:"cap_bps"` // 0 => default 5%
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	m, err := d.invmandate.CreateMandate(tenant, strings.TrimSpace(in.Name),
		invmandate.AssetClass(strings.ToUpper(in.AssetClass)), strings.TrimSpace(in.Partner),
		strings.ToUpper(strings.TrimSpace(in.Currency)), in.TargetMinor, in.CapBps, time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "MANDATE_REJECTED", err)
		return
	}
	if d.invmandateRepo != nil {
		if err := d.invmandateRepo.InsertMandate(r.Context(), m); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "MANDATE_PERSIST_FAILED", err)
			return
		}
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "invmandate.created", EntityType: "inv_mandate", EntityID: m.ID, After: map[string]any{"mandate": m}})
	httpsrv.WriteJSON(w, 201, m)
}

// listMandates returns the tenant's mandates.
func (d *deps) listMandates(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	if d.invmandateRepo != nil {
		ms, err := d.invmandateRepo.Mandates(r.Context(), tenant)
		if err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "MANDATE_READ_FAILED", err)
			return
		}
		httpsrv.WriteJSON(w, 200, map[string]any{"mandates": ms})
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"mandates": d.invmandate.Mandates(tenant)})
}

// setFundAUM registers an RSA fund's AUM (kobo) for cap checks, and the
// FX rate for USD-denominated mandates.
func (d *deps) setInvConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FundID       string     `json:"fund_id"`         // empty => only FX update
		AUMKobo      money.Kobo `json:"aum_kobo"`        // fund AUM
		FXKoboPerUSD int64      `json:"fx_kobo_per_usd"` // 0 => unchanged
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	if in.FundID != "" {
		d.invmandate.SetFundAUM(tenant, strings.TrimSpace(in.FundID), in.AUMKobo)
	}
	if in.FXKoboPerUSD > 0 {
		d.invmandate.SetFX(in.FXKoboPerUSD)
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "invmandate.config.set", EntityType: "inv_config", EntityID: in.FundID, After: map[string]any{"fund_id": in.FundID, "aum_kobo": in.AUMKobo, "fx_kobo_per_usd": in.FXKoboPerUSD}})
	httpsrv.WriteJSON(w, 200, map[string]any{"ok": true})
}

// createCommitment books a fund's commitment after the mandate-open,
// target-oversubscription and per-fund-cap gates. WORM.
func (d *deps) createCommitment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MandateID   string `json:"mandate_id"`
		FundID      string `json:"fund_id"`
		AmountMinor int64  `json:"amount_minor"`
		Currency    string `json:"currency"`
		Ref         string `json:"ref"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	c, err := d.invmandate.RecordCommitment(tenant, strings.TrimSpace(in.MandateID),
		strings.TrimSpace(in.FundID), in.AmountMinor, strings.ToUpper(strings.TrimSpace(in.Currency)),
		strings.TrimSpace(in.Ref), time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "COMMITMENT_REJECTED", err)
		return
	}
	if d.invmandateRepo != nil {
		if err := d.invmandateRepo.InsertCommitment(r.Context(), c); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "COMMITMENT_PERSIST_FAILED", err)
			return
		}
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "invmandate.commitment.recorded", EntityType: "inv_commitment", EntityID: c.ID, After: map[string]any{"commitment": c}})
	httpsrv.WriteJSON(w, 201, c)
}

// listCommitments lists commitments, optionally ?mandate_id= filtered.
func (d *deps) listCommitments(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	mandateID := r.URL.Query().Get("mandate_id")
	if d.invmandateRepo != nil {
		cs, err := d.invmandateRepo.Commitments(r.Context(), tenant, mandateID)
		if err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "COMMITMENT_READ_FAILED", err)
			return
		}
		httpsrv.WriteJSON(w, 200, map[string]any{"commitments": cs})
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"commitments": d.invmandate.Commitments(tenant, mandateID)})
}

// createDeployment draws down committed capital into a vehicle. WORM.
func (d *deps) createDeployment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MandateID   string `json:"mandate_id"`
		AmountMinor int64  `json:"amount_minor"`
		Currency    string `json:"currency"`
		Vehicle     string `json:"vehicle"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	dep, err := d.invmandate.RecordDeployment(tenant, strings.TrimSpace(in.MandateID),
		in.AmountMinor, strings.ToUpper(strings.TrimSpace(in.Currency)),
		strings.TrimSpace(in.Vehicle), time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "DEPLOYMENT_REJECTED", err)
		return
	}
	if d.invmandateRepo != nil {
		if err := d.invmandateRepo.InsertDeployment(r.Context(), dep); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "DEPLOYMENT_PERSIST_FAILED", err)
			return
		}
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "invmandate.deployment.recorded", EntityType: "inv_deployment", EntityID: dep.ID, After: map[string]any{"deployment": dep}})
	httpsrv.WriteJSON(w, 201, dep)
}

// listDeployments lists deployments, optionally ?mandate_id= filtered.
func (d *deps) listDeployments(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	mandateID := r.URL.Query().Get("mandate_id")
	if d.invmandateRepo != nil {
		ds, err := d.invmandateRepo.Deployments(r.Context(), tenant, mandateID)
		if err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "DEPLOYMENT_READ_FAILED", err)
			return
		}
		httpsrv.WriteJSON(w, 200, map[string]any{"deployments": ds})
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"deployments": d.invmandate.Deployments(tenant, mandateID)})
}

// invExposure reports one mandate's subscription/drawdown state.
// ?mandate_id= is required. When Postgres is wired and the in-memory
// engine has no record (e.g. after a restart), the report is computed
// from the durable ledger instead.
func (d *deps) invExposure(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	mandateID := r.URL.Query().Get("mandate_id")
	if mandateID == "" {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", errStr("mandate_id query parameter required"))
		return
	}
	ex, err := d.invmandate.Exposure(tenant, mandateID)
	if err != nil && d.invmandateRepo != nil {
		if ex2, rerr := d.exposureFromRepo(r, tenant, mandateID); rerr == nil {
			httpsrv.WriteJSON(w, 200, ex2)
			return
		}
	}
	if err != nil {
		httpsrv.WriteError(w, http.StatusNotFound, "MANDATE_NOT_FOUND", err)
		return
	}
	httpsrv.WriteJSON(w, 200, ex)
}

// exposureFromRepo rebuilds a mandate's exposure from Postgres.
func (d *deps) exposureFromRepo(r *http.Request, tenant, mandateID string) (invmandate.Exposure, error) {
	ms, err := d.invmandateRepo.Mandates(r.Context(), tenant)
	if err != nil {
		return invmandate.Exposure{}, err
	}
	var m *invmandate.Mandate
	for i := range ms {
		if ms[i].ID == mandateID {
			m = &ms[i]
			break
		}
	}
	if m == nil {
		return invmandate.Exposure{}, invmandate.ErrNoMandate
	}
	cs, err := d.invmandateRepo.Commitments(r.Context(), tenant, mandateID)
	if err != nil {
		return invmandate.Exposure{}, err
	}
	ds, err := d.invmandateRepo.Deployments(r.Context(), tenant, mandateID)
	if err != nil {
		return invmandate.Exposure{}, err
	}
	var committed, deployed int64
	for _, c := range cs {
		committed += c.AmountMinor // uniform currency per mandate
	}
	for _, dep := range ds {
		deployed += dep.AmountMinor
	}
	return invmandate.Exposure{
		MandateID:      mandateID,
		Currency:       m.Currency,
		TargetMinor:    m.TargetMinor,
		CommittedMinor: committed,
		DeployedMinor:  deployed,
		HeadroomMinor:  m.TargetMinor - committed,
		UndrawnMinor:   committed - deployed,
		FillBps:        committed * 10000 / m.TargetMinor,
	}, nil
}
