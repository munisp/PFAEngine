// transfer-service: RTS workflow with 365-day rule and quarterly cycles
// (§4 Phase 2, FR-402, INT-01).
package main

import (
	"net/http"
	"time"

	"github.com/munisp/pfaengine/internal/appx"
	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/transfer"
)

func main() {
	svc := transfer.NewService()
	aud := audit.NewStore()

	mux := http.NewServeMux()
	appx.Health(mux, "transfer")

	mux.HandleFunc("POST /v1/transfers", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RSAID, RSAPIN, Direction, CounterpartyPFA string
			ValueKobo                                 int64
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		tr, err := svc.Initiate(appx.TenantOf(r), in.RSAID, in.RSAPIN, transfer.Direction(in.Direction), in.CounterpartyPFA, money.Kobo(in.ValueKobo), time.Now().UTC())
		if err != nil {
			appx.Err(w, 422, "TRANSFER_REJECTED", err)
			return
		}
		aud.Append(audit.Record{TenantID: appx.TenantOf(r), Actor: "transfer-service", Action: "transfer.state.changed", EntityType: "transfer", EntityID: tr.ID, After: tr})
		appx.JSON(w, 201, tr)
	})

	mux.HandleFunc("POST /v1/transfers/{id}/submit-cycle", func(w http.ResponseWriter, r *http.Request) {
		svc.Validate(appx.Path(r, "id"))
		q, err := svc.SubmitToCycle(appx.Path(r, "id"), time.Now().UTC())
		if err != nil {
			appx.Err(w, 422, "SUBMIT_FAILED", err)
			return
		}
		appx.JSON(w, 200, map[string]string{"rts_quarter": q})
	})

	mux.HandleFunc("POST /v1/transfers/{id}/settle", func(w http.ResponseWriter, r *http.Request) {
		if err := svc.Settle(appx.Path(r, "id"), time.Now().UTC()); err != nil {
			appx.Err(w, 422, "SETTLE_FAILED", err)
			return
		}
		svc.Notify(appx.Path(r, "id"))
		appx.JSON(w, 200, map[string]string{"status": "NOTIFIED"})
	})

	mux.HandleFunc("GET /v1/transfers/{id}", func(w http.ResponseWriter, r *http.Request) {
		tr, ok := svc.Get(appx.Path(r, "id"))
		if !ok {
			appx.Err(w, 404, "NOT_FOUND", errS("transfer not found"))
			return
		}
		appx.JSON(w, 200, tr)
	})

	appx.Serve("transfer", appx.Env("PORT", "8093"), appx.TenantMiddleware(mux))
}

type errS string

func (e errS) Error() string { return string(e) }
