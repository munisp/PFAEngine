// employer-portal service: employer onboarding, schedule upload UX data,
// remittance file generation, compliance status (§4, FR-202/203).
package main

import (
	"net/http"
	"sync"
	"time"
)

import (
	"github.com/munisp/pfaengine/internal/appx"
	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/money"
)

type Employer struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	RCNumber     string    `json:"rc_number"`
	Name         string    `json:"name"`
	Contact      string    `json:"contact"`
	RemitTerms   string    `json:"remittance_terms"`
	Compliance   string    `json:"compliance_state"` // COMPLIANT | WATCH | DEFAULTING
	RegisteredAt time.Time `json:"registered_at"`
}

var (
	mu        sync.Mutex
	employers = map[string]*Employer{}
	seq       int
	aud       = audit.NewStore()
)

func main() {
	mux := http.NewServeMux()
	appx.Health(mux, "employer-portal")

	mux.HandleFunc("POST /v1/employers", func(w http.ResponseWriter, r *http.Request) {
		var e Employer
		if err := appx.Decode(r, &e); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		mu.Lock()
		seq++
		e.ID = "emp-" + itoa(seq)
		e.TenantID = appx.TenantOf(r)
		e.Compliance = "COMPLIANT"
		e.RegisteredAt = time.Now().UTC()
		employers[e.ID] = &e
		mu.Unlock()
		aud.Append(audit.Record{TenantID: e.TenantID, Actor: "employer-portal", Action: "employer.registered", EntityType: "employer", EntityID: e.ID, After: e})
		appx.JSON(w, 201, e)
	})

	mux.HandleFunc("GET /v1/employers/{id}/compliance", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		e, ok := employers[appx.Path(r, "id")]
		mu.Unlock()
		if !ok {
			appx.Err(w, 404, "NOT_FOUND", errS("employer not found"))
			return
		}
		appx.JSON(w, 200, map[string]any{"employer_id": e.ID, "compliance_state": e.Compliance})
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
