// audit-service: WORM hash-chained store, nightly verification,
// dual-control PenCom export (§4, SC-02, NFR-05).
package main

import (
	"net/http"

	"github.com/munisp/pfaengine/internal/appx"
	"github.com/munisp/pfaengine/internal/audit"
)

func main() {
	store := audit.NewStore()

	mux := http.NewServeMux()
	appx.Health(mux, "audit")

	mux.HandleFunc("POST /v1/audit/events", func(w http.ResponseWriter, r *http.Request) {
		var rec audit.Record
		if err := appx.Decode(r, &rec); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		rec.TenantID = appx.TenantOf(r)
		e := store.Append(rec)
		appx.JSON(w, 201, e)
	})

	mux.HandleFunc("POST /v1/audit/verify", func(w http.ResponseWriter, r *http.Request) {
		if err := store.Verify(); err != nil {
			appx.Err(w, 500, "CHAIN_BROKEN", err)
			return
		}
		appx.JSON(w, 200, map[string]any{"verified": true, "events": len(store.Events())})
	})

	mux.HandleFunc("POST /v1/audit/export", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Approver1, Approver2 string }
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		out, err := store.Export(appx.TenantOf(r), in.Approver1, in.Approver2)
		if err != nil {
			appx.Err(w, 403, "DUAL_CONTROL_REQUIRED", err)
			return
		}
		appx.JSON(w, 200, map[string]any{"tenant_id": appx.TenantOf(r), "events": out, "count": len(out)})
	})

	appx.Serve("audit", appx.Env("PORT", "8090"), appx.TenantMiddleware(mux))
}
