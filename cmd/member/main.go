// member-service: RSA registry, PenCom PIN issuance, nominees, employment
// history, channels data (§4, FR-107, FR-201, FR-204).
package main

import (
	"net/http"

	"github.com/munisp/pfaengine/internal/appx"
	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/member"
)

func main() {
	reg := member.NewRegistry()
	aud := audit.NewStore()

	mux := http.NewServeMux()
	appx.Health(mux, "member")

	mux.HandleFunc("POST /v1/members", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ FullName, DOB, Phone, Email string }
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		p := reg.CreatePerson(appx.TenantOf(r), in.FullName, in.DOB, in.Phone, in.Email)
		aud.Append(audit.Record{TenantID: appx.TenantOf(r), Actor: "api", Action: "member.created", EntityType: "person", EntityID: p.ID, After: p})
		appx.JSON(w, 201, p)
	})

	mux.HandleFunc("GET /v1/members/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, ok := reg.Person(appx.Path(r, "id"))
		if !ok {
			appx.Err(w, 404, "NOT_FOUND", errS("member not found"))
			return
		}
		appx.JSON(w, 200, p)
	})

	mux.HandleFunc("POST /v1/members/{id}/rsa", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			FundID, SchemeID string
			KYCPassed        bool `json:"kyc_passed"`
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		rsa, err := reg.OpenRSA(appx.TenantOf(r), appx.Path(r, "id"), in.FundID, in.SchemeID, in.KYCPassed)
		if err != nil {
			appx.Err(w, 422, "RSA_OPEN_FAILED", err)
			return
		}
		aud.Append(audit.Record{TenantID: appx.TenantOf(r), Actor: "api", Action: "rsa.opened", EntityType: "rsa", EntityID: rsa.ID, After: rsa})
		appx.JSON(w, 201, rsa)
	})

	mux.HandleFunc("POST /v1/rsa/{id}/pin/ack", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ PIN string }
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		if err := reg.AckPIN(appx.Path(r, "id"), in.PIN); err != nil {
			appx.Err(w, 409, "PIN_ACK_FAILED", err)
			return
		}
		appx.JSON(w, 200, map[string]string{"status": "ISSUED"})
	})

	mux.HandleFunc("POST /v1/rsa/{id}/nominees", func(w http.ResponseWriter, r *http.Request) {
		var n member.Nominee
		if err := appx.Decode(r, &n); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		if err := reg.AddNominee(appx.Path(r, "id"), n); err != nil {
			appx.Err(w, 422, "NOMINEE_FAILED", err)
			return
		}
		appx.JSON(w, 200, map[string]string{"status": "added"})
	})

	mux.HandleFunc("GET /v1/rsa/{id}", func(w http.ResponseWriter, r *http.Request) {
		rsa, ok := reg.GetRSA(appx.Path(r, "id"))
		if !ok {
			appx.Err(w, 404, "NOT_FOUND", errS("rsa not found"))
			return
		}
		appx.JSON(w, 200, rsa)
	})

	mux.HandleFunc("GET /v1/rsas", func(w http.ResponseWriter, r *http.Request) {
		appx.JSON(w, 200, reg.List(appx.TenantOf(r)))
	})

	appx.Serve("member", appx.Env("PORT", "8083"), appx.TenantMiddleware(mux))
}

type errS string

func (e errS) Error() string { return string(e) }
