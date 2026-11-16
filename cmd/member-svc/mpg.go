package main

// Tranche 15a: Minimum Pension Guarantee (MPG) + Pension Protection Fund
// (PPF) API surface (PRA 2014 s.84; PenCom ₦52,500 MPG directive,
// November 2025). Handlers operate on the in-memory mpg.Engine and write
// through to Postgres (store.MPGRepo) when DATABASE_URL is set.

import (
	"net/http"
	"strings"
	"time"

	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/mpg"
)

// mpgFloor returns the current MPG floor and its actuarial-review metadata.
func (d *deps) mpgFloor(w http.ResponseWriter, r *http.Request) {
	httpsrv.WriteJSON(w, 200, d.mpgEngine.Floor())
}

// createPPFContribution books a receipt into the Pension Protection Fund
// (FG / INDUSTRY / STATE / EMPLOYER). STATE contributions unlock MPG
// access for that state's retirees (the "states must contribute" rule).
func (d *deps) createPPFContribution(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Source         string     `json:"source"`
		ContributorRef string     `json:"contributor_ref"`
		AmountKobo     money.Kobo `json:"amount_kobo"`
		Period         string     `json:"period"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	c, err := d.mpgEngine.RecordContribution(tenant, mpg.PPFSource(strings.ToUpper(in.Source)),
		strings.TrimSpace(in.ContributorRef), in.AmountKobo, strings.TrimSpace(in.Period), time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "PPF_REJECTED", err)
		return
	}
	if d.mpgRepo != nil {
		if err := d.mpgRepo.InsertContribution(r.Context(), c); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "PPF_PERSIST_FAILED", err)
			return
		}
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "ppf.contribution.recorded", EntityType: "ppf_contribution", EntityID: c.ID, After: map[string]any{"contribution": c}})
	httpsrv.WriteJSON(w, 201, c)
}

// listPPF returns the PPF ledger and per-source aggregates for the tenant.
func (d *deps) listPPF(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	if d.mpgRepo != nil {
		contribs, err := d.mpgRepo.Contributions(r.Context(), tenant)
		if err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "PPF_READ_FAILED", err)
			return
		}
		agg, err := d.mpgRepo.PPFAggregates(r.Context(), tenant)
		if err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "PPF_READ_FAILED", err)
			return
		}
		httpsrv.WriteJSON(w, 200, map[string]any{"contributions": contribs, "aggregates_kobo": agg})
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{
		"contributions":   d.mpgEngine.Contributions(tenant),
		"aggregates_kobo": d.mpgEngine.PPFAggregates(tenant),
	})
}

// assessMPG runs the MPG assessment over a payroll batch for one period:
// every retiree below the ₦52,500 floor receives a PPF-funded top-up,
// except STATE-sector retirees whose state has not contributed (gated).
// Assessments persist to Postgres when DATABASE_URL is set.
func (d *deps) assessMPG(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Period string `json:"period"`
		Rows   []struct {
			RSAID              string     `json:"rsa_id"`
			Sector             string     `json:"sector"`
			StateCode          string     `json:"state_code"`
			MonthlyPensionKobo money.Kobo `json:"monthly_pension_kobo"`
		} `json:"rows"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	if strings.TrimSpace(in.Period) == "" || len(in.Rows) == 0 {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", errStr("period and rows are required"))
		return
	}
	rows := make([]mpg.PayrollRow, len(in.Rows))
	for i, rw := range in.Rows {
		rows[i] = mpg.PayrollRow{
			RSAID: rw.RSAID, Sector: mpg.Sector(strings.ToUpper(rw.Sector)),
			StateCode: strings.ToUpper(rw.StateCode), MonthlyPension: rw.MonthlyPensionKobo,
		}
	}
	tenant := httpsrv.TenantOf(r)
	assessments, total := d.mpgEngine.AssessPayroll(tenant, in.Period, rows, time.Now())
	if d.mpgRepo != nil {
		for _, a := range assessments {
			if err := d.mpgRepo.InsertAssessment(r.Context(), a); err != nil {
				httpsrv.WriteError(w, http.StatusInternalServerError, "MPG_PERSIST_FAILED", err)
				return
			}
		}
	}
	d.mpgMu.Lock()
	d.mpgAssess = append(d.mpgAssess, assessments...)
	d.mpgMu.Unlock()
	var eligible, gated int
	for _, a := range assessments {
		if a.Eligible {
			eligible++
		}
		if a.Gated {
			gated++
		}
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "mpg.payroll.assessed", EntityType: "mpg_assessment", EntityID: in.Period, After: map[string]any{"count": len(assessments), "eligible": eligible, "gated": gated, "total_topup_kobo": total}})
	httpsrv.WriteJSON(w, 200, map[string]any{
		"period": in.Period, "assessments": assessments,
		"eligible": eligible, "gated": gated, "total_topup_kobo": total,
	})
}

// listMPG returns MPG assessments for a period (Postgres when wired;
// otherwise the in-memory assessments accumulated this process).
func (d *deps) listMPG(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	period := strings.TrimSpace(r.URL.Query().Get("period"))
	if d.mpgRepo != nil && period != "" {
		out, err := d.mpgRepo.Assessments(r.Context(), tenant, period)
		if err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "MPG_READ_FAILED", err)
			return
		}
		httpsrv.WriteJSON(w, 200, map[string]any{"period": period, "assessments": out})
		return
	}
	d.mpgMu.Lock()
	defer d.mpgMu.Unlock()
	out := make([]mpg.Assessment, 0, len(d.mpgAssess))
	for _, a := range d.mpgAssess {
		if a.TenantID == tenant && (period == "" || a.Period == period) {
			out = append(out, a)
		}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"period": period, "assessments": out})
}

type errStr string

func (e errStr) Error() string { return string(e) }
