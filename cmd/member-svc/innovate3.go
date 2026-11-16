package main

// Tranche 17 (continued): diaspora, agents, embedded, regfile, esg.

import (
	"net/http"
	"strings"
	"time"

	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/embedded"
	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/money"
)

// ---- 11. diaspora ----------------------------------------------------------

func (d *deps) diasporaEnrol(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RSAID       string `json:"rsa_id"`
		Country     string `json:"country"`
		PassportRef string `json:"passport_ref"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	m, err := d.diaspora.Enrol(httpsrv.TenantOf(r), in.RSAID, strings.ToUpper(in.Country), in.PassportRef, time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "DIASPORA_REJECTED", err)
		return
	}
	httpsrv.WriteJSON(w, 201, m)
}

func (d *deps) diasporaSetRate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Currency    string `json:"currency"`
		KoboPerUnit int64  `json:"kobo_per_unit"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	d.diaspora.SetRate(strings.ToUpper(in.Currency), in.KoboPerUnit)
	httpsrv.WriteJSON(w, 200, map[string]any{"ok": true})
}

func (d *deps) diasporaContribute(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RSAID       string `json:"rsa_id"`
		Currency    string `json:"currency"`
		AmountMinor int64  `json:"amount_minor"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	c, err := d.diaspora.Contribute(tenant, in.RSAID, strings.ToUpper(in.Currency), in.AmountMinor, time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "DIASPORA_REJECTED", err)
		return
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "diaspora.contribution", EntityType: "diaspora_contribution", EntityID: c.ID, After: map[string]any{"contribution": c}})
	httpsrv.WriteJSON(w, 201, c)
}

func (d *deps) diasporaList(w http.ResponseWriter, r *http.Request) {
	cs, total := d.diaspora.Contributions(httpsrv.TenantOf(r), r.URL.Query().Get("rsa_id"))
	httpsrv.WriteJSON(w, 200, map[string]any{"contributions": cs, "total_kobo": total})
}

// ---- 12. agents --------------------------------------------------------------

func (d *deps) agentsAccredit(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name          string `json:"name"`
		Phone         string `json:"phone"`
		StateCode     string `json:"state_code"`
		CommissionBps int64  `json:"commission_bps"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	a := d.agents.Accredit(tenant, in.Name, in.Phone, strings.ToUpper(in.StateCode), in.CommissionBps, time.Now())
	if d.innovateRepo != nil {
		if err := d.innovateRepo.InsertAgent(r.Context(), a); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "AGENTS_PERSIST_FAILED", err)
			return
		}
	}
	httpsrv.WriteJSON(w, 201, a)
}

func (d *deps) agentsEnrol(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AgentID string `json:"agent_id"`
		RSAID   string `json:"rsa_id"`
		Name    string `json:"name"`
		Phone   string `json:"phone"`
		Trade   string `json:"trade"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	en, err := d.agents.Enrol(tenant, in.AgentID, in.RSAID, in.Name, in.Phone, in.Trade, time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "AGENTS_REJECTED", err)
		return
	}
	if d.innovateRepo != nil {
		if err := d.innovateRepo.InsertAgentEnrolment(r.Context(), en); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "AGENTS_PERSIST_FAILED", err)
			return
		}
	}
	httpsrv.WriteJSON(w, 201, en)
}

func (d *deps) agentsCommission(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AgentID          string     `json:"agent_id"`
		ContributionKobo money.Kobo `json:"contribution_kobo"`
		Period           string     `json:"period"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	c, err := d.agents.PayCommission(tenant, in.AgentID, in.ContributionKobo, in.Period, time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "AGENTS_REJECTED", err)
		return
	}
	if d.innovateRepo != nil {
		if err := d.innovateRepo.InsertCommission(r.Context(), c); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "AGENTS_PERSIST_FAILED", err)
			return
		}
	}
	httpsrv.WriteJSON(w, 201, c)
}

func (d *deps) agentsLeaderboard(w http.ResponseWriter, r *http.Request) {
	httpsrv.WriteJSON(w, 200, map[string]any{"leaderboard": d.agents.Leaderboard(httpsrv.TenantOf(r))})
}

// ---- 13. embedded --------------------------------------------------------------

func (d *deps) embeddedIssueKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Label   string   `json:"label"`
		Scopes  []string `json:"scopes"`
		Sandbox bool     `json:"sandbox"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	var scopes []embedded.Scope
	for _, s := range in.Scopes {
		scopes = append(scopes, embedded.Scope(s))
	}
	tenant := httpsrv.TenantOf(r)
	k, token, err := d.embedded.IssueKey(tenant, in.Label, scopes, in.Sandbox, time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusInternalServerError, "EMBEDDED_FAILED", err)
		return
	}
	if d.innovateRepo != nil {
		if err := d.innovateRepo.InsertAPIKey(r.Context(), k); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "EMBEDDED_PERSIST_FAILED", err)
			return
		}
	}
	// Token returned exactly once; only the hash persists.
	httpsrv.WriteJSON(w, 201, map[string]any{"key": k, "token": token})
}

func (d *deps) embeddedSubscribe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	wh := d.embedded.Subscribe(tenant, in.URL, in.Events, time.Now())
	if d.innovateRepo != nil {
		if err := d.innovateRepo.InsertWebhook(r.Context(), wh); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "EMBEDDED_PERSIST_FAILED", err)
			return
		}
	}
	httpsrv.WriteJSON(w, 201, wh)
}

func (d *deps) embeddedDispatch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Event string `json:"event"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	ds := d.embedded.Dispatch(httpsrv.TenantOf(r), in.Event, time.Now())
	httpsrv.WriteJSON(w, 200, map[string]any{"deliveries": ds})
}

// ---- 14. regfile ---------------------------------------------------------------

func (d *deps) regfileSchedule(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind    string    `json:"kind"`
		Period  string    `json:"period"`
		DueDate time.Time `json:"due_date"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	f := d.regfile.Schedule(httpsrv.TenantOf(r), regfileKind(in.Kind), in.Period, in.DueDate)
	httpsrv.WriteJSON(w, 201, f)
}

func (d *deps) regfileGenerate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID      string `json:"id"`
		Payload string `json:"payload"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	f, err := d.regfile.Generate(httpsrv.TenantOf(r), in.ID, in.Payload, time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "REGFILE_REJECTED", err)
		return
	}
	httpsrv.WriteJSON(w, 200, f)
}

func (d *deps) regfileFile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID              string     `json:"id"`
		OutstandingKobo money.Kobo `json:"outstanding_kobo"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	f, err := d.regfile.File(httpsrv.TenantOf(r), in.ID, in.OutstandingKobo, time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "REGFILE_REJECTED", err)
		return
	}
	httpsrv.WriteJSON(w, 200, f)
}

func (d *deps) regfileList(w http.ResponseWriter, r *http.Request) {
	httpsrv.WriteJSON(w, 200, map[string]any{"filings": d.regfile.List(httpsrv.TenantOf(r))})
}

// ---- 15. esg ---------------------------------------------------------------------

func (d *deps) esgReport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MandateID        string `json:"mandate_id"`
		DeploymentID     string `json:"deployment_id"`
		JobsCreated      int64  `json:"jobs_created"`
		Megawatts        int64  `json:"megawatts"`
		RoadKm           int64  `json:"road_km"`
		HomesFinanced    int64  `json:"homes_financed"`
		EnvironmentScore int    `json:"environment_score"`
		SocialScore      int    `json:"social_score"`
		GovernanceScore  int    `json:"governance_score"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	m := d.esg.Report(httpsrv.TenantOf(r), in.MandateID, in.DeploymentID, in.JobsCreated,
		in.Megawatts, in.RoadKm, in.HomesFinanced, in.EnvironmentScore, in.SocialScore, in.GovernanceScore, time.Now())
	httpsrv.WriteJSON(w, 201, m)
}

func (d *deps) esgRollup(w http.ResponseWriter, r *http.Request) {
	httpsrv.WriteJSON(w, 200, d.esg.Rollup(httpsrv.TenantOf(r), r.URL.Query().Get("mandate_id")))
}
