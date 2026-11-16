// kyc-service: NIN/BVN verification, liveness/face match, agent-assisted
// enrollment, duplicate detection (§4, FR-101..109, INT-02..04).
package main

import (
	"net/http"

	"github.com/munisp/pfaengine/internal/appx"
	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/kyc"
	"github.com/munisp/pfaengine/internal/vendors"
	"os"
)

func main() {
	// INT-02: >=2 contracted aggregators from day one. Real adapters when
	// credentials are present; deterministic mocks otherwise (dev/test).
	providers := []kyc.Provider{}
	if os.Getenv("SMILEID_API_KEY") != "" {
		providers = append(providers, &vendors.SmileID{
			PartnerID: os.Getenv("SMILEID_PARTNER_ID"), APIKey: os.Getenv("SMILEID_API_KEY"),
			BaseURL: appx.Env("SMILEID_BASE_URL", "https://api.smileidentity.com/v2"),
		})
	} else {
		providers = append(providers, kyc.MockProvider{ProviderName: appx.Env("KYC_PROVIDER_A", "smileid")})
	}
	if os.Getenv("DOJAH_SECRET_KEY") != "" {
		providers = append(providers, &vendors.Dojah{
			AppID: os.Getenv("DOJAH_APP_ID"), SecretKey: os.Getenv("DOJAH_SECRET_KEY"),
			BaseURL: appx.Env("DOJAH_BASE_URL", "https://api.dojah.io"),
		})
	} else {
		providers = append(providers, kyc.MockProvider{ProviderName: appx.Env("KYC_PROVIDER_B", "dojah")})
	}
	svc := kyc.NewService(appx.Env("KYC_HASH_SALT", "dev-kyc-salt"), providers...)
	aud := audit.NewStore()

	mux := http.NewServeMux()
	appx.Health(mux, "kyc")

	mux.HandleFunc("POST /v1/kyc/nin/verify", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ PersonID, NIN, Name, DOB string }
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		rec, dup, err := svc.VerifyNIN(appx.TenantOf(r), in.PersonID, in.NIN, in.Name, in.DOB)
		if err != nil {
			appx.Err(w, 502, "NIN_VERIFY_FAILED", err)
			return
		}
		aud.Append(audit.Record{TenantID: appx.TenantOf(r), Actor: in.PersonID, Action: "kyc.nin.verified", EntityType: "kyc", EntityID: in.PersonID, After: map[string]string{"provider_ref": rec.ProviderRef, "evidence": rec.EvidenceRef}, KYCRef: rec.ProviderRef})
		appx.JSON(w, 200, map[string]any{"token": rec.Token, "provider_ref": rec.ProviderRef, "evidence_ref": rec.EvidenceRef, "duplicate_of": dup})
	})

	mux.HandleFunc("POST /v1/kyc/bvn/verify", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ PersonID, BVN, Name, DOB, Phone string }
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		rec, dup, err := svc.ValidateBVN(appx.TenantOf(r), in.PersonID, in.BVN, in.Name, in.DOB, in.Phone)
		if err != nil {
			appx.Err(w, 502, "BVN_VERIFY_FAILED", err)
			return
		}
		aud.Append(audit.Record{TenantID: appx.TenantOf(r), Actor: in.PersonID, Action: "kyc.bvn.validated", EntityType: "kyc", EntityID: in.PersonID, KYCRef: rec.EvidenceRef})
		appx.JSON(w, 200, map[string]any{"bvn_hash": rec.BVNHash, "phone_bound": rec.PhoneBound, "duplicate_of": dup})
	})

	mux.HandleFunc("POST /v1/kyc/liveness", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			PersonID  string `json:"person_id"`
			Kind      string `json:"kind"` // ENROLL | IAM_ALIVE
			SelfieRef string `json:"selfie_ref"`
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		chk, err := svc.SubmitLiveness(appx.TenantOf(r), in.PersonID, kyc.LivenessKind(in.Kind), in.SelfieRef)
		if err != nil {
			appx.Err(w, 422, "LIVENESS_FAILED", err)
			return
		}
		aud.Append(audit.Record{TenantID: appx.TenantOf(r), Actor: in.PersonID, Action: "kyc.liveness." + string(chk.Status), EntityType: "liveness", EntityID: chk.ID, After: chk, KYCRef: chk.EvidenceRef})
		appx.JSON(w, 200, chk)
	})

	mux.HandleFunc("GET /v1/kyc/liveness/{id}", func(w http.ResponseWriter, r *http.Request) {
		personID := r.URL.Query().Get("person_id")
		kind := r.URL.Query().Get("kind")
		chk, ok := svc.Liveness(personID, kyc.LivenessKind(kind))
		if !ok {
			appx.Err(w, 404, "NOT_FOUND", errS("no liveness check"))
			return
		}
		appx.JSON(w, 200, chk)
	})

	mux.HandleFunc("GET /v1/kyc/queued", func(w http.ResponseWriter, r *http.Request) {
		appx.JSON(w, 200, map[string]any{"queued": svc.Queued()})
	})

	appx.Serve("kyc", appx.Env("PORT", "8082"), appx.TenantMiddleware(mux))
}

type errS string

func (e errS) Error() string { return string(e) }
