package main

// Africa's Talking USSD callback (INT-08, BR-09 USSD parity): the gateway
// POSTs application/x-www-form-urlencoded with the case-sensitive fields
// sessionId, serviceCode, phoneNumber and text, where text is the cumulative
// "*"-joined input chain for the session (e.g. "1*3"). The response must be
// text/plain and start with "CON " (keep the session open, show the menu)
// or "END " (terminate, show the final reply).
//
// The handler is STATELESS by design: the chatbot intent surface is a flat,
// single-level menu (chatbot.USSDCode maps intents to digits 1-14 and every
// selection is terminal — it answers with the intent's canned reply), so no
// per-session menu state is required. The last segment of the cumulative
// text is always the answer to the screen currently on display, which also
// makes invalid-input recovery work naturally ("99*2" = the member typed 99,
// got the error+menu, then typed 2). Stateless means no in-memory session
// map, so there is nothing to evict and no unbounded-growth vector — the
// shared per-IP rate limiter (httpsrv.RateLimit) fronts this route like
// every other.
//
// Replies come from the deterministic chatbot engine's canned strings
// (internal/chatbot + i18n bundles), keeping USSD in lock-step parity with
// the chatbot and WhatsApp surfaces. Replies default to English; Pidgin and
// the other locales are loaded (I18N_DIR) and the i18n fallback chain
// applies, but USSD has no per-member locale lookup because member.Registry
// has no phone-number index (only RSAByPIN) — member-by-phone personalisation
// is therefore deliberately skipped (see docs/USSD.md).
//
// Privacy: the MSISDN (phoneNumber) is parsed for protocol fidelity only and
// is never logged or persisted by this handler.

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/munisp/pfaengine/internal/chatbot"
	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/i18n"
)

// ussdMenuEntry is one root-menu line: the digit comes from
// chatbot.USSDCode so the USSD menu can never drift from the chatbot intent
// surface.
type ussdMenuEntry struct {
	intent chatbot.Intent
	label  string
}

// ussdMenu is the root menu in display order (digits 1-14).
var ussdMenu = []ussdMenuEntry{
	{chatbot.IntentBalance, "Check RSA balance"},
	{chatbot.IntentLastContribution, "Last contribution"},
	{chatbot.IntentStatement, "Get statement"},
	{chatbot.IntentIAmAlive, "I-Am-Alive confirmation"},
	{chatbot.IntentTransferStatus, "RSA transfer status"},
	{chatbot.IntentProjection, "Retirement projection"},
	{chatbot.IntentGrow, "Grow my savings"},
	{chatbot.IntentFundChoice, "Change fund (I-IV)"},
	{chatbot.IntentMortgage, "Mortgage equity (25% RSA)"},
	{chatbot.IntentClaims, "Retirement claims"},
	{chatbot.IntentNominee, "Next-of-kin / nominees"},
	{chatbot.IntentLearn, "Learn about pensions"},
	{chatbot.IntentComplaint, "Raise a complaint"},
	{chatbot.IntentCaseStatus, "Track a case"},
}

// ussdIntentKey mirrors chatbot's intent→i18n-key mapping (unexported there)
// so a menu digit can be answered with the same canned reply the chatbot
// would give, without re-classifying a synthetic message.
var ussdIntentKey = map[chatbot.Intent]string{
	chatbot.IntentBalance:          "chat.balance",
	chatbot.IntentLastContribution: "chat.last_contribution",
	chatbot.IntentStatement:        "chat.statement",
	chatbot.IntentIAmAlive:         "chat.i_am_alive",
	chatbot.IntentTransferStatus:   "chat.transfer_status",
	chatbot.IntentProjection:       "chat.projection",
	chatbot.IntentGrow:             "chat.grow",
	chatbot.IntentFundChoice:       "chat.fund_choice",
	chatbot.IntentMortgage:         "chat.mortgage",
	chatbot.IntentClaims:           "chat.claims",
	chatbot.IntentNominee:          "chat.nominee",
	chatbot.IntentLearn:            "chat.learn",
	chatbot.IntentComplaint:        "chat.complaint",
	chatbot.IntentCaseStatus:       "chat.case_status",
}

// ussdMenuText renders the root menu (English labels; the canned replies are
// localized, the menu chrome is intentionally simple per BR-09 parity).
func ussdMenuText() string {
	var b strings.Builder
	b.WriteString("Welcome to PFAEngine. Reply with a number:")
	for _, e := range ussdMenu {
		fmt.Fprintf(&b, "\n%s. %s", chatbot.USSDCode(e.intent), e.label)
	}
	return b.String()
}

// ussdCodeIndex reverses chatbot.USSDCode for the menu intents:
// menu digit -> intent.
func ussdCodeIndex() map[string]chatbot.Intent {
	m := make(map[string]chatbot.Intent, len(ussdMenu))
	for _, e := range ussdMenu {
		m[chatbot.USSDCode(e.intent)] = e.intent
	}
	return m
}

// ussdReply resolves an intent's canned reply with the same fallback chain
// as chatbot.Responder: requested locale -> English -> bare key.
func ussdReply(r chatbot.Responder, loc i18n.Locale, i chatbot.Intent) string {
	key := ussdIntentKey[i]
	if b, ok := r.Bundles[loc]; ok {
		if m := b.Messages[key]; m != "" {
			return m
		}
	}
	if b, ok := r.Bundles[i18n.English]; ok {
		if m := b.Messages[key]; m != "" {
			return m
		}
	}
	return key
}

// ussdRespond is the stateless core of the callback: given the cumulative
// text chain it returns the exact text/plain body ("CON ..."/"END ...").
func ussdRespond(r chatbot.Responder, text string) string {
	text = strings.TrimSpace(text)
	if text == "" { // new session: show the root menu
		return "CON " + ussdMenuText()
	}
	// The last "*"-separated segment is the answer to the screen currently
	// on display; earlier segments are the path already taken. Because the
	// menu is flat and every selection is terminal, the first valid
	// selection ends the session.
	parts := strings.Split(text, "*")
	choice := strings.TrimSpace(parts[len(parts)-1])
	if intent, ok := ussdCodeIndex()[choice]; ok {
		return "END " + ussdReply(r, i18n.English, intent)
	}
	return "CON Invalid selection, please try again.\n" + ussdMenuText()
}

// ussdFallbackBundles is the safety net when the i18n directory is not
// available to the member-svc process (bare message keys would otherwise be
// read out to members over USSD). It mirrors the en.json chat.* strings;
// i18n/en.json remains the source of truth whenever it loads.
func ussdFallbackBundles() map[i18n.Locale]i18n.Bundle {
	return map[i18n.Locale]i18n.Bundle{
		i18n.English: {Locale: i18n.English, Messages: map[string]string{
			"chat.balance":           "Your RSA balance is on your latest statement. Dial *789# and press 1 to hear it.",
			"chat.last_contribution": "Your last contribution details are on your statement. Dial *789# and press 2.",
			"chat.statement":         "Your statement is ready in the app or via email. Dial *789# and press 3.",
			"chat.i_am_alive":        "To confirm you are alive, open the app and complete the liveness check, or dial *789# and press 4.",
			"chat.transfer_status":   "RSA transfers complete within 365 days of your request. Dial *789# and press 5 for status.",
			"chat.projection":        "See your retirement projection in the app (Grow tab) or dial *789# and press 6.",
			"chat.grow":              "Increase your savings with auto-escalation or round-ups in the app (Grow tab), or dial *789# and press 7.",
			"chat.fund_choice":       "Change your fund (I-IV) in the app under Funds, or dial *789# and press 8.",
			"chat.mortgage":          "You can use up to 25% of your RSA as mortgage equity. Apply in the app (Home tab) or dial *789# and press 9.",
			"chat.claims":            "Start your retirement claim in the app (Claims tab) — fast-track completes in days. Or dial *789# and press 10.",
			"chat.nominee":           "Update your next-of-kin nominations in the app (Family tab) or dial *789# and press 11.",
			"chat.learn":             "Learn about pensions in your language in the app (Learn tab) or dial *789# and press 12.",
			"chat.complaint":         "Sorry about the trouble. Tell me what happened in one message and I will open a complaint case for you right away — you will get a case number to track it. Or dial *789# and press 13.",
			"chat.case_status":       "Send your case number (e.g. cs-000123) and I will tell you the status. You can also see all your cases in the app under Support, or dial *789# and press 14.",
		}},
	}
}

var (
	ussdResponderOnce sync.Once
	ussdResponder     chatbot.Responder
)

// sharedUSSDResponder loads the i18n bundles once (I18N_DIR, default "i18n"
// — the same convention as cmd/notification) and falls back to the embedded
// English set when the directory is unavailable.
func sharedUSSDResponder() chatbot.Responder {
	ussdResponderOnce.Do(func() {
		bundles, err := i18n.LoadDir(httpsrv.Getenv("I18N_DIR", "i18n"))
		if err != nil || len(bundles) == 0 {
			bundles = ussdFallbackBundles()
		}
		ussdResponder = chatbot.Responder{Bundles: bundles}
	})
	return ussdResponder
}

// ussdCallback handles POST /api/v1/ussd/callback from the Africa's Talking
// USSD gateway. It replies 200 text/plain with a "CON "/"END " body in all
// cases — AT terminates sessions on non-200 or malformed responses, so even
// a malformed form gets a graceful END rather than an error envelope.
func (d *deps) ussdCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "END Service temporarily unavailable. Please try again.")
		return
	}
	// AT field names are case-sensitive: sessionId, serviceCode,
	// phoneNumber, text. sessionId/serviceCode are protocol metadata the
	// stateless design does not need; phoneNumber is intentionally unused
	// (no member-by-phone index; never logged — NDPA minimisation).
	text := r.PostForm.Get("text")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, ussdRespond(sharedUSSDResponder(), text))
}
