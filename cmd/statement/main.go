// statement-service: QR/HMAC-verifiable statements, on-demand and scheduled
// (§4, FR-501). PDF at /v1/members/{id}/statement.pdf, JSON payload at /statement.
package main

import (
	"net/http"
	"time"

	"github.com/munisp/pfaengine/internal/appx"
	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/statement"
)

var signingSecret = []byte("dev-statement-signing-secret")

func main() {
	mux := http.NewServeMux()
	appx.Health(mux, "statement")

	build := func(r *http.Request) statement.Payload {
		rsaID := appx.Path(r, "id")
		period := r.URL.Query().Get("period")
		if period == "" {
			period = time.Now().UTC().Format("2006-01")
		}
		p := statement.Payload{
			ID: "stmt-" + rsaID + "-" + period, TenantID: appx.TenantOf(r), RSAID: rsaID,
			MemberName: r.URL.Query().Get("name"), RSAPIN: rsaID, Period: period,
			ClosingBalance: money.NGN(0), GeneratedAt: time.Now().UTC(),
		}
		p.VerifyCode = statement.Sign(signingSecret, p.TenantID, p.RSAID, p.Period, p.ClosingBalance)
		return p
	}

	mux.HandleFunc("GET /v1/members/{id}/statement", func(w http.ResponseWriter, r *http.Request) {
		appx.JSON(w, 200, build(r))
	})

	mux.HandleFunc("GET /v1/members/{id}/statement.pdf", func(w http.ResponseWriter, r *http.Request) {
		pdf := statement.RenderPDF(build(r))
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", "inline; filename=statement.pdf")
		w.Write(pdf)
	})

	mux.HandleFunc("GET /v1/statements/verify", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var closing money.Kobo
		closing, _ = money.ParseKobo(q.Get("closing"))
		ok := statement.Verify(signingSecret, q.Get("tenant"), q.Get("rsa"), q.Get("period"), closing, q.Get("code"))
		appx.JSON(w, 200, map[string]bool{"valid": ok})
	})

	mux.HandleFunc("POST /v1/statements/schedules", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RSAID     string `json:"rsa_id"`
			Frequency string `json:"frequency"` // QUARTERLY default
			Channel   string `json:"channel"`
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		if in.Frequency == "" {
			in.Frequency = "QUARTERLY"
		}
		appx.JSON(w, 201, map[string]any{"status": "scheduled", "frequency": in.Frequency, "rsa_id": in.RSAID})
	})

	appx.Serve("statement", appx.Env("PORT", "8087"), appx.TenantMiddleware(mux))
}
