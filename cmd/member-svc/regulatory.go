package main

// Tranche 21: PenCom-parity regulatory surface handlers — forms library,
// KYC document uploads, branch appointments, fund-performance publication,
// statutory disclosures, news CMS and the tenant licence profile (PRA s.60).

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/branch"
	"github.com/munisp/pfaengine/internal/disclosure"
	"github.com/munisp/pfaengine/internal/docslib"
	"github.com/munisp/pfaengine/internal/docupload"
	"github.com/munisp/pfaengine/internal/funds"
	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/news"
	"github.com/munisp/pfaengine/internal/tenancy"
)

func (d *deps) auditReg(r *http.Request, action, entityType, entityID, detail string) {
	d.audit.Append(audit.Record{
		TenantID: httpsrv.TenantOf(r), Actor: actorOf(r), Action: action,
		EntityType: entityType, EntityID: entityID,
		After: map[string]any{"detail": detail},
	})
}

// ---- forms library (PenCom downloads parity) --------------------------------

func (d *deps) formsList(w http.ResponseWriter, r *http.Request) {
	forms := d.docslib.List(httpsrv.TenantOf(r), docslib.Category(r.URL.Query().Get("category")))
	if forms == nil {
		forms = []docslib.Form{}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"forms": forms})
}

func (d *deps) formGet(w http.ResponseWriter, r *http.Request) {
	f, err := d.docslib.Get(httpsrv.TenantOf(r), r.PathValue("id"))
	if err != nil {
		httpsrv.WriteError(w, 404, "NOT_FOUND", err)
		return
	}
	httpsrv.WriteJSON(w, 200, f)
}

func (d *deps) formVersions(w http.ResponseWriter, r *http.Request) {
	vs := d.docslib.Versions(httpsrv.TenantOf(r), r.URL.Query().Get("code"))
	if vs == nil {
		vs = []docslib.Form{}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"versions": vs})
}

func (d *deps) formPublish(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code        string `json:"code"`
		Title       string `json:"title"`
		Category    string `json:"category"`
		Description string `json:"description"`
		Ref         string `json:"ref"`
		SHA         string `json:"sha256"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	f, err := d.docslib.Publish(httpsrv.TenantOf(r), in.Code, in.Title,
		docslib.Category(in.Category), in.Description, in.Ref, in.SHA)
	if err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	d.auditReg(r, "form.published", "form", f.ID, fmt.Sprintf("%s v%d", f.Code, f.Version))
	httpsrv.WriteJSON(w, 201, f)
}

// ---- KYC document uploads ---------------------------------------------------

func (d *deps) docSubmit(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MemberID string `json:"member_id"`
		Kind     string `json:"kind"`
		Filename string `json:"filename"`
		Mime     string `json:"mime"`
		Size     int64  `json:"size"`
		Ref      string `json:"ref"`
		SHA256   string `json:"sha256"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	doc, err := d.docupload.Submit(httpsrv.TenantOf(r), in.MemberID,
		docupload.Kind(in.Kind), in.Filename, in.Mime, in.Size, in.Ref, in.SHA256)
	if err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	d.auditReg(r, "document.submitted", "document", doc.ID, string(doc.Kind))
	httpsrv.WriteJSON(w, 201, doc)
}

func (d *deps) docMine(w http.ResponseWriter, r *http.Request) {
	docs := d.docupload.ByMember(httpsrv.TenantOf(r), r.URL.Query().Get("member_id"))
	if docs == nil {
		docs = []docupload.Document{}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"documents": docs})
}

func (d *deps) docPending(w http.ResponseWriter, r *http.Request) {
	docs := d.docupload.Pending(httpsrv.TenantOf(r))
	if docs == nil {
		docs = []docupload.Document{}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"documents": docs})
}

func (d *deps) docReview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Approve bool   `json:"approve"`
		Note    string `json:"note"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	doc, err := d.docupload.Review(httpsrv.TenantOf(r), r.PathValue("id"), actorOf(r), in.Approve, in.Note)
	if err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	d.auditReg(r, "document.reviewed", "document", doc.ID, in.Note)
	httpsrv.WriteJSON(w, 200, doc)
}

// ---- branch appointments ------------------------------------------------------

func (d *deps) branchList(w http.ResponseWriter, r *http.Request) {
	bs := d.branch.Branches(httpsrv.TenantOf(r), r.URL.Query().Get("state"))
	if bs == nil {
		bs = []branch.Branch{}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"branches": bs})
}

func (d *deps) branchAdd(w http.ResponseWriter, r *http.Request) {
	var in branch.Branch
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	in.TenantID = httpsrv.TenantOf(r)
	b, err := d.branch.AddBranch(in)
	if err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	d.geoUpsertBranch(in.TenantID, b) // PostGIS write-through (nil-safe)
	d.auditReg(r, "branch.added", "branch", b.ID, b.Name)
	httpsrv.WriteJSON(w, 201, b)
}

func (d *deps) branchAvailability(w http.ResponseWriter, r *http.Request) {
	slots, err := d.branch.Availability(httpsrv.TenantOf(r), r.PathValue("id"), r.URL.Query().Get("date"))
	if err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	if slots == nil {
		slots = []int{}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"slots": slots})
}

func (d *deps) apptBook(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BranchID string `json:"branch_id"`
		MemberID string `json:"member_id"`
		Date     string `json:"date"`
		Slot     int    `json:"slot"`
		Purpose  string `json:"purpose"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	a, err := d.branch.Book(httpsrv.TenantOf(r), in.BranchID, in.MemberID, in.Date, in.Slot, in.Purpose)
	if err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	d.auditReg(r, "appointment.booked", "appointment", a.ID, in.Date)
	httpsrv.WriteJSON(w, 201, a)
}

func (d *deps) apptList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	as := d.branch.Appointments(httpsrv.TenantOf(r), q.Get("member_id"), q.Get("branch_id"), q.Get("date"))
	if as == nil {
		as = []branch.Appointment{}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"appointments": as})
}

func (d *deps) apptTransition(w http.ResponseWriter, r *http.Request) {
	var in struct {
		To string `json:"to"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	a, err := d.branch.Transition(httpsrv.TenantOf(r), r.PathValue("id"), branch.ApptStatus(in.To))
	if err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	httpsrv.WriteJSON(w, 200, a)
}

// ---- statutory disclosures (WORM) ---------------------------------------------

func (d *deps) disclosureList(w http.ResponseWriter, r *http.Request) {
	pubs := d.disclosure.List(httpsrv.TenantOf(r), disclosure.Kind(r.URL.Query().Get("kind")))
	if pubs == nil {
		pubs = []disclosure.Publication{}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"disclosures": pubs})
}

func (d *deps) disclosurePublish(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind   string `json:"kind"`
		Title  string `json:"title"`
		Period string `json:"period"`
		Ref    string `json:"ref"`
		SHA    string `json:"sha256"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	p, err := d.disclosure.Publish(httpsrv.TenantOf(r), disclosure.Kind(in.Kind),
		in.Title, in.Period, in.Ref, in.SHA, actorOf(r))
	if err != nil {
		httpsrv.WriteError(w, 409, "CONFLICT", err)
		return
	}
	d.auditReg(r, "disclosure.published", "disclosure", p.ID, in.Period)
	httpsrv.WriteJSON(w, 201, p)
}

// ---- news / education CMS -----------------------------------------------------

func (d *deps) newsFeed(w http.ResponseWriter, r *http.Request) {
	arts := d.news.Feed(httpsrv.TenantOf(r), news.Category(r.URL.Query().Get("category")))
	if arts == nil {
		arts = []news.Article{}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"articles": arts})
}

func (d *deps) newsDrafts(w http.ResponseWriter, r *http.Request) {
	arts := d.news.Drafts(httpsrv.TenantOf(r))
	if arts == nil {
		arts = []news.Article{}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"articles": arts})
}

func (d *deps) newsCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title    string `json:"title"`
		Body     string `json:"body"`
		Category string `json:"category"`
		Pinned   bool   `json:"pinned"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	a, err := d.news.Create(httpsrv.TenantOf(r), in.Title, in.Body,
		news.Category(in.Category), actorOf(r), in.Pinned)
	if err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	d.auditReg(r, "news.created", "news", a.ID, in.Title)
	httpsrv.WriteJSON(w, 201, a)
}

func (d *deps) newsPublish(w http.ResponseWriter, r *http.Request) {
	a, err := d.news.Publish(httpsrv.TenantOf(r), r.PathValue("id"))
	if err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	d.auditReg(r, "news.published", "news", a.ID, "")
	httpsrv.WriteJSON(w, 200, a)
}

func (d *deps) newsArchive(w http.ResponseWriter, r *http.Request) {
	a, err := d.news.Archive(httpsrv.TenantOf(r), r.PathValue("id"))
	if err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	httpsrv.WriteJSON(w, 200, a)
}

// ---- fund performance publication ---------------------------------------------

func (d *deps) fundPublishNAV(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Date  string `json:"date"`
		Price int64  `json:"price"` // kobo
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	if err := d.funds.PublishNAV(r.PathValue("id"), in.Date, money.Kobo(in.Price)); err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	d.auditReg(r, "nav.published", "fund", r.PathValue("id"), in.Date)
	httpsrv.WriteJSON(w, 201, map[string]any{"ok": true})
}

func (d *deps) fundSeries(w http.ResponseWriter, r *http.Request) {
	series, err := d.funds.Series(r.PathValue("id"))
	if err != nil {
		httpsrv.WriteError(w, 404, "NOT_FOUND", err)
		return
	}
	if series == nil {
		series = []funds.PricePoint{}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"series": series})
}

func (d *deps) fundPerformance(w http.ResponseWriter, r *http.Request) {
	asOf := r.URL.Query().Get("as_of")
	if fundID := r.URL.Query().Get("fund_id"); fundID != "" {
		p, err := d.funds.Performance(fundID, asOf)
		if err != nil {
			httpsrv.WriteError(w, 404, "NOT_FOUND", err)
			return
		}
		httpsrv.WriteJSON(w, 200, p)
		return
	}
	out := map[string]any{}
	for _, id := range d.funds.FundIDs() {
		if p, err := d.funds.Performance(id, asOf); err == nil {
			out[id] = p
		}
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"performance": out})
}

// ---- tenancy licence profile (PRA s.60) ----------------------------------------

func (d *deps) licenseGet(w http.ResponseWriter, r *http.Request) {
	lp, ok := d.tenants.LicenseProfileOf(httpsrv.TenantOf(r))
	if !ok {
		httpsrv.WriteError(w, 404, "NOT_FOUND", errors.New("tenant not provisioned"))
		return
	}
	httpsrv.WriteJSON(w, 200, lp)
}

func (d *deps) licenseSet(w http.ResponseWriter, r *http.Request) {
	var in tenancy.LicenseProfile
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	if err := d.tenants.SetLicenseProfile(httpsrv.TenantOf(r), in); err != nil {
		httpsrv.WriteError(w, 404, "NOT_FOUND", err)
		return
	}
	d.auditReg(r, "license.updated", "tenant", httpsrv.TenantOf(r), in.LicenseNo)
	httpsrv.WriteJSON(w, 200, in)
}
