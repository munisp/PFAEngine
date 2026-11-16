// benefit-service: claims workflow, maker-checker, custodian instructions
// (§4 Phase 2, FR-401, SCM-06/07).
package main

import (
	"net/http"

	"github.com/munisp/pfaengine/internal/appx"
	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/benefit"
	"github.com/munisp/pfaengine/internal/money"
)

func main() {
	svc := benefit.NewService()
	aud := audit.NewStore()

	mux := http.NewServeMux()
	appx.Health(mux, "benefit")

	mux.HandleFunc("POST /v1/claims", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RSAID       string `json:"rsa_id"`
			Type        string `json:"type"`
			AmountKobo  int64  `json:"amount_kobo"`
			BalanceKobo int64  `json:"balance_kobo"`
			AgeYears    int    `json:"age_years"`
			RuleVersion string `json:"rule_version"`
			Actor       string `json:"actor"`
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		c, err := svc.Submit(appx.TenantOf(r), in.RSAID, benefit.ClaimType(in.Type),
			money.Kobo(in.AmountKobo), money.Kobo(in.BalanceKobo), in.AgeYears, in.RuleVersion, in.Actor)
		if err != nil {
			appx.Err(w, 422, "CLAIM_REJECTED", err)
			return
		}
		aud.Append(audit.Record{TenantID: appx.TenantOf(r), Actor: in.Actor, Action: "claim.submitted", EntityType: "claim", EntityID: c.ID, After: c})
		appx.JSON(w, 201, c)
	})

	mux.HandleFunc("POST /v1/claims/{id}/decision", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Approver string `json:"approver"`
			Approve  bool   `json:"approve"`
			Reason   string `json:"reason"`
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		c, err := svc.Decide(appx.Path(r, "id"), in.Approver, in.Approve, in.Reason)
		if err != nil {
			appx.Err(w, 422, "DECISION_FAILED", err)
			return
		}
		aud.Append(audit.Record{TenantID: appx.TenantOf(r), Actor: in.Approver, Action: "claim.decided", EntityType: "claim", EntityID: c.ID, After: c})
		appx.JSON(w, 200, c)
	})

	mux.HandleFunc("POST /v1/claims/{id}/instruct-payment", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ CustodianRef string }
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		if err := svc.InstructPayment(appx.Path(r, "id"), in.CustodianRef); err != nil {
			appx.Err(w, 422, "INSTRUCT_FAILED", err)
			return
		}
		appx.JSON(w, 200, map[string]string{"status": "PAYMENT_INSTRUCTED"})
	})

	mux.HandleFunc("GET /v1/claims/{id}", func(w http.ResponseWriter, r *http.Request) {
		c, ok := svc.Get(appx.Path(r, "id"))
		if !ok {
			appx.Err(w, 404, "NOT_FOUND", errS("claim not found"))
			return
		}
		appx.JSON(w, 200, c)
	})

	appx.Serve("benefit", appx.Env("PORT", "8092"), appx.TenantMiddleware(mux))
}

type errS string

func (e errS) Error() string { return string(e) }
