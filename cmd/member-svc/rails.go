package main

// Gap-3/Gap-4 handlers: payroll-provider integration (ERPNext/Frappe HR —
// open-source payroll) with PSSP schedule file import/export, and the PenCom
// rails (PENCAP data recapture, RTS RSA-transfer hub, PSSP schedule
// submission). All rails are env-configured and fail closed: unconfigured
// clients return 503 RAIL_UNCONFIGURED, never a mock success.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/payroll"
	"github.com/munisp/pfaengine/internal/pencomrails"
	"github.com/munisp/pfaengine/internal/persist"
)

func errRail(name string) error { return errors.New("rail not configured: " + name) }

// storeSchedule remembers a schedule (snapshot write-through when Postgres
// persistence is active) so push/export/status flows can reference it.
func (d *deps) storeSchedule(s payroll.Schedule) {
	d.payMu.Lock()
	d.paySchedules[s.ID] = s
	d.payMu.Unlock()
	persist.Save(d.snapRepo, "payroll.schedule", s.TenantID, s.ID, s)
}

func (d *deps) scheduleOf(tenantID, id string) (payroll.Schedule, bool) {
	d.payMu.Lock()
	defer d.payMu.Unlock()
	s, ok := d.paySchedules[id]
	if !ok || s.TenantID != tenantID {
		return payroll.Schedule{}, false
	}
	return s, true
}

func (d *deps) auditRails(r *http.Request, action, entityType, entityID string, after map[string]any) {
	d.audit.Append(audit.Record{TenantID: httpsrv.TenantOf(r), Actor: actorOf(r),
		Action: action, EntityType: entityType, EntityID: entityID, After: after})
}

// ---- Payroll: ERPNext/Frappe HR (open-source payroll) ----

// POST /api/v1/payroll/erpnext/sync — pull a Payroll Entry from ERPNext and
// register it as a contribution schedule.
func (d *deps) payrollERPNextSync(w http.ResponseWriter, r *http.Request) {
	if d.erpnext == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "RAIL_UNCONFIGURED", errRail("erpnext (set ERPNEXT_BASE_URL/API_KEY/API_SECRET)"))
		return
	}
	var in struct {
		PayrollEntryID string `json:"payroll_entry_id"`
		EmployerID     string `json:"employer_id"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_JSON", err)
		return
	}
	if in.PayrollEntryID == "" {
		httpsrv.WriteError(w, http.StatusBadRequest, "VALIDATION", errors.New("payroll_entry_id required"))
		return
	}
	s, err := d.erpnext.FetchPayrollEntry(r.Context(), in.PayrollEntryID)
	if err != nil {
		httpsrv.WriteError(w, http.StatusBadGateway, "ERPNEXT_ERROR", err)
		return
	}
	s.TenantID = httpsrv.TenantOf(r)
	if in.EmployerID != "" {
		s.EmployerID = in.EmployerID
	}
	if err := s.Validate(); err != nil {
		httpsrv.WriteError(w, http.StatusBadGateway, "ERPNEXT_INVALID_SCHEDULE", err)
		return
	}
	d.storeSchedule(s)
	d.auditRails(r, "PAYROLL_ERPNEXT_SYNC", "payroll_schedule", s.ID,
		map[string]any{"lines": len(s.Lines), "total_kobo": s.Total(), "period": s.Period})
	httpsrv.WriteJSON(w, 200, s)
}

// POST /api/v1/payroll/schedules/{id}/push — push a stored schedule back to
// ERPNext as a Contribution Schedule document.
func (d *deps) payrollPush(w http.ResponseWriter, r *http.Request) {
	if d.erpnext == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "RAIL_UNCONFIGURED", errRail("erpnext"))
		return
	}
	s, ok := d.scheduleOf(httpsrv.TenantOf(r), r.PathValue("id"))
	if !ok {
		httpsrv.WriteError(w, http.StatusNotFound, "NOT_FOUND", errors.New("schedule not found"))
		return
	}
	name, err := d.erpnext.PushContributionSchedule(r.Context(), s)
	if err != nil {
		httpsrv.WriteError(w, http.StatusBadGateway, "ERPNEXT_ERROR", err)
		return
	}
	d.auditRails(r, "PAYROLL_PUSH", "payroll_schedule", s.ID, map[string]any{"erpnext_doc": name})
	httpsrv.WriteJSON(w, 200, map[string]any{"schedule_id": s.ID, "erpnext_doc": name})
}

// POST /api/v1/payroll/pssp/import?format=pensphere|pension-central — import
// a PSSP remittance schedule file (CSV body).
func (d *deps) psspImport(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	body, err := io.ReadAll(io.LimitReader(r.Body, 20<<20))
	if err != nil || len(body) == 0 {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_BODY", errors.New("CSV body required"))
		return
	}
	var s payroll.Schedule
	switch format {
	case "pensphere":
		s, err = payroll.ParsePensphere(strings.NewReader(string(body)))
	case "pension-central":
		s, err = payroll.ParsePensionCentral(strings.NewReader(string(body)))
	default:
		httpsrv.WriteError(w, http.StatusBadRequest, "VALIDATION", errors.New("format must be pensphere or pension-central"))
		return
	}
	if err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "PSSP_PARSE", err)
		return
	}
	if err := s.Validate(); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "PSSP_INVALID", err)
		return
	}
	s.TenantID = httpsrv.TenantOf(r)
	if s.ID == "" {
		s.ID = "import-" + time.Now().UTC().Format("20060102150405")
	}
	d.storeSchedule(s)
	d.auditRails(r, "PSSP_IMPORT", "payroll_schedule", s.ID,
		map[string]any{"format": format, "lines": len(s.Lines), "total_kobo": s.Total()})
	httpsrv.WriteJSON(w, 200, s)
}

// GET /api/v1/payroll/pssp/export?format=pensphere|pension-central&id=...
func (d *deps) psspExport(w http.ResponseWriter, r *http.Request) {
	s, ok := d.scheduleOf(httpsrv.TenantOf(r), r.URL.Query().Get("id"))
	if !ok {
		httpsrv.WriteError(w, http.StatusNotFound, "NOT_FOUND", errors.New("schedule not found"))
		return
	}
	var sb strings.Builder
	switch r.URL.Query().Get("format") {
	case "pensphere":
		if err := payroll.FormatPensphere(&sb, s); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "PSSP_FORMAT", err)
			return
		}
	case "pension-central":
		if err := payroll.FormatPensionCentral(&sb, s); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "PSSP_FORMAT", err)
			return
		}
	default:
		httpsrv.WriteError(w, http.StatusBadRequest, "VALIDATION", errors.New("format must be pensphere or pension-central"))
		return
	}
	d.auditRails(r, "PSSP_EXPORT", "payroll_schedule", s.ID, nil)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write([]byte(sb.String()))
}

// ---- PenCom rails ----

// POST /api/v1/pencom/recapture — submit a PENCAP data-recapture batch.
func (d *deps) pencomRecaptureSubmit(w http.ResponseWriter, r *http.Request) {
	if d.pencap == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "RAIL_UNCONFIGURED", errRail("pencap (set PENCAP_BASE_URL/PFA_CODE/API_KEY)"))
		return
	}
	var in struct {
		Records []pencomrails.RecaptureRecord `json:"records"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_JSON", err)
		return
	}
	if len(in.Records) == 0 {
		httpsrv.WriteError(w, http.StatusBadRequest, "VALIDATION", errors.New("records required"))
		return
	}
	for i, rec := range in.Records {
		if err := rec.Validate(); err != nil {
			httpsrv.WriteError(w, http.StatusBadRequest, "VALIDATION", errors.New("record "+itoa(i)+": "+err.Error()))
			return
		}
	}
	batchID, err := d.pencap.SubmitRecapture(r.Context(), in.Records)
	if err != nil {
		httpsrv.WriteError(w, http.StatusBadGateway, "PENCAP_ERROR", err)
		return
	}
	d.auditRails(r, "PENCAP_RECAPTURE_SUBMIT", "recapture_batch", batchID, map[string]any{"records": len(in.Records)})
	httpsrv.WriteJSON(w, 200, map[string]any{"batch_id": batchID, "records": len(in.Records)})
}

// GET /api/v1/pencom/recapture/{batchId} — fetch validation results.
func (d *deps) pencomRecaptureResults(w http.ResponseWriter, r *http.Request) {
	if d.pencap == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "RAIL_UNCONFIGURED", errRail("pencap"))
		return
	}
	res, err := d.pencap.FetchResults(r.Context(), r.PathValue("batchId"))
	if err != nil {
		httpsrv.WriteError(w, http.StatusBadGateway, "PENCAP_ERROR", err)
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"results": res})
}

// POST /api/v1/pencom/transfers — initiate an RSA transfer via the RTS hub.
func (d *deps) pencomTransferInitiate(w http.ResponseWriter, r *http.Request) {
	if d.rts == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "RAIL_UNCONFIGURED", errRail("rts (set RTS_BASE_URL/PFA_CODE/API_KEY)"))
		return
	}
	var in struct {
		PIN   string `json:"pin"`
		ToPFA string `json:"to_pfa"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_JSON", err)
		return
	}
	if in.PIN == "" || in.ToPFA == "" {
		httpsrv.WriteError(w, http.StatusBadRequest, "VALIDATION", errors.New("pin and to_pfa required"))
		return
	}
	ti, err := d.rts.InitiateTransfer(r.Context(), in.PIN, in.ToPFA)
	if err != nil {
		httpsrv.WriteError(w, http.StatusBadGateway, "RTS_ERROR", err)
		return
	}
	d.auditRails(r, "RTS_TRANSFER_INITIATE", "rsa_transfer", ti.ID, map[string]any{"pin": in.PIN, "to_pfa": in.ToPFA})
	httpsrv.WriteJSON(w, 200, ti)
}

// GET /api/v1/pencom/transfers?since=RFC3339 — poll inbound transfer
// instructions from the RTS hub.
func (d *deps) pencomTransfersInbound(w http.ResponseWriter, r *http.Request) {
	if d.rts == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "RAIL_UNCONFIGURED", errRail("rts"))
		return
	}
	since := time.Time{}
	if s := r.URL.Query().Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			httpsrv.WriteError(w, http.StatusBadRequest, "VALIDATION", errors.New("since must be RFC3339"))
			return
		}
		since = t
	}
	out, err := d.rts.PollInbound(r.Context(), since)
	if err != nil {
		httpsrv.WriteError(w, http.StatusBadGateway, "RTS_ERROR", err)
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"transfers": out})
}

// POST /api/v1/pencom/transfers/{id}/confirm — confirm an inbound transfer.
func (d *deps) pencomTransferConfirm(w http.ResponseWriter, r *http.Request) {
	if d.rts == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "RAIL_UNCONFIGURED", errRail("rts"))
		return
	}
	id := r.PathValue("id")
	if err := d.rts.Confirm(r.Context(), id); err != nil {
		httpsrv.WriteError(w, http.StatusBadGateway, "RTS_ERROR", err)
		return
	}
	d.auditRails(r, "RTS_TRANSFER_CONFIRM", "rsa_transfer", id, nil)
	httpsrv.WriteJSON(w, 200, map[string]any{"id": id, "status": "CONFIRMED"})
}

// POST /api/v1/pencom/pssp/schedules — submit a stored schedule to the PSSP.
func (d *deps) psspSubmit(w http.ResponseWriter, r *http.Request) {
	if d.pssp == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "RAIL_UNCONFIGURED", errRail("pssp (set PSSP_BASE_URL/PFA_CODE/API_KEY)"))
		return
	}
	var in struct {
		ScheduleID string `json:"schedule_id"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_JSON", err)
		return
	}
	s, ok := d.scheduleOf(httpsrv.TenantOf(r), in.ScheduleID)
	if !ok {
		httpsrv.WriteError(w, http.StatusNotFound, "NOT_FOUND", errors.New("schedule not found"))
		return
	}
	payload, err := json.Marshal(s)
	if err != nil {
		httpsrv.WriteError(w, http.StatusInternalServerError, "ENCODE", err)
		return
	}
	ref, err := d.pssp.SubmitSchedule(r.Context(), payload)
	if err != nil {
		httpsrv.WriteError(w, http.StatusBadGateway, "PSSP_ERROR", err)
		return
	}
	d.auditRails(r, "PSSP_SCHEDULE_SUBMIT", "payroll_schedule", s.ID, map[string]any{"pssp_ref": ref})
	httpsrv.WriteJSON(w, 200, map[string]any{"schedule_id": s.ID, "pssp_ref": ref})
}

// GET /api/v1/pencom/pssp/schedules/{ref} — poll PSSP processing status.
func (d *deps) psspStatus(w http.ResponseWriter, r *http.Request) {
	if d.pssp == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "RAIL_UNCONFIGURED", errRail("pssp"))
		return
	}
	st, err := d.pssp.ScheduleStatus(r.Context(), r.PathValue("ref"))
	if err != nil {
		httpsrv.WriteError(w, http.StatusBadGateway, "PSSP_ERROR", err)
		return
	}
	httpsrv.WriteJSON(w, 200, st)
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
