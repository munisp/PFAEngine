// fund-pricing service: fund registry I–VI, daily NAV publication,
// unitization (§4 Phase 2, FR-304).
package main

import (
	"net/http"

	"github.com/munisp/pfaengine/internal/appx"
	"github.com/munisp/pfaengine/internal/funds"
	"github.com/munisp/pfaengine/internal/money"
)

func main() {
	reg := funds.NewRegistry()
	// Seed PenCom multi-fund structure.
	for _, f := range []funds.Fund{
		{ID: funds.FundI, Name: "Fund I (Aggressive)", Currency: "NGN"},
		{ID: funds.FundII, Name: "Fund II (Moderate, default ≤49)", Currency: "NGN"},
		{ID: funds.FundIII, Name: "Fund III (Conservative, 50+)", Currency: "NGN"},
		{ID: funds.FundIV, Name: "Fund IV (Retiree)", Currency: "NGN"},
		{ID: funds.FundV, Name: "Fund V (Micro/PPP)", Currency: "NGN"},
		{ID: funds.FundVI, Name: "Fund VI (Non-Interest)", Currency: "NGN"},
	} {
		reg.Register(f)
	}

	mux := http.NewServeMux()
	appx.Health(mux, "fund-pricing")

	mux.HandleFunc("POST /v1/funds/{id}/nav", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Date      string `json:"date"`
			PriceKobo int64  `json:"price_kobo"`
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		if err := reg.PublishNAV(appx.Path(r, "id"), in.Date, money.Kobo(in.PriceKobo)); err != nil {
			appx.Err(w, 404, "NO_FUND", err)
			return
		}
		appx.JSON(w, 200, map[string]string{"status": "published", "date": in.Date})
	})

	mux.HandleFunc("GET /v1/funds/{id}/nav", func(w http.ResponseWriter, r *http.Request) {
		date := r.URL.Query().Get("date")
		p, err := reg.Price(appx.Path(r, "id"), date)
		if err != nil {
			appx.Err(w, 404, "NO_NAV", err)
			return
		}
		appx.JSON(w, 200, map[string]any{"fund_id": appx.Path(r, "id"), "date": date, "price_kobo": p})
	})

	mux.HandleFunc("POST /v1/funds/{id}/allocate", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RSAID      string `json:"rsa_id"`
			Date       string `json:"date"`
			AmountKobo int64  `json:"amount_kobo"`
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		u, err := reg.Allocate(in.RSAID, appx.Path(r, "id"), in.Date, money.Kobo(in.AmountKobo))
		if err != nil {
			appx.Err(w, 422, "ALLOCATE_FAILED", err)
			return
		}
		appx.JSON(w, 200, map[string]any{"rsa_id": in.RSAID, "units": u.String()})
	})

	mux.HandleFunc("GET /v1/rsas/{id}/value", func(w http.ResponseWriter, r *http.Request) {
		appx.JSON(w, 200, map[string]any{
			"holdings":   reg.Holdings(appx.Path(r, "id")),
			"value_kobo": reg.Value(appx.Path(r, "id"), r.URL.Query().Get("date")),
		})
	})

	appx.Serve("fund-pricing", appx.Env("PORT", "8094"), appx.TenantMiddleware(mux))
}
