// contribution-service: schedule ingestion, 8/10 split, posting (§4, FR-301).
package main

import (
	"bytes"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/appx"
	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/contribution"
	"github.com/munisp/pfaengine/internal/ledger"
	"github.com/munisp/pfaengine/internal/money"
)

type state struct {
	mu      sync.Mutex
	scheds  map[string]*contribution.Schedule
	seq     int
	ledger  *ledger.Ledger
	history map[string][]contribution.Line // rsaPIN -> posted lines
}

func main() {
	st := &state{scheds: map[string]*contribution.Schedule{}, ledger: ledger.New(), history: map[string][]contribution.Line{}}
	aud := audit.NewStore()
	validatePIN := func(pin string) bool { return len(pin) >= 6 } // PenCom PCRS PIN check adapter (INT-01)

	mux := http.NewServeMux()
	appx.Health(mux, "contribution")

	mux.HandleFunc("POST /v1/employers/{id}/schedules", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var sched *contribution.Schedule
		var err error
		if r.Header.Get("Content-Type") == "application/json" {
			sched, err = contribution.ParseJSON(appx.TenantOf(r), appx.Path(r, "id"), r.URL.Query().Get("period"), body, validatePIN)
		} else {
			sched, err = contribution.ParseCSV(appx.TenantOf(r), appx.Path(r, "id"), r.URL.Query().Get("period"), bytes.NewReader(body), validatePIN)
		}
		if sched == nil {
			appx.Err(w, 400, "PARSE_FAILED", err)
			return
		}
		st.mu.Lock()
		st.seq++
		sched.ID = "sched-" + itoa(st.seq)
		st.scheds[sched.ID] = sched
		st.mu.Unlock()
		aud.Append(audit.Record{TenantID: appx.TenantOf(r), Actor: appx.Path(r, "id"), Action: "contribution.scheduled", EntityType: "schedule", EntityID: sched.ID, After: map[string]any{"lines": len(sched.Lines), "status": sched.Status}})
		if err != nil {
			appx.JSON(w, 422, sched) // validation errors surfaced with detail
			return
		}
		appx.JSON(w, 201, sched)
	})

	mux.HandleFunc("POST /v1/contributions", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ScheduleID string `json:"schedule_id"`
			Date       string `json:"date"`
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		st.mu.Lock()
		sched, ok := st.scheds[in.ScheduleID]
		st.mu.Unlock()
		if !ok {
			appx.Err(w, 404, "NOT_FOUND", errS("schedule not found"))
			return
		}
		posted := 0
		for _, line := range sched.Lines {
			cashID := "custody-cash"
			rsaID := "member-rsa:" + line.RSAPIN
			st.ledger.OpenAccount(ledger.Account{ID: cashID, TenantID: sched.TenantID, Name: "Custody Cash", Type: ledger.Asset, Currency: "NGN"})
			st.ledger.OpenAccount(ledger.Account{ID: rsaID, TenantID: sched.TenantID, Name: "RSA " + line.RSAPIN, Type: ledger.Liability, Currency: "NGN"})
			if _, err := st.ledger.Post(sched.TenantID, "contribution "+sched.Period, sched.ID, []ledger.Posting{
				{AccountID: cashID, Debit: line.Total()},
				{AccountID: rsaID, Credit: line.Total()},
			}); err != nil {
				continue
			}
			st.history[line.RSAPIN] = append(st.history[line.RSAPIN], line)
			posted++
			aud.Append(audit.Record{TenantID: sched.TenantID, Actor: "contribution-service", Action: "contribution.posted", EntityType: "contribution", EntityID: sched.ID + "/" + line.RSAPIN, After: line})
		}
		sched.Status = contribution.SchedPosted
		sched.PostedAt = time.Now().UTC()
		appx.JSON(w, 200, map[string]any{"schedule_id": sched.ID, "posted_lines": posted, "total_kobo": sched.Total(), "status": sched.Status})
	})

	mux.HandleFunc("GET /v1/members/{id}/contributions", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		lines := st.history[appx.Path(r, "id")] // id = RSA PIN here
		st.mu.Unlock()
		var total money.Kobo
		for _, l := range lines {
			total += l.Total()
		}
		appx.JSON(w, 200, map[string]any{"rsa_pin": appx.Path(r, "id"), "lines": lines, "total_kobo": total})
	})

	appx.Serve("contribution", appx.Env("PORT", "8084"), appx.TenantMiddleware(mux))
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

type errS string

func (e errS) Error() string { return string(e) }
