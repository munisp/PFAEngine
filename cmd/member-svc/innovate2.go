package main

// Tranche 17 (continued): mortgage, claimsfast, succession, fraud,
// readiness handlers.

import (
	"net/http"
	"strings"
	"time"

	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/fraud"
	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/mortgage"
	"github.com/munisp/pfaengine/internal/readiness"
	"github.com/munisp/pfaengine/internal/succession"
)

// ---- 6. mortgage (PRA s.89(2)) -------------------------------------------

func (d *deps) mortgageApply(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MemberID       string     `json:"member_id"`
		RSABalanceKobo money.Kobo `json:"rsa_balance_kobo"`
		AmountKobo     money.Kobo `json:"amount_kobo"`
		LenderRef      string     `json:"lender_ref"`
		Employed       bool       `json:"employed"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	a, err := d.mortgage.Apply(tenant, in.MemberID, in.RSABalanceKobo, in.AmountKobo, in.LenderRef, in.Employed, time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "MORTGAGE_REJECTED", err)
		return
	}
	if d.innovateRepo != nil {
		if err := d.innovateRepo.InsertMortgageApp(r.Context(), a); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "MORTGAGE_PERSIST_FAILED", err)
			return
		}
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "mortgage.applied", EntityType: "mortgage_application", EntityID: a.ID, After: map[string]any{"application": a}})
	httpsrv.WriteJSON(w, 201, a)
}

func (d *deps) mortgageDecide(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID     string `json:"id"`
		Action string `json:"action"` // approve | reject | disburse
		Reason string `json:"reason"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	var a mortgage.Application
	var err error
	switch in.Action {
	case "approve":
		a, err = d.mortgage.Approve(tenant, in.ID, time.Now())
	case "reject":
		a, err = d.mortgage.Reject(tenant, in.ID, in.Reason, time.Now())
	case "disburse":
		a, err = d.mortgage.Disburse(tenant, in.ID, time.Now())
	default:
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", errStr("action must be approve|reject|disburse"))
		return
	}
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "MORTGAGE_REJECTED", err)
		return
	}
	if d.innovateRepo != nil {
		if err := d.innovateRepo.UpdateMortgageStatus(r.Context(), a); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "MORTGAGE_PERSIST_FAILED", err)
			return
		}
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "mortgage." + in.Action, EntityType: "mortgage_application", EntityID: a.ID, After: map[string]any{"application": a}})
	httpsrv.WriteJSON(w, 200, a)
}

func (d *deps) mortgageList(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	memberID := r.URL.Query().Get("member_id")
	if d.innovateRepo != nil {
		apps, err := d.innovateRepo.MortgageApps(r.Context(), tenant, memberID)
		if err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "MORTGAGE_READ_FAILED", err)
			return
		}
		httpsrv.WriteJSON(w, 200, map[string]any{"applications": apps})
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"applications": d.mortgage.List(tenant, memberID)})
}

// ---- 7. claimsfast --------------------------------------------------------

func (d *deps) claimsOpen(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RSAID string `json:"rsa_id"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	httpsrv.WriteJSON(w, 201, d.claimsfast.Open(httpsrv.TenantOf(r), in.RSAID, time.Now()))
}

func (d *deps) claimsComplete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RSAID string `json:"rsa_id"`
		Item  string `json:"item"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	p, err := d.claimsfast.Complete(httpsrv.TenantOf(r), in.RSAID, strings.ToUpper(in.Item))
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "CLAIMS_REJECTED", err)
		return
	}
	httpsrv.WriteJSON(w, 200, p)
}

func (d *deps) claimsGet(w http.ResponseWriter, r *http.Request) {
	p, ok := d.claimsfast.Get(httpsrv.TenantOf(r), r.URL.Query().Get("rsa_id"))
	if !ok {
		httpsrv.WriteError(w, http.StatusNotFound, "CLAIMS_NOT_FOUND", errStr("no pack"))
		return
	}
	httpsrv.WriteJSON(w, 200, p)
}

// ---- 8. succession ---------------------------------------------------------

func (d *deps) successionNominate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MemberID      string                   `json:"member_id"`
		Beneficiaries []succession.Beneficiary `json:"beneficiaries"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	n, err := d.succession.Nominate(tenant, in.MemberID, in.Beneficiaries, time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "SUCCESSION_REJECTED", err)
		return
	}
	if d.innovateRepo != nil {
		if err := d.innovateRepo.InsertNomination(r.Context(), n); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "SUCCESSION_PERSIST_FAILED", err)
			return
		}
	}
	httpsrv.WriteJSON(w, 201, n)
}

func (d *deps) successionGet(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	memberID := r.URL.Query().Get("member_id")
	if d.innovateRepo != nil {
		n, err := d.innovateRepo.CurrentNomination(r.Context(), tenant, memberID)
		if err != nil {
			httpsrv.WriteError(w, http.StatusNotFound, "SUCCESSION_NOT_FOUND", err)
			return
		}
		httpsrv.WriteJSON(w, 200, n)
		return
	}
	n, err := d.succession.Current(tenant, memberID)
	if err != nil {
		httpsrv.WriteError(w, http.StatusNotFound, "SUCCESSION_NOT_FOUND", err)
		return
	}
	httpsrv.WriteJSON(w, 200, n)
}

func (d *deps) successionDue(w http.ResponseWriter, r *http.Request) {
	httpsrv.WriteJSON(w, 200, map[string]any{
		"due": d.succession.DueForReconfirm(httpsrv.TenantOf(r), time.Now())})
}

// ---- 9. fraud ---------------------------------------------------------------

func (d *deps) fraudScreen(w http.ResponseWriter, r *http.Request) {
	var tx fraud.Tx
	if err := decode(r, &tx); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	tx.TenantID = tenant
	tx.At = time.Now()
	score, signals, c := d.fraud.Score(tx)
	if c != nil && d.innovateRepo != nil {
		if err := d.innovateRepo.InsertFraudCase(r.Context(), *c); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "FRAUD_PERSIST_FAILED", err)
			return
		}
	}
	if c != nil {
		d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "fraud.case.opened", EntityType: "fraud_case", EntityID: c.ID, After: map[string]any{"case": c}})
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"score": score, "signals": signals, "case": c})
}

func (d *deps) fraudCases(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	openOnly := r.URL.Query().Get("open") == "1"
	if d.innovateRepo != nil {
		cases, err := d.innovateRepo.FraudCases(r.Context(), tenant, openOnly)
		if err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "FRAUD_READ_FAILED", err)
			return
		}
		httpsrv.WriteJSON(w, 200, map[string]any{"cases": cases})
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"cases": d.fraud.Cases(tenant, openOnly)})
}

func (d *deps) fraudResolve(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CaseID string `json:"case_id"`
		Status string `json:"status"` // CONFIRMED_FRAUD | CLEARED
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	c, err := d.fraud.Resolve(tenant, in.CaseID, fraud.CaseStatus(strings.ToUpper(in.Status)))
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "FRAUD_REJECTED", err)
		return
	}
	if d.innovateRepo != nil {
		if err := d.innovateRepo.ResolveFraudCase(r.Context(), tenant, in.CaseID, c.Status); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "FRAUD_PERSIST_FAILED", err)
			return
		}
	}
	httpsrv.WriteJSON(w, 200, c)
}

// ---- 10. readiness ------------------------------------------------------------

func (d *deps) readinessCompute(w http.ResponseWriter, r *http.Request) {
	var in struct {
		EmployerID string           `json:"employer_id"`
		Period     string           `json:"period"`
		Inputs     readiness.Inputs `json:"inputs"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	rep := d.readiness.Compute(httpsrv.TenantOf(r), in.EmployerID, in.Period, in.Inputs, time.Now())
	httpsrv.WriteJSON(w, 201, rep)
}

func (d *deps) readinessList(w http.ResponseWriter, r *http.Request) {
	httpsrv.WriteJSON(w, 200, map[string]any{"reports": d.readiness.Reports(
		httpsrv.TenantOf(r), r.URL.Query().Get("employer_id"))})
}
