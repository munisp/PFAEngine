// employer-portal service: employer onboarding, schedule upload UX data,
// remittance file generation, compliance status (§4, FR-202/203).
//
// FR-520: employer registration runs THROUGH the unified onboarding
// framework — POST /v1/employers opens an EMPLOYER onboarding case
// (rc_validate -> glip_evidence (FR-515 3x rule) -> mandate_form ->
// pssp_link -> maker-checker approval) instead of a bare create. The
// employer becomes COMPLIANT only when the case reaches ACTIVE.
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
	"github.com/munisp/pfaengine/internal/employer"
	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/onboarding"
	"github.com/munisp/pfaengine/internal/store"
)

// onb is the write-through onboarding store (Postgres when DATABASE_URL
// is set, in-memory otherwise). Package-level so handlers share it; set in
// main before serving.
var onb *onboarding.PersistentStore

type Employer struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	RCNumber     string    `json:"rc_number"`
	Name         string    `json:"name"`
	Contact      string    `json:"contact"`
	RemitTerms   string    `json:"remittance_terms"`
	Compliance   string    `json:"compliance_state"` // PENDING | COMPLIANT | WATCH | DEFAULTING
	RegisteredAt time.Time `json:"registered_at"`
}

var (
	mu             sync.Mutex
	employers      = map[string]*Employer{}
	caseByEmployer = map[string]string{}
	seq            int
	aud            = audit.NewStore()
)

func main() {
	// Tranche 15b (durable persistence): when DATABASE_URL is set,
	// employer onboarding cases write through to Postgres (same
	// onboarding.Repo as member-svc); otherwise purely in-memory.
	var onbRepo onboarding.Repo
	if dsn := appx.Env("DATABASE_URL", ""); dsn != "" {
		db, err := store.Open(context.Background(), dsn)
		if err != nil {
			log.Fatalf("employer-portal: DATABASE_URL set but store.Open failed: %v", err)
		}
		defer db.Close()
		onbRepo = store.NewOnboardingRepo(db)
		log.Printf("employer-portal: DATABASE_URL set — onboarding persists to Postgres")
	}
	onb = onboarding.NewPersistentStore(onboarding.NewStore(aud), onbRepo)

	mux := http.NewServeMux()
	appx.Health(mux, "employer-portal")

	// FR-520: registration opens an EMPLOYER onboarding case; the employer
	// record starts PENDING and flips to COMPLIANT on case activation.
	mux.HandleFunc("POST /v1/employers", func(w http.ResponseWriter, r *http.Request) {
		var e Employer
		if err := appx.Decode(r, &e); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		actor := strings.TrimSpace(r.Header.Get("X-Actor"))
		if actor == "" {
			actor = strings.TrimSpace(e.Contact)
		}
		if actor == "" {
			actor = "employer-self"
		}
		tenant := appx.TenantOf(r)
		c, err := onb.Create(r.Context(), tenant, onboarding.StakeholderEmployer, actor, nil)
		if err != nil {
			appx.Err(w, 500, "ONBOARDING_FAILED", err)
			return
		}
		mu.Lock()
		seq++
		e.ID = "emp-" + itoa(seq)
		e.TenantID = tenant
		e.Compliance = "PENDING"
		e.RegisteredAt = time.Now().UTC()
		employers[e.ID] = &e
		caseByEmployer[e.ID] = c.ID
		mu.Unlock()
		_ = onb.LinkRef(r.Context(), tenant, c.ID, e.ID)
		aud.Append(audit.Record{TenantID: e.TenantID, Actor: "employer-portal", Action: "employer.registered", EntityType: "employer", EntityID: e.ID, After: map[string]any{"employer": e, "case_id": c.ID}})
		appx.JSON(w, 201, map[string]any{"employer": e, "case_id": c.ID, "onboarding_status": c.Status})
	})

	// FR-520: advance an employer case step. glip_evidence validates the
	// FR-515 3x-emolument GLIP rule before the step completes.
	mux.HandleFunc("POST /v1/employers/{id}/steps/{step}", func(w http.ResponseWriter, r *http.Request) {
		emp, caseID, ok := lookupEmployer(appx.Path(r, "id"))
		if !ok {
			appx.Err(w, 404, "NOT_FOUND", errS("employer not found"))
			return
		}
		step := appx.Path(r, "step")
		if step == "glip_evidence" {
			var in struct {
				Insurer             string     `json:"insurer"`
				PolicyNo            string     `json:"policy_no"`
				CoverMultipleX100   int64      `json:"cover_multiple_x100"`
				Expiry              time.Time  `json:"expiry"`
				AnnualEmolumentKobo money.Kobo `json:"annual_emolument_kobo"`
				CoverAmountKobo     money.Kobo `json:"cover_amount_kobo"`
			}
			if err := appx.Decode(r, &in); err != nil {
				appx.Err(w, 400, "BAD_REQUEST", err)
				return
			}
			g := employer.GLIP{
				EmployerID: emp.ID, Insurer: in.Insurer, PolicyNo: in.PolicyNo,
				CoverMultiple: in.CoverMultipleX100, Expiry: in.Expiry,
			}
			if err := g.Valid(in.AnnualEmolumentKobo, in.CoverAmountKobo, time.Now().UTC()); err != nil {
				appx.Err(w, 422, "GLIP_INVALID", err)
				return
			}
		}
		if err := onb.Advance(r.Context(), emp.TenantID, caseID, step); err != nil {
			writeOnbErr(w, err)
			return
		}
		c, _ := onb.Get(r.Context(), emp.TenantID, caseID)
		appx.JSON(w, 200, c)
	})

	// FR-520 maker-checker approval: the approver (X-Actor) must differ
	// from the case submitter. On ACTIVE the employer becomes COMPLIANT.
	mux.HandleFunc("POST /v1/employers/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		emp, caseID, ok := lookupEmployer(appx.Path(r, "id"))
		if !ok {
			appx.Err(w, 404, "NOT_FOUND", errS("employer not found"))
			return
		}
		approver := strings.TrimSpace(r.Header.Get("X-Actor"))
		if approver == "" {
			appx.Err(w, 403, "STAFF_REQUIRED", errS("X-Actor staff identity header required"))
			return
		}
		if err := onb.Approve(r.Context(), emp.TenantID, caseID, approver); err != nil {
			writeOnbErr(w, err)
			return
		}
		mu.Lock()
		emp.Compliance = "COMPLIANT"
		mu.Unlock()
		aud.Append(audit.Record{TenantID: emp.TenantID, Actor: approver, Action: "employer.activated", EntityType: "employer", EntityID: emp.ID, After: map[string]any{"case_id": caseID, "compliance_state": "COMPLIANT"}})
		c, _ := onb.Get(r.Context(), emp.TenantID, caseID)
		appx.JSON(w, 200, map[string]any{"employer_id": emp.ID, "compliance_state": emp.Compliance, "case": c})
	})

	mux.HandleFunc("POST /v1/employers/{id}/reject", func(w http.ResponseWriter, r *http.Request) {
		emp, caseID, ok := lookupEmployer(appx.Path(r, "id"))
		if !ok {
			appx.Err(w, 404, "NOT_FOUND", errS("employer not found"))
			return
		}
		approver := strings.TrimSpace(r.Header.Get("X-Actor"))
		if approver == "" {
			appx.Err(w, 403, "STAFF_REQUIRED", errS("X-Actor staff identity header required"))
			return
		}
		var in struct {
			Reason string `json:"reason"`
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		if err := onb.Reject(r.Context(), emp.TenantID, caseID, approver, strings.TrimSpace(in.Reason)); err != nil {
			writeOnbErr(w, err)
			return
		}
		aud.Append(audit.Record{TenantID: emp.TenantID, Actor: approver, Action: "employer.rejected", EntityType: "employer", EntityID: emp.ID, After: map[string]any{"case_id": caseID, "reason": in.Reason}})
		c, _ := onb.Get(r.Context(), emp.TenantID, caseID)
		appx.JSON(w, 200, c)
	})

	mux.HandleFunc("GET /v1/employers/{id}/compliance", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		e, ok := employers[appx.Path(r, "id")]
		var caseID string
		if ok {
			caseID = caseByEmployer[e.ID]
		}
		mu.Unlock()
		if !ok {
			appx.Err(w, 404, "NOT_FOUND", errS("employer not found"))
			return
		}
		out := map[string]any{"employer_id": e.ID, "compliance_state": e.Compliance, "case_id": caseID}
		if c, found := onb.Get(r.Context(), e.TenantID, caseID); found {
			out["onboarding_status"] = c.Status
		}
		appx.JSON(w, 200, out)
	})

	// Remittance file generation (FR-203): payment file for the bank rails.
	mux.HandleFunc("POST /v1/employers/{id}/remittance-file", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ScheduleID string `json:"schedule_id"`
			TotalKobo  int64  `json:"total_kobo"`
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		appx.JSON(w, 201, map[string]any{
			"file_id": "rem-" + in.ScheduleID, "format": "ISO20022-pain.001-derived",
			"total": money.Kobo(in.TotalKobo).Naira(), "rail": "Remita/Interswitch",
		})
	})

	appx.Serve("employer-portal", appx.Env("PORT", "8091"), appx.TenantMiddleware(mux))
}

func lookupEmployer(id string) (*Employer, string, bool) {
	mu.Lock()
	defer mu.Unlock()
	e, ok := employers[id]
	if !ok {
		return nil, "", false
	}
	return e, caseByEmployer[id], true
}

// writeOnbErr maps onboarding domain errors onto HTTP statuses.
func writeOnbErr(w http.ResponseWriter, err error) {
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

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

type errS string

func (e errS) Error() string { return string(e) }
