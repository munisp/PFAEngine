package main

// Tranche 20: unified case management + CRM handlers. The casemgmt engine
// is the single operational case store across all domains; the crm engine
// composes the member 360 view, interaction timeline, segments, campaigns
// and the PenCom complaints register. Cross-domain hooks (fraud, ML,
// claims, recon) auto-open cases so nothing operational lives outside the
// engine.

import (
	"net/http"
	"strings"
	"time"

	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/casemgmt"
	"github.com/munisp/pfaengine/internal/crm"
	"github.com/munisp/pfaengine/internal/httpsrv"
)

// ---- staff: case workbench -------------------------------------------------

func (d *deps) casesList(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	q := r.URL.Query()
	f := casemgmt.Filter{
		Assignee: q.Get("assignee"),
		MemberID: q.Get("member_id"),
		OpenOnly: q.Get("open") == "1",
	}
	if s := q.Get("status"); s != "" {
		for _, x := range strings.Split(s, ",") {
			f.Status = append(f.Status, casemgmt.Status(strings.ToUpper(strings.TrimSpace(x))))
		}
	}
	if s := q.Get("type"); s != "" {
		for _, x := range strings.Split(s, ",") {
			f.Type = append(f.Type, casemgmt.Type(strings.ToUpper(strings.TrimSpace(x))))
		}
	}
	if s := q.Get("queue"); s != "" {
		for _, x := range strings.Split(s, ",") {
			f.Queue = append(f.Queue, casemgmt.Queue(strings.ToUpper(strings.TrimSpace(x))))
		}
	}
	if q.Get("breached") == "1" {
		t := true
		f.Breached = &t
	}
	cases := d.cases.List(tenant, f)
	if cases == nil {
		cases = []casemgmt.Case{}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"cases": cases})
}

func (d *deps) casesOpen(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Type        string   `json:"type"`
		Subject     string   `json:"subject"`
		Description string   `json:"description"`
		MemberID    string   `json:"member_id"`
		EmployerID  string   `json:"employer_id"`
		LinkedKind  string   `json:"linked_kind"`
		LinkedID    string   `json:"linked_id"`
		Priority    string   `json:"priority"`
		Queue       string   `json:"queue"`
		Tags        []string `json:"tags"`
		NoDedup     bool     `json:"no_dedup"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	c, merged, err := d.cases.Open(casemgmt.OpenInput{
		TenantID: tenant, Type: casemgmt.Type(strings.ToUpper(in.Type)),
		Subject: in.Subject, Description: in.Description,
		MemberID: in.MemberID, EmployerID: in.EmployerID,
		LinkedKind: in.LinkedKind, LinkedID: in.LinkedID,
		Source: casemgmt.SourceManual, Priority: casemgmt.Priority(strings.ToUpper(in.Priority)),
		Queue: casemgmt.Queue(strings.ToUpper(in.Queue)), Tags: in.Tags,
		Actor: actorOf(r), NoDedup: in.NoDedup,
	})
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "CASE_OPEN_FAILED", err)
		return
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: actorOf(r), Action: "case.opened", EntityType: "case", EntityID: c.ID, After: map[string]any{"type": c.Type, "merged": merged}})
	httpsrv.WriteJSON(w, 201, map[string]any{"case": c, "merged": merged})
}

func (d *deps) caseGet(w http.ResponseWriter, r *http.Request) {
	c, err := d.cases.Get(httpsrv.TenantOf(r), r.PathValue("id"))
	if err != nil {
		httpsrv.WriteError(w, http.StatusNotFound, "CASE_NOT_FOUND", err)
		return
	}
	httpsrv.WriteJSON(w, 200, c)
}

func (d *deps) caseTransition(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	c, err := d.cases.Transition(httpsrv.TenantOf(r), r.PathValue("id"),
		casemgmt.Status(strings.ToUpper(in.Status)), actorOf(r), in.Note)
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "CASE_TRANSITION_FAILED", err)
		return
	}
	d.audit.Append(audit.Record{TenantID: httpsrv.TenantOf(r), Actor: actorOf(r), Action: "case.transition", EntityType: "case", EntityID: c.ID, After: map[string]any{"status": c.Status}})
	httpsrv.WriteJSON(w, 200, c)
}

func (d *deps) caseAssign(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Assignee string `json:"assignee"`
		Queue    string `json:"queue"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	c, err := d.cases.Assign(httpsrv.TenantOf(r), r.PathValue("id"), in.Assignee, casemgmt.Queue(strings.ToUpper(in.Queue)), actorOf(r))
	if err != nil {
		httpsrv.WriteError(w, http.StatusNotFound, "CASE_NOT_FOUND", err)
		return
	}
	httpsrv.WriteJSON(w, 200, c)
}

func (d *deps) caseComment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Body string `json:"body"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	c, err := d.cases.AddComment(httpsrv.TenantOf(r), r.PathValue("id"), actorOf(r), in.Body)
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "CASE_COMMENT_FAILED", err)
		return
	}
	httpsrv.WriteJSON(w, 200, c)
}

func (d *deps) caseAddTask(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title string `json:"title"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	c, err := d.cases.AddTask(httpsrv.TenantOf(r), r.PathValue("id"), in.Title, actorOf(r))
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "CASE_TASK_FAILED", err)
		return
	}
	httpsrv.WriteJSON(w, 200, c)
}

func (d *deps) caseCompleteTask(w http.ResponseWriter, r *http.Request) {
	c, err := d.cases.CompleteTask(httpsrv.TenantOf(r), r.PathValue("id"), r.PathValue("taskId"), actorOf(r))
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "CASE_TASK_FAILED", err)
		return
	}
	httpsrv.WriteJSON(w, 200, c)
}

func (d *deps) caseLink(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OtherID string `json:"other_id"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	c, err := d.cases.Link(httpsrv.TenantOf(r), r.PathValue("id"), in.OtherID, actorOf(r))
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "CASE_LINK_FAILED", err)
		return
	}
	httpsrv.WriteJSON(w, 200, c)
}

func (d *deps) caseResolve(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Resolution string `json:"resolution"`
		Code       string `json:"code"` // UPHELD|REJECTED|WITHDRAWN|CONFIRMED_FRAUD|CLEARED|FIXED|DUPLICATE
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	c, err := d.cases.Resolve(httpsrv.TenantOf(r), r.PathValue("id"), in.Resolution, strings.ToUpper(in.Code), actorOf(r))
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "CASE_RESOLVE_FAILED", err)
		return
	}
	d.audit.Append(audit.Record{TenantID: httpsrv.TenantOf(r), Actor: actorOf(r), Action: "case.resolved", EntityType: "case", EntityID: c.ID, After: map[string]any{"code": c.ResolutionCode}})
	httpsrv.WriteJSON(w, 200, c)
}

func (d *deps) caseEscalate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	c, err := d.cases.Escalate(httpsrv.TenantOf(r), r.PathValue("id"), actorOf(r), in.Reason)
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "CASE_ESCALATE_FAILED", err)
		return
	}
	httpsrv.WriteJSON(w, 200, c)
}

func (d *deps) caseSweep(w http.ResponseWriter, r *http.Request) {
	hits := d.cases.Sweep(httpsrv.TenantOf(r))
	if hits == nil {
		hits = []casemgmt.Case{}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"breached": hits, "count": len(hits)})
}

func (d *deps) caseStats(w http.ResponseWriter, r *http.Request) {
	httpsrv.WriteJSON(w, 200, d.cases.Stats(httpsrv.TenantOf(r)))
}

func (d *deps) caseRate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Score int `json:"score"` // 1..5
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	c, err := d.cases.Rate(httpsrv.TenantOf(r), r.PathValue("id"), in.Score, actorOf(r))
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "CASE_RATE_FAILED", err)
		return
	}
	httpsrv.WriteJSON(w, 200, c)
}

func (d *deps) casesRoster(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Queue string   `json:"queue"`
		Staff []string `json:"staff"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	d.cases.SetRoster(casemgmt.Queue(strings.ToUpper(in.Queue)), in.Staff)
	httpsrv.WriteJSON(w, 200, map[string]any{"ok": true})
}

// ---- member: self-service ---------------------------------------------------

func (d *deps) memberCases(w http.ResponseWriter, r *http.Request) {
	memberID := r.PathValue("id")
	cases := d.cases.List(httpsrv.TenantOf(r), casemgmt.Filter{MemberID: memberID})
	if cases == nil {
		cases = []casemgmt.Case{}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"cases": cases})
}

// memberRaiseCase lets a member raise a complaint or service request from
// any surface (PWA, Flutter, WhatsApp via the chatbot relay).
func (d *deps) memberRaiseCase(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Type        string   `json:"type"` // COMPLAINT | SERVICE_REQUEST | GENERAL
		Subject     string   `json:"subject"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	t := casemgmt.Type(strings.ToUpper(in.Type))
	switch t {
	case casemgmt.TypeComplaint, casemgmt.TypeServiceRequest, casemgmt.TypeGeneral:
	default:
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", errStr("member cases must be COMPLAINT, SERVICE_REQUEST or GENERAL"))
		return
	}
	tenant := httpsrv.TenantOf(r)
	memberID := r.PathValue("id")
	if _, ok := d.members.Person(memberID); !ok {
		httpsrv.WriteError(w, http.StatusNotFound, "NOT_FOUND", errStr("member not found"))
		return
	}
	c, merged, err := d.cases.Open(casemgmt.OpenInput{
		TenantID: tenant, Type: t, MemberID: memberID,
		Subject: in.Subject, Description: in.Description,
		Source: casemgmt.SourceMember, Tags: in.Tags, Actor: memberID,
	})
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "CASE_OPEN_FAILED", err)
		return
	}
	// log the touchpoint on the CRM timeline
	ch := crm.Channel(strings.ToUpper(r.URL.Query().Get("channel")))
	if ch == "" {
		ch = crm.ChWeb
	}
	_, _ = d.crm.Record(crm.Interaction{
		TenantID: tenant, MemberID: memberID, Channel: ch,
		Kind: string(t), Summary: in.Subject, CaseID: c.ID,
	})
	httpsrv.WriteJSON(w, 201, map[string]any{"case": c, "merged": merged})
}

// ---- CRM --------------------------------------------------------------------

func (d *deps) crmMember360(w http.ResponseWriter, r *http.Request) {
	v, err := d.crm.Member360(httpsrv.TenantOf(r), r.PathValue("id"))
	if err != nil {
		httpsrv.WriteError(w, http.StatusNotFound, "MEMBER_NOT_FOUND", err)
		return
	}
	httpsrv.WriteJSON(w, 200, v)
}

func (d *deps) crmRecordInteraction(w http.ResponseWriter, r *http.Request) {
	var in crm.Interaction
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	in.TenantID = httpsrv.TenantOf(r)
	in.Channel = crm.Channel(strings.ToUpper(string(in.Channel)))
	ix, err := d.crm.Record(in)
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "CRM_RECORD_FAILED", err)
		return
	}
	if d.caseRepo != nil {
		_ = d.caseRepo.SaveInteraction(r.Context(), ix)
	}
	httpsrv.WriteJSON(w, 201, ix)
}

func (d *deps) crmTimeline(w http.ResponseWriter, r *http.Request) {
	tl := d.crm.Timeline(httpsrv.TenantOf(r), r.PathValue("id"), 100)
	if tl == nil {
		tl = []crm.Interaction{}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"timeline": tl})
}

func (d *deps) crmSegmentMembers(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	tag := crm.SegmentTag(strings.ToUpper(r.PathValue("tag")))
	type row struct {
		MemberID string           `json:"member_id"`
		Name     string           `json:"name"`
		Segments []crm.SegmentTag `json:"segments"`
	}
	var out []row
	for _, rsa := range d.members.List(tenant) {
		f, err := d.memberFacts(tenant, rsa.PersonID)
		if err != nil {
			continue
		}
		tags := crm.Segment(f, time.Now())
		if crm.Has(tags, tag) {
			name := ""
			if p, ok := d.members.Person(rsa.PersonID); ok {
				name = p.FullName
			}
			out = append(out, row{MemberID: rsa.PersonID, Name: name, Segments: tags})
		}
	}
	if out == nil {
		out = []row{}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"segment": tag, "members": out})
}

func (d *deps) crmCampaigns(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cs := d.crm.Campaigns(httpsrv.TenantOf(r))
		if cs == nil {
			cs = []crm.Campaign{}
		}
		httpsrv.WriteJSON(w, 200, map[string]any{"campaigns": cs})
		return
	}
	var in struct {
		Name    string `json:"name"`
		Segment string `json:"segment"`
		Channel string `json:"channel"`
		Message string `json:"message"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	c, err := d.crm.CreateCampaign(httpsrv.TenantOf(r), in.Name,
		crm.SegmentTag(strings.ToUpper(in.Segment)), crm.Channel(strings.ToUpper(in.Channel)), in.Message)
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "CAMPAIGN_FAILED", err)
		return
	}
	if d.caseRepo != nil {
		_ = d.caseRepo.SaveCampaign(r.Context(), c)
	}
	httpsrv.WriteJSON(w, 201, c)
}

func (d *deps) crmLaunchCampaign(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	// resolve segment membership from live member facts
	var memberIDs []string
	for _, cmp := range d.crm.Campaigns(tenant) {
		if cmp.ID == r.PathValue("id") {
			for _, rsa := range d.members.List(tenant) {
				f, err := d.memberFacts(tenant, rsa.PersonID)
				if err != nil {
					continue
				}
				if crm.Has(crm.Segment(f, time.Now()), cmp.Segment) {
					memberIDs = append(memberIDs, rsa.PersonID)
				}
			}
			break
		}
	}
	c, err := d.crm.LaunchCampaign(tenant, r.PathValue("id"), memberIDs)
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "CAMPAIGN_LAUNCH_FAILED", err)
		return
	}
	if d.caseRepo != nil {
		_ = d.caseRepo.SaveCampaign(r.Context(), c)
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: actorOf(r), Action: "crm.campaign.launched", EntityType: "campaign", EntityID: c.ID, After: map[string]any{"recipients": c.Recipients}})
	httpsrv.WriteJSON(w, 200, c)
}

func (d *deps) crmComplaintsRegister(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from := time.Now().AddDate(0, -3, 0) // default: last quarter
	to := time.Now()
	if s := q.Get("from"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			from = t
		}
	}
	if s := q.Get("to"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			to = t.Add(24*time.Hour - time.Second)
		}
	}
	rows := d.crm.ComplaintsRegister(httpsrv.TenantOf(r), from, to)
	if rows == nil {
		rows = []crm.ComplaintRow{}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"register": rows, "from": from, "to": to})
}

// ---- integration helpers -----------------------------------------------------

// memberFacts is the crm.FactsProvider adapter over the domain engines.
func (d *deps) memberFacts(tenantID, memberID string) (crm.MemberFacts, error) {
	p, ok := d.members.Person(memberID)
	if !ok {
		return crm.MemberFacts{}, errStr("member not found")
	}
	f := crm.MemberFacts{MemberID: memberID, JoinedAt: p.CreatedAt}
	if dob, err := time.Parse("2006-01-02", p.DOB); err == nil {
		f.AgeYears = int(time.Since(dob).Hours() / (24 * 365.25))
	}
	f.BalanceKobo = int64(d.ledger.Balance(rsaAccount(memberID)))
	d.microMu.Lock()
	_, f.MicroPension = d.micro[memberID]
	d.microMu.Unlock()
	if d.diaspora != nil {
		if cs, _ := d.diaspora.Contributions(tenantID, memberID); len(cs) > 0 {
			f.Diaspora = true
		}
	}
	// last contribution: newest ledger credit to the member RSA account
	// (best-effort from the in-memory ledger; zero when never contributed)
	return f, nil
}

func actorOf(r *http.Request) string {
	if a := r.Header.Get("X-Actor"); a != "" {
		return a
	}
	return "staff"
}
