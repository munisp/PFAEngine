// identity-service: accounts, RBAC, TOTP MFA, tenant provisioning,
// branding/feature flags, consent records (§4; BR-05, FR-106, NFR-20).
//
// FR-520: tenant and staff provisioning run THROUGH the unified onboarding
// framework — POST /v1/tenants opens a TENANT case (PenCom licence
// evidence + capital adequacy check via internal/capital) and POST
// /v1/staff opens a STAFF case (RBAC role assignment + TOTP enrollment);
// both require maker-checker approval before the tenant is provisioned /
// the staff login is enabled.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/appx"
	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/authn"
	"github.com/munisp/pfaengine/internal/capital"
	"github.com/munisp/pfaengine/internal/kyc"
	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/onboarding"
	"github.com/munisp/pfaengine/internal/store"
	"github.com/munisp/pfaengine/internal/tenancy"
)

// staffPayload is the pending staff account behind a STAFF case.
type staffPayload struct {
	Email string
}

func main() {
	auth := authn.NewService(appx.Env("SIGNING_SECRET", "dev-signing-secret-0123456789"))
	tenants := tenancy.NewRegistry()
	aud := audit.NewStore()
	kycSvc := kyc.NewService(appx.Env("KYC_HASH_SALT", "dev-kyc-salt"))
	// Tranche 15b (durable persistence): when DATABASE_URL is set, TENANT
	// and STAFF onboarding cases write through to Postgres (same
	// onboarding.Repo as member-svc); otherwise purely in-memory.
	var onbRepo onboarding.Repo
	if dsn := appx.Env("DATABASE_URL", ""); dsn != "" {
		db, err := store.Open(context.Background(), dsn)
		if err != nil {
			log.Fatalf("identity: DATABASE_URL set but store.Open failed: %v", err)
		}
		defer db.Close()
		onbRepo = store.NewOnboardingRepo(db)
		log.Printf("identity: DATABASE_URL set — onboarding persists to Postgres")
	}
	onb := onboarding.NewPersistentStore(onboarding.NewStore(aud), onbRepo)

	var payloadMu sync.Mutex
	tenantPayloads := map[string]tenancy.Tenant{} // TENANT case ID -> tenant to provision
	staffPayloads := map[string]staffPayload{}    // STAFF case ID -> account email

	// activate runs the provisioning side-effect once a case reaches
	// ACTIVE (after maker-checker approval).
	activate := func(c *onboarding.Case) {
		if c.Status != onboarding.StatusActive || c.RefID != "" {
			return
		}
		payloadMu.Lock()
		t, isTenant := tenantPayloads[c.ID]
		sp, isStaff := staffPayloads[c.ID]
		payloadMu.Unlock()
		switch {
		case isTenant:
			out, err := tenants.Provision(t)
			if err != nil {
				return
			}
			aud.Append(audit.Record{TenantID: out.ID, Actor: "platform", Action: "tenant.provisioned", EntityType: "tenant", EntityID: out.ID, After: out})
			_ = onb.LinkRef(context.Background(), c.TenantID, c.ID, out.ID)
		case isStaff:
			if err := auth.SetDisabled(sp.Email, false); err == nil {
				_ = onb.LinkRef(context.Background(), c.TenantID, c.ID, sp.Email)
			}
		}
	}

	mux := http.NewServeMux()
	appx.Health(mux, "identity")

	// FR-520: open a TENANT onboarding case instead of direct provisioning;
	// the tenant record is created when the case is approved.
	mux.HandleFunc("POST /v1/tenants", func(w http.ResponseWriter, r *http.Request) {
		var t tenancy.Tenant
		if err := appx.Decode(r, &t); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		if t.ID == "" || t.Name == "" {
			appx.Err(w, 400, "BAD_REQUEST", errors.New("id and name required"))
			return
		}
		actor := strings.TrimSpace(r.Header.Get("X-Actor"))
		if actor == "" {
			actor = "platform-sales"
		}
		c, err := onb.Create(r.Context(), t.ID, onboarding.StakeholderTenant, actor, nil)
		if err != nil {
			appx.Err(w, 400, "ONBOARDING_FAILED", err)
			return
		}
		payloadMu.Lock()
		tenantPayloads[c.ID] = t
		payloadMu.Unlock()
		appx.JSON(w, 201, map[string]any{"case_id": c.ID, "tenant_id": t.ID, "status": c.Status, "case": c})
	})

	// FR-520: staff onboarding — create the (disabled) account and open a
	// STAFF case; rbac_role_assign + totp_enroll steps, then maker-checker
	// approval enables login.
	mux.HandleFunc("POST /v1/staff", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Email    string   `json:"email"`
			Password string   `json:"password"`
			Roles    []string `json:"roles"`
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		if in.Email == "" || in.Password == "" || len(in.Roles) == 0 {
			appx.Err(w, 400, "BAD_REQUEST", errors.New("email, password and roles are required"))
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
		if err := auth.SetDisabled(in.Email, true); err != nil {
			appx.Err(w, 500, "DISABLE_FAILED", err)
			return
		}
		actor := strings.TrimSpace(r.Header.Get("X-Actor"))
		if actor == "" {
			actor = in.Email
		}
		c, err := onb.Create(r.Context(), appx.TenantOf(r), onboarding.StakeholderStaff, actor, nil)
		if err != nil {
			appx.Err(w, 500, "ONBOARDING_FAILED", err)
			return
		}
		payloadMu.Lock()
		staffPayloads[c.ID] = staffPayload{Email: in.Email}
		payloadMu.Unlock()
		aud.Append(audit.Record{TenantID: appx.TenantOf(r), Actor: a.ID, Action: "staff.onboarding.opened", EntityType: "account", EntityID: a.ID, After: map[string]any{"case_id": c.ID, "roles": in.Roles}})
		appx.JSON(w, 201, map[string]any{"case_id": c.ID, "account_id": a.ID, "email": a.Email, "status": c.Status})
	})

	// FR-520: advance a TENANT or STAFF case step. licence evidence and the
	// capital adequacy check (internal/capital, FR-517) validate real
	// evidence; totp_enroll enrolls the staff account in MFA (RFC 6238).
	mux.HandleFunc("POST /v1/onboarding/{id}/steps/{step}", func(w http.ResponseWriter, r *http.Request) {
		id, step := appx.Path(r, "id"), appx.Path(r, "step")
		tenant := appx.TenantOf(r)
		c, ok := onb.Get(r.Context(), tenant, id)
		if !ok {
			appx.Err(w, 404, "NOT_FOUND", errors.New("onboarding case not found"))
			return
		}
		if c.Stakeholder != onboarding.StakeholderTenant && c.Stakeholder != onboarding.StakeholderStaff {
			appx.Err(w, 403, "WRONG_SERVICE", errors.New("this pipeline is advanced by its owning service"))
			return
		}
		var respExtra map[string]any
		switch step {
		case "license_evidence":
			payloadMu.Lock()
			t, found := tenantPayloads[id]
			payloadMu.Unlock()
			if found && strings.TrimSpace(t.LicenseNo) == "" {
				appx.Err(w, 422, "LICENSE_REQUIRED", errors.New("PenCom licence number evidence required"))
				return
			}
		case "capital_check":
			var in struct {
				ShareholdersFundsKobo money.Kobo `json:"shareholders_funds_kobo"`
				AUMKobo               money.Kobo `json:"aum_kobo"`
			}
			if err := appx.Decode(r, &in); err != nil {
				appx.Err(w, 400, "BAD_REQUEST", err)
				return
			}
			st := capital.Assess(in.ShareholdersFundsKobo, in.AUMKobo, time.Now().UTC())
			if !st.Compliant {
				appx.Err(w, 422, "CAPITAL_SHORTFALL", errors.New("shareholders' funds below required capital (FR-517)"))
				return
			}
		case "totp_enroll":
			payloadMu.Lock()
			sp, found := staffPayloads[id]
			payloadMu.Unlock()
			if !found {
				appx.Err(w, 409, "NO_ACCOUNT", errors.New("no staff account linked to case"))
				return
			}
			secret, err := auth.EnrollMFA(sp.Email)
			if err != nil {
				appx.Err(w, 404, "NOT_FOUND", err)
				return
			}
			respExtra = map[string]any{"totp_secret": secret}
		}
		if err := onb.Advance(r.Context(), tenant, id, step); err != nil {
			writeOnbError(w, err)
			return
		}
		c, _ = onb.Get(r.Context(), tenant, id)
		activate(c)
		c, _ = onb.Get(r.Context(), tenant, id)
		appx.JSON(w, 200, map[string]any{"case": c, "extra": respExtra})
	})

	mux.HandleFunc("GET /v1/onboarding/{id}", func(w http.ResponseWriter, r *http.Request) {
		c, ok := onb.Get(r.Context(), appx.TenantOf(r), appx.Path(r, "id"))
		if !ok {
			appx.Err(w, 404, "NOT_FOUND", errors.New("onboarding case not found"))
			return
		}
		appx.JSON(w, 200, c)
	})

	// FR-520 maker-checker approval: activates the case and provisions the
	// tenant / enables the staff login.
	mux.HandleFunc("POST /v1/onboarding/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		approver := strings.TrimSpace(r.Header.Get("X-Actor"))
		if approver == "" {
			appx.Err(w, 403, "STAFF_REQUIRED", errors.New("X-Actor staff identity header required"))
			return
		}
		id := appx.Path(r, "id")
		tenant := appx.TenantOf(r)
		if err := onb.Approve(r.Context(), tenant, id, approver); err != nil {
			writeOnbError(w, err)
			return
		}
		c, _ := onb.Get(r.Context(), tenant, id)
		activate(c)
		c, _ = onb.Get(r.Context(), tenant, id)
		appx.JSON(w, 200, c)
	})

	mux.HandleFunc("POST /v1/onboarding/{id}/reject", func(w http.ResponseWriter, r *http.Request) {
		approver := strings.TrimSpace(r.Header.Get("X-Actor"))
		if approver == "" {
			appx.Err(w, 403, "STAFF_REQUIRED", errors.New("X-Actor staff identity header required"))
			return
		}
		var in struct {
			Reason string `json:"reason"`
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		tenant := appx.TenantOf(r)
		if err := onb.Reject(r.Context(), tenant, appx.Path(r, "id"), approver, strings.TrimSpace(in.Reason)); err != nil {
			writeOnbError(w, err)
			return
		}
		c, _ := onb.Get(r.Context(), tenant, appx.Path(r, "id"))
		appx.JSON(w, 200, c)
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
			status := 401
			if errors.Is(err, authn.ErrAccountDisabled) {
				status = 403
			}
			appx.Err(w, status, "LOGIN_FAILED", err)
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

// writeOnbError maps onboarding domain errors onto HTTP statuses.
func writeOnbError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, onboarding.ErrCaseNotFound):
		appx.Err(w, 404, "NOT_FOUND", err)
	case errors.Is(err, onboarding.ErrMakerChecker):
		appx.Err(w, 403, "MAKER_CHECKER", err)
	case errors.Is(err, onboarding.ErrApproverRequired), errors.Is(err, onboarding.ErrReasonRequired):
		appx.Err(w, 400, "BAD_REQUEST", err)
	case errors.Is(err, onboarding.ErrUnknownStep):
		appx.Err(w, 400, "UNKNOWN_STEP", err)
	default:
		appx.Err(w, 409, "STEP_CONFLICT", err)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
