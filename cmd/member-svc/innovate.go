package main

// Tranche 17: the 15-innovation API surface. Stateless computational
// engines (advisory) answer directly; stateful domains run in-memory
// engines with write-through to store.InnovateRepo when DATABASE_URL is
// set (fraud cases, API keys, webhooks, agents, nominations, mortgage
// applications, escalation rules).

import (
	"net/http"
	"strings"
	"time"

	"github.com/munisp/pfaengine/internal/advisory"
	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/escalation"
	"github.com/munisp/pfaengine/internal/glidepath"
	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/literacy"
	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/regfile"
)

// ---- 1. advisory (stateless) --------------------------------------------

func (d *deps) advisoryProject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BalanceKobo     money.Kobo `json:"balance_kobo"`
		MonthlyKobo     money.Kobo `json:"monthly_contrib_kobo"`
		Years           int        `json:"years_to_retire"`
		AnnualReturnBps int64      `json:"annual_return_bps"`
		SalaryKobo      money.Kobo `json:"salary_kobo"`
		ReplacementBps  int64      `json:"replacement_bps"`
		AnnuityMonths   int        `json:"annuity_months"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	if in.AnnuityMonths == 0 {
		in.AnnuityMonths = 240
	}
	p, err := advisory.Project(in.BalanceKobo, in.MonthlyKobo, in.Years, in.AnnualReturnBps,
		in.SalaryKobo, in.ReplacementBps, in.AnnuityMonths)
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "ADVISORY_REJECTED", err)
		return
	}
	httpsrv.WriteJSON(w, 200, p)
}

// ---- 2. escalation -------------------------------------------------------

func (d *deps) escalationEnrol(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MemberID string `json:"member_id"`
		BaseBps  int64  `json:"base_rate_bps"`
		StepBps  int64  `json:"step_bps"`
		MaxBps   int64  `json:"max_bps"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	ru, err := d.escalation.Enrol(tenant, in.MemberID, in.BaseBps, in.StepBps, in.MaxBps, time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "ESCALATION_REJECTED", err)
		return
	}
	if d.innovateRepo != nil {
		if err := d.innovateRepo.UpsertEscalationRule(r.Context(), ru); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "ESCALATION_PERSIST_FAILED", err)
			return
		}
	}
	httpsrv.WriteJSON(w, 201, ru)
}

func (d *deps) escalationTick(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	evs := d.escalation.Tick(tenant, time.Now())
	if d.innovateRepo != nil {
		for _, ev := range evs {
			if ru, ok := d.escalation.Rule(tenant, ev.MemberID); ok {
				_ = d.innovateRepo.UpsertEscalationRule(r.Context(), ru)
			}
		}
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "escalation.tick", EntityType: "escalation_rule", EntityID: tenant, After: map[string]any{"events": evs}})
	httpsrv.WriteJSON(w, 200, map[string]any{"events": evs})
}

func (d *deps) escalationOptOut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MemberID string `json:"member_id"`
		Reason   string `json:"reason"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	if err := d.escalation.OptOut(tenant, in.MemberID, in.Reason, time.Now()); err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "ESCALATION_REJECTED", err)
		return
	}
	if d.innovateRepo != nil {
		if ru, ok := d.escalation.Rule(tenant, in.MemberID); ok {
			_ = d.innovateRepo.UpsertEscalationRule(r.Context(), ru)
		}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"ok": true})
}

func (d *deps) escalationList(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	if d.innovateRepo != nil {
		rules, err := d.innovateRepo.EscalationRules(r.Context(), tenant)
		if err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "ESCALATION_READ_FAILED", err)
			return
		}
		httpsrv.WriteJSON(w, 200, map[string]any{"rules": rules})
		return
	}
	memberID := r.URL.Query().Get("member_id")
	if ru, ok := d.escalation.Rule(tenant, memberID); ok {
		httpsrv.WriteJSON(w, 200, map[string]any{"rules": []escalation.Rule{ru}})
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"rules": []escalation.Rule{}})
}

// ---- 3. roundup ----------------------------------------------------------

func (d *deps) roundupOpen(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MemberID       string     `json:"member_id"`
		StepKobo       money.Kobo `json:"step_kobo"`
		MonthlyCapKobo money.Kobo `json:"monthly_cap_kobo"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	wlt, err := d.roundup.Open(httpsrv.TenantOf(r), in.MemberID, in.StepKobo, in.MonthlyCapKobo, time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "ROUNDUP_REJECTED", err)
		return
	}
	httpsrv.WriteJSON(w, 201, wlt)
}

func (d *deps) roundupSweep(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MemberID string     `json:"member_id"`
		TxKobo   money.Kobo `json:"tx_amount_kobo"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	s, err := d.roundup.SweepTx(httpsrv.TenantOf(r), in.MemberID, in.TxKobo, time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "ROUNDUP_REJECTED", err)
		return
	}
	httpsrv.WriteJSON(w, 201, s)
}

func (d *deps) roundupWallet(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	wlt, ok := d.roundup.Wallet(tenant, r.URL.Query().Get("member_id"))
	if !ok {
		httpsrv.WriteError(w, http.StatusNotFound, "ROUNDUP_NOT_FOUND", errStr("no wallet"))
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"wallet": wlt, "sweeps": d.roundup.Sweeps(tenant, wlt.MemberID)})
}

// ---- 4. glidepath --------------------------------------------------------

func (d *deps) glidepathRegister(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MemberID   string `json:"member_id"`
		AgeYears   int    `json:"age_years"`
		Retired    bool   `json:"retired"`
		Aggressive bool   `json:"aggressive"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	m, err := d.glidepath.Register(httpsrv.TenantOf(r), in.MemberID, in.AgeYears, in.Retired, in.Aggressive, time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "GLIDEPATH_REJECTED", err)
		return
	}
	httpsrv.WriteJSON(w, 201, m)
}

func (d *deps) glidepathBirthday(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MemberID string `json:"member_id"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	sw, err := d.glidepath.Birthday(httpsrv.TenantOf(r), in.MemberID, time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "GLIDEPATH_REJECTED", err)
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"switch": sw})
}

func (d *deps) glidepathElect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MemberID string `json:"member_id"`
		Fund     string `json:"fund"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	sw, err := d.glidepath.Elect(httpsrv.TenantOf(r), in.MemberID, glidepathFund(in.Fund), time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "GLIDEPATH_REJECTED", err)
		return
	}
	httpsrv.WriteJSON(w, 200, sw)
}

func (d *deps) glidepathGet(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	memberID := r.URL.Query().Get("member_id")
	m, ok := d.glidepath.Member(tenant, memberID)
	if !ok {
		httpsrv.WriteError(w, http.StatusNotFound, "GLIDEPATH_NOT_FOUND", errStr("no member"))
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"member": m, "switches": d.glidepath.Switches(tenant, memberID)})
}

// ---- 5. literacy ---------------------------------------------------------

func (d *deps) literacyCatalogue(w http.ResponseWriter, r *http.Request) {
	httpsrv.WriteJSON(w, 200, map[string]any{"lessons": d.literacy.Catalogue(
		literacy.Lang(r.URL.Query().Get("lang")), r.URL.Query().Get("topic"))})
}

func (d *deps) literacyAnswer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MemberID string `json:"member_id"`
		LessonID string `json:"lesson_id"`
		Option   int    `json:"option"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	p, err := d.literacy.Answer(httpsrv.TenantOf(r), in.MemberID, in.LessonID, in.Option, time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "LITERACY_REJECTED", err)
		return
	}
	httpsrv.WriteJSON(w, 200, p)
}

func (d *deps) literacyScore(w http.ResponseWriter, r *http.Request) {
	passed, total := d.literacy.Score(httpsrv.TenantOf(r), r.URL.Query().Get("member_id"))
	httpsrv.WriteJSON(w, 200, map[string]any{"passed": passed, "total": total})
}

// glidepathFund parses a fund string for the glidepath API.
func glidepathFund(s string) glidepath.Fund { return glidepath.Fund(strings.ToUpper(s)) }

// regfileKind parses a filing kind string for the regfile API.
func regfileKind(s string) regfile.FilingKind { return regfile.FilingKind(strings.ToUpper(s)) }
