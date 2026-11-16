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
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/authn"
	"github.com/munisp/pfaengine/internal/contribution"
	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/i18n"
	"github.com/munisp/pfaengine/internal/kyc"
	"github.com/munisp/pfaengine/internal/ledger"
	"github.com/munisp/pfaengine/internal/member"
	"github.com/munisp/pfaengine/internal/micropension"
	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/onboarding"
	"github.com/munisp/pfaengine/internal/prefs"
	"github.com/munisp/pfaengine/internal/recon"
	"github.com/munisp/pfaengine/internal/retiree"
	"github.com/munisp/pfaengine/internal/statement"
)

// deps bundles the domain stores the handlers operate on.
type deps struct {
	kyc      *kyc.Service
	members  *member.Registry
	ledger   *ledger.Ledger
	recon    *recon.Engine
	audit    *audit.Store
	authn    *authn.Service
	retirees *retiree.Register
	prefs    *prefs.Store
	alive    *aliveTracker
	// onb is the unified onboarding case store (FR-520); onbPayloads holds
	// the per-case submission payloads the step handlers and activation
	// hooks need, and micro holds the micro-pension accounts opened for
	// MICRO_MEMBER cases.
	onb         *onboarding.Store
	onbMu       sync.Mutex
	onbPayloads map[string]onboardPayload
	microMu     sync.Mutex
	micro       map[string]*micropension.Account
	// stmtSecret signs statement verification codes.
	stmtSecret []byte
}

func main() {
	salt := httpsrv.Getenv("KYC_HASH_SALT", "dev-salt-change-me")
	aud := audit.NewStore()
	d := &deps{
		kyc: kyc.NewService(salt,
			kyc.MockProvider{ProviderName: "mock-nimc"},
			kyc.MockProvider{ProviderName: "mock-dojah"}),
		members:     member.NewRegistry(),
		ledger:      ledger.New(),
		recon:       recon.NewEngine(),
		audit:       aud,
		authn:       authn.NewService(httpsrv.Getenv("SIGNING_SECRET", "dev-signing-secret-0123456789")),
		retirees:    retiree.NewRegister(),
		prefs:       prefs.NewStore(),
		alive:       newAliveTracker(),
		onb:         onboarding.NewStore(aud),
		onbPayloads: map[string]onboardPayload{},
		micro:       map[string]*micropension.Account{},
		stmtSecret:  []byte(httpsrv.Getenv("STATEMENT_SIGNING_SECRET", "dev-statement-signing-secret")),
	}
	srv := httpsrv.New("member-svc", ":"+httpsrv.Getenv("PORT", "8096"), d.handler())
	log.Fatal(srv.Run())
}

// routeEntry is one registered API route.
type routeEntry struct {
	method  string
	pattern string
	handler func(*deps) http.HandlerFunc
}

// apiRoutes is the single source of truth for the mux and the exported
// route table (OpenAPI contract test).
var apiRoutes = []routeEntry{
	{"POST", "/api/v1/members", func(d *deps) http.HandlerFunc { return d.createMember }},
	{"POST", "/api/v1/members/{id}/alive", func(d *deps) http.HandlerFunc { return d.submitAlive }},
	{"GET", "/api/v1/members/{id}/alive", func(d *deps) http.HandlerFunc { return d.aliveStatus }},
	{"PUT", "/api/v1/members/{id}/locale", func(d *deps) http.HandlerFunc { return d.setLocale }},
	{"GET", "/api/v1/members/{id}/locale", func(d *deps) http.HandlerFunc { return d.getLocale }},
	{"GET", "/api/v1/members/{id}/balance", func(d *deps) http.HandlerFunc { return d.balance }},
	{"GET", "/api/v1/members/{id}/statement", func(d *deps) http.HandlerFunc { return d.statementJSON }},
	{"POST", "/api/v1/statements/verify", func(d *deps) http.HandlerFunc { return d.verifyStatement }},
	{"POST", "/api/v1/contributions/upload", func(d *deps) http.HandlerFunc { return d.uploadContributions }},
	{"GET", "/api/v1/recon/exceptions", func(d *deps) http.HandlerFunc { return d.exceptions }},
	{"POST", "/api/v1/recon/exceptions/{id}/resolve", func(d *deps) http.HandlerFunc { return d.resolveException }},
	{"POST", "/api/v1/onboarding", func(d *deps) http.HandlerFunc { return d.createOnboardingCase }},
	{"GET", "/api/v1/onboarding/{id}", func(d *deps) http.HandlerFunc { return d.getOnboardingCase }},
	{"POST", "/api/v1/onboarding/{id}/steps/{step}", func(d *deps) http.HandlerFunc { return d.completeOnboardingStep }},
	{"POST", "/api/v1/onboarding/{id}/approve", func(d *deps) http.HandlerFunc { return d.approveOnboarding }},
	{"POST", "/api/v1/onboarding/{id}/reject", func(d *deps) http.HandlerFunc { return d.rejectOnboarding }},
	{"GET", "/api/v1/audit/export", func(d *deps) http.HandlerFunc { return d.auditExport }},
}

// Routes returns the registered route table as "METHOD /path" strings for
// the OpenAPI contract test.
func Routes() []string {
	out := make([]string, 0, len(apiRoutes))
	for _, e := range apiRoutes {
		out = append(out, e.method+" "+e.pattern)
	}
	return out
}

// handler builds the tenant-scoped API surface behind the Tranche 10
// security chain: security headers -> per-IP rate limit -> body cap ->
// tenant enforcement -> optional bearer-token validation. Health probes are
// added by httpsrv.New outside this chain.
func (d *deps) handler() http.Handler {
	mux := http.NewServeMux()
	for _, e := range apiRoutes {
		mux.HandleFunc(e.method+" "+e.pattern, e.handler(d))
	}
	var h http.Handler = mux
	h = httpsrv.Tenant(h)
	h = httpsrv.OptionalAuth(d.authn)(h)
	h = httpsrv.MaxBody(10 << 20)(h)
	h = httpsrv.RateLimit(100, 200)(h)
	h = httpsrv.SecurityHeaders(h)
	return h
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
	// Bind the member's phone so channel services (e.g. the WhatsApp
	// webhook) can resolve the sender to this member and their locale.
	// NOTE: prefs.Store is in-memory per process — see internal/prefs.
	d.prefs.LinkPhone(in.Phone, p.ID)

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
	d.openMemberAccounts(tenant, p.ID)

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

// maxAliveRetries is the I-Am-Alive retry cap: at most 3 failed attempts
// per member per rolling 24-hour window. The counter is in-memory
// (aliveTracker); a durable deployment should move it next to the KYC
// records (Postgres) — documented follow-up.
const maxAliveRetries = 3

// aliveRetryWindow is the retry-cap window (24h).
const aliveRetryWindow = 24 * time.Hour

// aliveRecord is the member-facing liveness status returned by
// GET /members/{id}/alive.
type aliveRecord struct {
	LastVerifiedAt time.Time `json:"last_verified_at"`
	NextDue        time.Time `json:"next_due"`
	Status         string    `json:"status"` // VERIFIED
}

// aliveTracker keeps the member-facing liveness status (for members who are
// not in the retiree register) and the per-member failed-attempt counters
// for the retry cap. Thread-safe; in-memory only.
type aliveTracker struct {
	mu      sync.Mutex
	records map[string]aliveRecord
	fails   map[string][]time.Time
}

func newAliveTracker() *aliveTracker {
	return &aliveTracker{records: map[string]aliveRecord{}, fails: map[string][]time.Time{}}
}

// retriesLeft prunes failures older than the window and returns how many
// failed attempts the member may still make (0 = capped).
func (t *aliveTracker) retriesLeft(memberID string, now time.Time) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	kept := t.fails[memberID][:0]
	for _, at := range t.fails[memberID] {
		if now.Sub(at) < aliveRetryWindow {
			kept = append(kept, at)
		}
	}
	t.fails[memberID] = kept
	left := maxAliveRetries - len(kept)
	if left < 0 {
		left = 0
	}
	return left
}

// recordFailure notes one failed attempt.
func (t *aliveTracker) recordFailure(memberID string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fails[memberID] = append(t.fails[memberID], now)
}

// markVerified records a successful verification for the status endpoint.
func (t *aliveTracker) markVerified(memberID string, at, nextDue time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.records[memberID] = aliveRecord{LastVerifiedAt: at, NextDue: nextDue, Status: "VERIFIED"}
}

// status returns the member's last successful verification.
func (t *aliveTracker) status(memberID string) (aliveRecord, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rec, ok := t.records[memberID]
	return rec, ok
}

// submitAlive handles POST /api/v1/members/{id}/alive — the PWA I-Am-Alive
// check (FR-108). The selfie runs through the KYC provider facade with the
// IAM_ALIVE kind and regulator thresholds: score >= 0.80 verifies (200),
// 0.60-0.80 queues for manual review (202), < 0.60 fails (422) with the
// remaining retries. Explicit consent is required (NDPA, FR-106).
func (d *deps) submitAlive(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := d.members.Person(id); !ok {
		httpsrv.WriteError(w, http.StatusNotFound, "NOT_FOUND", errors.New("member not found"))
		return
	}
	var in struct {
		SelfieRef string `json:"selfie_ref"`
		Consent   bool   `json:"consent"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	if !in.Consent {
		httpsrv.WriteError(w, http.StatusBadRequest, "CONSENT_REQUIRED",
			errors.New("explicit consent is required for liveness verification"))
		return
	}
	if in.SelfieRef == "" {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", errors.New("selfie_ref is required"))
		return
	}
	tenant := httpsrv.TenantOf(r)
	now := time.Now().UTC()

	// Retry cap: 3 failed attempts per member per rolling 24h.
	if left := d.alive.retriesLeft(id, now); left <= 0 {
		httpsrv.WriteJSON(w, http.StatusTooManyRequests, map[string]any{
			"status": "FAILED", "retries_left": 0,
			"error": "retry cap reached; try again after 24 hours",
		})
		return
	}
	d.kyc.RecordConsent(tenant, id, "IAM_ALIVE", "alive-v1", true)
	chk, err := d.kyc.SubmitLiveness(tenant, id, kyc.KindIAmAlive, in.SelfieRef)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, kyc.ErrRetryCapExceeded) {
			status = http.StatusTooManyRequests
		}
		httpsrv.WriteError(w, status, "LIVENESS_FAILED", err)
		return
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "member.alive.submitted",
		EntityType: "liveness", EntityID: chk.ID,
		After: map[string]any{"member_id": id, "status": string(chk.Status), "score": chk.Score}})

	switch chk.Status {
	case kyc.StatusPassed:
		// Retiree register semantics (FR-108): reset the 12-month clock.
		// Members who are not (yet) retirees still get the KYC record and a
		// 12-month next-due date.
		nextDue := now.AddDate(1, 0, 0)
		if d.retirees.RecordLiveness(id, now) {
			if rt, ok := d.retirees.Get(id); ok {
				nextDue = rt.NextLivenessDue
			}
		}
		d.alive.markVerified(id, now, nextDue)
		httpsrv.WriteJSON(w, http.StatusOK, map[string]any{
			"status": "VERIFIED", "next_due": nextDue, "score": chk.Score,
		})
	case kyc.StatusManualReview:
		httpsrv.WriteJSON(w, http.StatusAccepted, map[string]any{
			"status": "MANUAL_REVIEW", "check_id": chk.ID, "score": chk.Score,
		})
	default: // StatusFailed
		d.alive.recordFailure(id, now)
		httpsrv.WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"status": "FAILED", "retries_left": d.alive.retriesLeft(id, now), "score": chk.Score,
		})
	}
}

// aliveStatus handles GET /api/v1/members/{id}/alive: the member's last
// successful I-Am-Alive verification and the next due date (404 when the
// member has never verified).
func (d *deps) aliveStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := d.members.Person(id); !ok {
		httpsrv.WriteError(w, http.StatusNotFound, "NOT_FOUND", errors.New("member not found"))
		return
	}
	rec, ok := d.alive.status(id)
	if !ok {
		httpsrv.WriteError(w, http.StatusNotFound, "NEVER_VERIFIED",
			errors.New("member has no successful liveness verification"))
		return
	}
	httpsrv.WriteJSON(w, http.StatusOK, rec)
}

// setLocale handles PUT /api/v1/members/{id}/locale: stores the member's
// preferred locale (REG-06) for app and channel (WhatsApp/USSD) replies.
func (d *deps) setLocale(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := d.members.Person(id); !ok {
		httpsrv.WriteError(w, http.StatusNotFound, "NOT_FOUND", errors.New("member not found"))
		return
	}
	var in struct {
		Locale string `json:"locale"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	loc := i18n.Locale(in.Locale)
	if !prefs.Supported(loc) {
		httpsrv.WriteError(w, http.StatusBadRequest, "UNSUPPORTED_LOCALE",
			errors.New("locale must be one of en, yo, ha, ig, pcm-NG"))
		return
	}
	d.prefs.Set(id, loc)
	d.audit.Append(audit.Record{TenantID: httpsrv.TenantOf(r), Actor: "member-svc", Action: "member.locale.set",
		EntityType: "person", EntityID: id, After: map[string]any{"locale": string(loc)}})
	httpsrv.WriteJSON(w, http.StatusOK, map[string]any{"member_id": id, "locale": string(loc)})
}

// getLocale handles GET /api/v1/members/{id}/locale (defaults to English).
func (d *deps) getLocale(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := d.members.Person(id); !ok {
		httpsrv.WriteError(w, http.StatusNotFound, "NOT_FOUND", errors.New("member not found"))
		return
	}
	httpsrv.WriteJSON(w, http.StatusOK, map[string]any{"member_id": id, "locale": string(d.prefs.Get(id))})
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

// resolveException applies a maker-checker resolution to an open exception
// (§6.2). The resolver identity comes from the X-Actor header.
func (d *deps) resolveException(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Resolution string `json:"resolution"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	switch in.Resolution {
	case "AUTO_MATCH", "RECLASSIFY", "REFUND":
	default:
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST",
			errors.New("resolution must be AUTO_MATCH, RECLASSIFY or REFUND"))
		return
	}
	id := r.PathValue("id")
	actor := strings.TrimSpace(r.Header.Get("X-Actor"))
	if actor == "" {
		actor = "ops-unknown"
	}
	if err := d.recon.Resolve(id, in.Resolution, actor); err != nil {
		status := http.StatusConflict
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		httpsrv.WriteError(w, status, "RESOLVE_FAILED", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	d.audit.Append(audit.Record{TenantID: tenant, Actor: actor, Action: "recon.exception.resolved",
		EntityType: "exception", EntityID: id, After: map[string]any{"resolution": in.Resolution}})
	httpsrv.WriteJSON(w, http.StatusOK, map[string]string{"id": id, "resolution": in.Resolution, "resolved_by": actor})
}

// statementJSON returns the member's statement payload with the
// HMAC verification code (FR-501); the closing balance is the live ledger
// balance for the member's RSA.
func (d *deps) statementJSON(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := d.members.Person(id)
	if !ok {
		httpsrv.WriteError(w, http.StatusNotFound, "NOT_FOUND", errors.New("member not found"))
		return
	}
	tenant := httpsrv.TenantOf(r)
	period := r.URL.Query().Get("period")
	if period == "" {
		period = time.Now().UTC().Format("2006-01")
	}
	closing := d.ledger.Balance(rsaAccount(id))
	payload := statement.Payload{
		ID: "stmt-" + id + "-" + period, TenantID: tenant, RSAID: id,
		MemberName: p.FullName, RSAPIN: id, Period: period,
		ClosingBalance: closing, GeneratedAt: time.Now().UTC(),
	}
	payload.VerifyCode = statement.Sign(d.stmtSecret, tenant, id, period, closing)
	d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "statement.generated",
		EntityType: "statement", EntityID: payload.ID})
	httpsrv.WriteJSON(w, http.StatusOK, payload)
}

// verifyStatement re-checks a statement verification code (QR round-trip,
// FR-501): callers present the RSA, period, closing balance and code.
func (d *deps) verifyStatement(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RSAID       string     `json:"rsa_id"`
		Period      string     `json:"period"`
		ClosingKobo money.Kobo `json:"closing_kobo"`
		Code        string     `json:"code"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	valid := statement.Verify(d.stmtSecret, httpsrv.TenantOf(r), in.RSAID, in.Period, in.ClosingKobo, in.Code)
	httpsrv.WriteJSON(w, http.StatusOK, map[string]bool{"valid": valid})
}

// auditExport returns the tenant's hash-chained audit slice under dual
// control (X-Approver-1 / X-Approver-2 must be two distinct staff
// identities) together with the chain integrity check (SC-02, §6.3).
func (d *deps) auditExport(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	if q := strings.TrimSpace(r.URL.Query().Get("tenant")); q != "" && q != tenant {
		httpsrv.WriteError(w, http.StatusForbidden, "TENANT_MISMATCH",
			errors.New("query tenant does not match X-Tenant-ID"))
		return
	}
	a1 := strings.TrimSpace(r.Header.Get("X-Approver-1"))
	a2 := strings.TrimSpace(r.Header.Get("X-Approver-2"))
	events, err := d.audit.Export(tenant, a1, a2)
	if err != nil {
		httpsrv.WriteError(w, http.StatusForbidden, "DUAL_CONTROL_REQUIRED", err)
		return
	}
	if events == nil {
		events = []audit.Event{}
	}
	httpsrv.WriteJSON(w, http.StatusOK, map[string]any{
		"tenant":      tenant,
		"count":       len(events),
		"events":      events,
		"chain_valid": d.audit.Verify() == nil,
		"approved_by": []string{a1, a2},
		"exported_at": time.Now().UTC(),
	})
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

// openMemberAccounts opens the tenant custody-cash asset account and the
// member's RSA liability account (idempotent; ErrAccountExist ignored).
// Shared by the direct create-member handler and the onboarding activation
// hook (FR-520).
func (d *deps) openMemberAccounts(tenant, memberID string) {
	_ = d.ledger.OpenAccount(ledger.Account{ID: "custody-cash", TenantID: tenant, Name: "Custody Cash", Type: ledger.Asset, Currency: "NGN"})
	_ = d.ledger.OpenAccount(ledger.Account{ID: rsaAccount(memberID), TenantID: tenant, Name: "RSA " + memberID, Type: ledger.Liability, Currency: "NGN"})
}

func rcptID(i int) string { return "rcpt-" + strconv.Itoa(i) }

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
