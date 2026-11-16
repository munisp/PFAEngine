// ledger-service: double-entry postings, trial balance, replay (§4, FR-302/306).
package main

import (
	"net/http"

	"github.com/munisp/pfaengine/internal/appx"
	"github.com/munisp/pfaengine/internal/ledger"
	"github.com/munisp/pfaengine/internal/money"
)

func main() {
	lg := ledger.New()

	mux := http.NewServeMux()
	appx.Health(mux, "ledger")

	mux.HandleFunc("POST /v1/ledger/accounts", func(w http.ResponseWriter, r *http.Request) {
		var a ledger.Account
		if err := appx.Decode(r, &a); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		a.TenantID = appx.TenantOf(r)
		if err := lg.OpenAccount(a); err != nil {
			appx.Err(w, 409, "ACCOUNT_EXISTS", err)
			return
		}
		appx.JSON(w, 201, a)
	})

	mux.HandleFunc("POST /v1/ledger/entries", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Memo      string           `json:"memo"`
			Reference string           `json:"reference"`
			Postings  []ledger.Posting `json:"postings"`
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		e, err := lg.Post(appx.TenantOf(r), in.Memo, in.Reference, in.Postings)
		if err != nil {
			appx.Err(w, 422, "POST_FAILED", err)
			return
		}
		appx.JSON(w, 201, e)
	})

	mux.HandleFunc("POST /v1/ledger/entries/{id}/reverse", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Memo string }
		_ = appx.Decode(r, &in)
		e, err := lg.Reverse(appx.Path(r, "id"), in.Memo)
		if err != nil {
			appx.Err(w, 422, "REVERSAL_FAILED", err)
			return
		}
		appx.JSON(w, 201, e)
	})

	mux.HandleFunc("GET /v1/ledger/accounts/{id}/balance", func(w http.ResponseWriter, r *http.Request) {
		appx.JSON(w, 200, map[string]any{"account_id": appx.Path(r, "id"), "balance_kobo": lg.Balance(appx.Path(r, "id"))})
	})

	mux.HandleFunc("GET /v1/ledger/trial-balance", func(w http.ResponseWriter, r *http.Request) {
		tb := lg.TrialBalance()
		out := map[string]string{}
		for id, b := range tb {
			out[id] = b.Naira()
		}
		appx.JSON(w, 200, out)
	})

	mux.HandleFunc("POST /v1/ledger/replay", func(w http.ResponseWriter, r *http.Request) {
		tb := lg.Replay()
		appx.JSON(w, 200, map[string]any{"entries": len(lg.Entries()), "trial_balance_accounts": len(tb)})
	})

	_ = money.NGN // keep import for money semantics in postings
	appx.Serve("ledger", appx.Env("PORT", "8086"), appx.TenantMiddleware(mux))
}
