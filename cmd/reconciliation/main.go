// reconciliation-service: auto-matching, exception queues, suspense,
// employer-default detection (§4, FR-303, FR-305).
package main

import (
	"net/http"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/appx"
	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/contribution"
	"github.com/munisp/pfaengine/internal/recon"
)

type state struct {
	mu       sync.Mutex
	engine   *recon.Engine
	scheds   map[string]*contribution.Schedule
	receipts map[string][]recon.Receipt
}

func main() {
	st := &state{engine: recon.NewEngine(), scheds: map[string]*contribution.Schedule{}, receipts: map[string][]recon.Receipt{}}
	aud := audit.NewStore()

	mux := http.NewServeMux()
	appx.Health(mux, "reconciliation")

	mux.HandleFunc("POST /v1/reconciliation/run", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Schedule contribution.Schedule `json:"schedule"`
			Receipts []recon.Receipt       `json:"receipts"`
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		in.Schedule.TenantID = appx.TenantOf(r)
		res := st.engine.Reconcile(&in.Schedule, in.Receipts)
		aud.Append(audit.Record{TenantID: appx.TenantOf(r), Actor: "recon-service", Action: "recon.run", EntityType: "schedule", EntityID: in.Schedule.ID, After: res})
		appx.JSON(w, 200, map[string]any{"result": res, "auto_match_rate": res.AutoMatchRate(), "meets_target_95": res.AutoMatchRate() >= 0.95})
	})

	mux.HandleFunc("GET /v1/reconciliation/exceptions", func(w http.ResponseWriter, r *http.Request) {
		excs := st.engine.Exceptions(appx.TenantOf(r))
		appx.JSON(w, 200, map[string]any{"exceptions": excs, "aged_buckets": st.engine.AgedBuckets(appx.TenantOf(r), time.Now())})
	})

	mux.HandleFunc("POST /v1/reconciliation/{id}/resolve", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Resolution string `json:"resolution"` // AUTO_MATCH | RECLASSIFY | REFUND
			Actor      string `json:"actor"`
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		if err := st.engine.Resolve(appx.Path(r, "id"), in.Resolution, in.Actor); err != nil {
			appx.Err(w, 422, "RESOLVE_FAILED", err)
			return
		}
		aud.Append(audit.Record{TenantID: appx.TenantOf(r), Actor: in.Actor, Action: "recon.resolved", EntityType: "exception", EntityID: appx.Path(r, "id")})
		appx.JSON(w, 200, map[string]string{"status": "resolved"})
	})

	mux.HandleFunc("GET /v1/reconciliation/suspense/{employer}", func(w http.ResponseWriter, r *http.Request) {
		appx.JSON(w, 200, map[string]any{"employer_id": appx.Path(r, "employer"), "suspense_kobo": st.engine.Suspense(appx.Path(r, "employer"))})
	})

	mux.HandleFunc("POST /v1/reconciliation/defaults", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Schedules    []*contribution.Schedule `json:"schedules"`
			Receipts     []recon.Receipt          `json:"receipts"`
			ThresholdPct int                      `json:"threshold_pct"`
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		if in.ThresholdPct == 0 {
			in.ThresholdPct = 10
		}
		alerts := recon.DetectDefaults(in.Schedules, in.Receipts, in.ThresholdPct)
		for _, a := range alerts {
			aud.Append(audit.Record{TenantID: appx.TenantOf(r), Actor: "recon-service", Action: "employer.default.detected", EntityType: "employer", EntityID: a.EmployerID, After: a})
		}
		appx.JSON(w, 200, map[string]any{"alerts": alerts})
	})

	appx.Serve("reconciliation", appx.Env("PORT", "8085"), appx.TenantMiddleware(mux))
}

type errS string

func (e errS) Error() string { return string(e) }
