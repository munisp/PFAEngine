// identity-service: accounts, RBAC, TOTP MFA, tenant provisioning,
// branding/feature flags, consent records (§4; BR-05, FR-106, NFR-20).
package main

import (
	"net/http"
	"time"

	"github.com/munisp/pfaengine/internal/appx"
	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/authn"
	"github.com/munisp/pfaengine/internal/kyc"
	"github.com/munisp/pfaengine/internal/tenancy"
)

func main() {
	auth := authn.NewService(appx.Env("SIGNING_SECRET", "dev-signing-secret-0123456789"))
	tenants := tenancy.NewRegistry()
	aud := audit.NewStore()
	kycSvc := kyc.NewService(appx.Env("KYC_HASH_SALT", "dev-kyc-salt"))

	mux := http.NewServeMux()
	appx.Health(mux, "identity")

	mux.HandleFunc("POST /v1/tenants", func(w http.ResponseWriter, r *http.Request) {
		var t tenancy.Tenant
		if err := appx.Decode(r, &t); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		out, err := tenants.Provision(t)
		if err != nil {
			appx.Err(w, 400, "PROVISION_FAILED", err)
			return
		}
		aud.Append(audit.Record{TenantID: out.ID, Actor: "platform", Action: "tenant.provisioned", EntityType: "tenant", EntityID: out.ID, After: out})
		appx.JSON(w, 201, out)
	})

	mux.HandleFunc("GET /v1/tenants", func(w http.ResponseWriter, r *http.Request) {
		appx.JSON(w, 200, tenants.List())
	})

	mux.HandleFunc("GET /v1/tenants/{id}/residency", func(w http.ResponseWriter, r *http.Request) {
		t, ok := tenants.Get(appx.Path(r, "id"))
		if !ok {
			appx.Err(w, 404, "NOT_FOUND", errString("tenant not found"))
			return
		}
		appx.JSON(w, 200, tenants.ResidencyAttestation(t))
	})

	mux.HandleFunc("POST /v1/accounts", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Email    string   `json:"email"`
			Password string   `json:"password"`
			Roles    []string `json:"roles"`
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		roles := make([]authn.Role, len(in.Roles))
		for i, ro := range in.Roles {
			roles[i] = authn.Role(ro)
		}
		a, err := auth.CreateAccount(appx.TenantOf(r), in.Email, in.Password, roles...)
		if err != nil {
			appx.Err(w, 409, "ACCOUNT_EXISTS", err)
			return
		}
		aud.Append(audit.Record{TenantID: appx.TenantOf(r), Actor: a.ID, Action: "account.created", EntityType: "account", EntityID: a.ID})
		appx.JSON(w, 201, a)
	})

	mux.HandleFunc("POST /v1/accounts/{email}/mfa/enroll", func(w http.ResponseWriter, r *http.Request) {
		secret, err := auth.EnrollMFA(appx.Path(r, "email"))
		if err != nil {
			appx.Err(w, 404, "NOT_FOUND", err)
			return
		}
		appx.JSON(w, 200, map[string]string{"totp_secret": secret})
	})

	mux.HandleFunc("POST /v1/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Email, Password, TOTP string
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		tok, err := auth.Login(in.Email, in.Password, in.TOTP, time.Now())
		if err != nil {
			appx.Err(w, 401, "LOGIN_FAILED", err)
			return
		}
		appx.JSON(w, 200, map[string]string{"token": tok})
	})

	// NDPA consent endpoints (FR-106) — owned by identity for session/consent.
	mux.HandleFunc("POST /v1/consents", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			PersonID, Purpose, WordingVersion string
			Granted                           bool
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		c := kycSvc.RecordConsent(appx.TenantOf(r), in.PersonID, in.Purpose, in.WordingVersion, in.Granted)
		aud.Append(audit.Record{TenantID: appx.TenantOf(r), Actor: in.PersonID, Action: "consent.recorded", EntityType: "consent", EntityID: c.ID, After: c, ConsentRef: c.ID})
		appx.JSON(w, 201, c)
	})

	mux.HandleFunc("GET /v1/consents", func(w http.ResponseWriter, r *http.Request) {
		appx.JSON(w, 200, kycSvc.Consents(r.URL.Query().Get("person_id")))
	})

	appx.Serve("identity", appx.Env("PORT", "8081"), appx.TenantMiddleware(mux))
}

type errString string

func (e errString) Error() string { return string(e) }
