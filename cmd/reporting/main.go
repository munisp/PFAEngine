// reporting-service: statutory returns with sign-off, inspection packs
// (§4, FR-502, REG-09).
package main

import (
	"net/http"

	"github.com/munisp/pfaengine/internal/appx"
	"github.com/munisp/pfaengine/internal/regx"
	"github.com/munisp/pfaengine/internal/reporting"
)

func main() {
	svc := reporting.NewService()
	regAdapters := regx.NewRegistry()

	mux := http.NewServeMux()
	appx.Health(mux, "reporting")

	mux.HandleFunc("GET /v1/reports/formats", func(w http.ResponseWriter, r *http.Request) {
		j := r.URL.Query().Get("jurisdiction")
		a, err := regAdapters.For(j)
		if err != nil {
			appx.Err(w, 404, "NO_ADAPTER", err)
			return
		}
		appx.JSON(w, 200, a.ReportFormats())
	})

	mux.HandleFunc("GET /v1/reports/{code}", func(w http.ResponseWriter, r *http.Request) {
		ret := svc.Generate(appx.TenantOf(r), appx.Path(r, "code"),
			r.URL.Query().Get("period"),
			r.URL.Query().Get("rule_version"),
			map[string]any{"source": "platform", "jurisdiction": r.URL.Query().Get("jurisdiction")})
		appx.JSON(w, 200, ret)
	})

	mux.HandleFunc("POST /v1/reports/{id}/signoff", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Officer1, Officer2 string }
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		if err := svc.SignOff(appx.Path(r, "id"), in.Officer1, in.Officer2); err != nil {
			appx.Err(w, 422, "SIGNOFF_FAILED", err)
			return
		}
		appx.JSON(w, 200, map[string]string{"status": "SIGNED_OFF"})
	})

	mux.HandleFunc("POST /v1/reports/{id}/file", func(w http.ResponseWriter, r *http.Request) {
		if err := svc.File(appx.Path(r, "id")); err != nil {
			appx.Err(w, 422, "FILE_FAILED", err)
			return
		}
		appx.JSON(w, 200, map[string]string{"status": "FILED"})
	})

	appx.Serve("reporting", appx.Env("PORT", "8088"), appx.TenantMiddleware(mux))
}
