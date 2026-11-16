// member-svc: production-hardened member service (Tranche 8) wiring the
// domain packages behind the httpsrv layer: KYC-gated onboarding
// (kyc.VerifyNIN/ValidateBVN), ledger balances, contribution CSV upload
// (contribution.ParseCSV → recon.Engine → ledger post), and the
// reconciliation exception queue. In-memory stores behind the same
// interfaces as the other services.
package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/contribution"
	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/kyc"
	"github.com/munisp/pfaengine/internal/ledger"
	"github.com/munisp/pfaengine/internal/member"
	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/recon"
)

// deps bundles the domain stores the handlers operate on.
type deps struct {
	kyc     *kyc.Service
	members *member.Registry
	ledger  *ledger.Ledger
	recon   *recon.Engine
	audit   *audit.Store
}

func main() {
	salt := httpsrv.Getenv("KYC_HASH_SALT", "dev-salt-change-me")
	d := &deps{
		kyc: kyc.NewService(salt,
			kyc.MockProvider{ProviderName: "mock-nimc"},
			kyc.MockProvider{ProviderName: "mock-dojah"}),
		members: member.NewRegistry(),
		ledger:  ledger.New(),
		recon:   recon.NewEngine(),
		audit:   audit.NewStore(),
	}
	srv := httpsrv.New("member-svc", ":"+httpsrv.Getenv("PORT", "8096"), d.handler())
	log.Fatal(srv.Run())
}

// handler builds the tenant-scoped API surface. Health probes are added by
// httpsrv.New outside the tenant middleware.
func (d *deps) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/members", d.createMember)
	mux.HandleFunc("GET /api/v1/members/{id}/balance", d.balance)
	mux.HandleFunc("POST /api/v1/contributions/upload", d.uploadContributions)
	mux.HandleFunc("GET /api/v1/recon/exceptions", d.exceptions)
	return httpsrv.Tenant(mux)
}

// createMember onboards a member: NIN verification (mandatory, FR-101) and
// optional BVN validation (FR-102) run through the provider facade before
// the registry record and ledger accounts are created.
func (d *deps) createMember(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FullName string `json:"full_name"`
		DOB      string `json:"dob"`
		Phone    string `json:"phone"`
		Email    string `json:"email"`
		NIN      string `json:"nin"`
		BVN      string `json:"bvn,omitempty"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	if in.FullName == "" || in.NIN == "" {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST",
			errors.New("full_name and nin are required"))
		return
	}
	tenant := httpsrv.TenantOf(r)
	p := d.members.CreatePerson(tenant, in.FullName, in.DOB, in.Phone, in.Email)

	ninRec, dupOf, err := d.kyc.VerifyNIN(tenant, p.ID, in.NIN, in.FullName, in.DOB)
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "KYC_FAILED", err)
		return
	}
	var bvnVerified bool
	if in.BVN != "" {
		if _, _, err := d.kyc.ValidateBVN(tenant, p.ID, in.BVN, in.FullName, in.DOB, in.Phone); err != nil {
			httpsrv.WriteError(w, http.StatusUnprocessableEntity, "KYC_FAILED", err)
			return
		}
		bvnVerified = true
	}
	// Ledger accounts: one custody-cash asset per tenant, one RSA liability
	// per member (ErrAccountExist ignored — idempotent account creation).
	_ = d.ledger.OpenAccount(ledger.Account{ID: "custody-cash", TenantID: tenant, Name: "Custody Cash", Type: ledger.Asset, Currency: "NGN"})
	_ = d.ledger.OpenAccount(ledger.Account{ID: rsaAccount(p.ID), TenantID: tenant, Name: "RSA " + p.ID, Type: ledger.Liability, Currency: "NGN"})

	d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "member.onboarded", EntityType: "person", EntityID: p.ID, After: map[string]any{"nin_token": ninRec.Token != "", "bvn": bvnVerified}})
	out := map[string]any{
		"member_id":    p.ID,
		"full_name":    p.FullName,
		"nin_verified": true,
		"bvn_verified": bvnVerified,
	}
	if dupOf != "" {
		out["duplicate_of"] = dupOf
	}
	httpsrv.WriteJSON(w, http.StatusCreated, out)
}

// balance returns the member's RSA balance from the ledger (integer kobo).
func (d *deps) balance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := d.members.Person(id); !ok {
		httpsrv.WriteError(w, http.StatusNotFound, "NOT_FOUND", errors.New("member not found"))
		return
	}
	bal := d.ledger.Balance(rsaAccount(id))
	httpsrv.WriteJSON(w, http.StatusOK, map[string]any{
		"member_id":     id,
		"balance_kobo":  bal,
		"balance_naira": bal.Naira(),
	})
}

// uploadContributions ingests an employer CSV schedule:
//
//	rsa_pin,gross_kobo[,voluntary_kobo[,is_ppp[,received_kobo]]]
//
// rsa_pin is the member ID in this deployment. The optional received_kobo
// column represents the custodian remittance for that line; the schedule is
// reconciled against those receipts (recon.Engine) and only auto-matched
// lines post to the ledger — mismatches land in the exception queue.
func (d *deps) uploadContributions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	employer := r.URL.Query().Get("employer")
	if employer == "" {
		employer = "employer-self"
	}
	period := r.URL.Query().Get("period")
	validatePIN := func(pin string) bool {
		_, ok := d.members.Person(pin)
		return ok
	}
	sched, perr := contribution.ParseCSV(tenant, employer, period, bytes.NewReader(body), validatePIN)
	if sched == nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "PARSE_FAILED", perr)
		return
	}
	if perr != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "SCHEDULE_INVALID", perr)
		return
	}

	receipts, err := receiptsFromCSV(tenant, employer, body)
	if err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "RECEIPT_PARSE_FAILED", err)
		return
	}
	res := d.recon.Reconcile(sched, receipts)

	// Post auto-matched lines (exact PIN + amount) to the ledger.
	byPIN := map[string]money.Kobo{}
	for _, rc := range receipts {
		byPIN[rc.RSAPIN] = rc.Amount
	}
	var posted money.Kobo
	matched := 0
	for _, line := range sched.Lines {
		amt, ok := byPIN[line.RSAPIN]
		if !ok || amt != line.Total() {
			continue // recon raised an exception for this line
		}
		if _, err := d.ledger.Post(tenant, "contribution "+period, "upload", []ledger.Posting{
			{AccountID: "custody-cash", Debit: line.Total()},
			{AccountID: rsaAccount(line.RSAPIN), Credit: line.Total()},
		}); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "POST_FAILED", err)
			return
		}
		matched++
		posted += line.Total()
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "contribution.posted", EntityType: "schedule", EntityID: employer + "/" + period, After: map[string]any{"lines": len(sched.Lines), "matched": matched, "posted_kobo": posted}})
	httpsrv.WriteJSON(w, http.StatusCreated, map[string]any{
		"lines":           res.TotalLines,
		"auto_matched":    res.AutoMatched,
		"exceptions":      res.ExceptionCnt,
		"posted_lines":    matched,
		"posted_kobo":     posted,
		"auto_match_rate": res.AutoMatchRate(),
	})
}

// exceptions lists the tenant's open reconciliation exceptions.
func (d *deps) exceptions(w http.ResponseWriter, r *http.Request) {
	out := d.recon.Exceptions(httpsrv.TenantOf(r))
	if out == nil {
		out = []*recon.Exception{}
	}
	httpsrv.WriteJSON(w, http.StatusOK, out)
}

// receiptsFromCSV builds custodian receipts from the optional 5th CSV
// column (received_kobo). Lines without the column are treated as fully
// remitted, so clean files auto-match end-to-end.
func receiptsFromCSV(tenantID, employerID string, body []byte) ([]recon.Receipt, error) {
	rd := csv.NewReader(bytes.NewReader(body))
	rd.TrimLeadingSpace = true
	rows, err := rd.ReadAll()
	if err != nil {
		return nil, err
	}
	var receipts []recon.Receipt
	for i, row := range rows {
		if len(row) < 2 {
			continue
		}
		if i == 0 && strings.EqualFold(strings.TrimSpace(row[0]), "rsa_pin") {
			continue
		}
		pin := strings.TrimSpace(row[0])
		amt := ""
		if len(row) > 4 {
			amt = strings.TrimSpace(row[4])
		}
		if amt == "" {
			// Full remittance: employee + employer + voluntary.
			gross, gerr := parseAmount(row[1])
			if gerr != nil {
				return nil, gerr
			}
			var vol money.Kobo
			if len(row) > 2 && strings.TrimSpace(row[2]) != "" {
				v, verr := parseAmount(row[2])
				if verr != nil {
					return nil, verr
				}
				vol = v
			}
			line := contribution.SplitLine(pin, gross, vol, false)
			receipts = append(receipts, recon.Receipt{
				ID: rcptID(i), TenantID: tenantID, EmployerID: employerID,
				RSAPIN: pin, Amount: line.Total(), Source: "CUSTODIAN",
			})
			continue
		}
		v, perr := parseAmount(amt)
		if perr != nil {
			return nil, perr
		}
		receipts = append(receipts, recon.Receipt{
			ID: rcptID(i), TenantID: tenantID, EmployerID: employerID,
			RSAPIN: pin, Amount: v, Source: "CUSTODIAN",
		})
	}
	return receipts, nil
}

func parseAmount(s string) (money.Kobo, error) {
	s = strings.TrimSpace(s)
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		return money.Kobo(v), nil
	}
	return money.ParseKobo(s)
}

func rsaAccount(memberID string) string { return "member-rsa:" + memberID }

func rcptID(i int) string { return "rcpt-" + strconv.Itoa(i) }

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
