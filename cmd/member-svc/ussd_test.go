package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/munisp/pfaengine/internal/chatbot"
	"github.com/munisp/pfaengine/internal/i18n"
)

// ussdTestBundles returns an English bundle with a distinctive canned reply
// per intent so tests can prove the right intent was resolved.
func ussdTestBundles() map[i18n.Locale]i18n.Bundle {
	msgs := map[string]string{}
	for i, key := range ussdIntentKey {
		msgs[key] = "REPLY-" + chatbot.USSDCode(i)
	}
	return map[i18n.Locale]i18n.Bundle{
		i18n.English: {Locale: i18n.English, Messages: msgs},
	}
}

// stubUSSDResponder points the shared responder at the test bundles (the
// production loader would read I18N_DIR, which does not exist under the
// cmd/member-svc test working directory).
func stubUSSDResponder(t *testing.T) {
	t.Helper()
	ussdResponderOnce = sync.Once{}
	ussdResponder = chatbot.Responder{Bundles: ussdTestBundles()}
	ussdResponderOnce.Do(func() {}) // mark consumed so sharedUSSDResponder keeps it
	t.Cleanup(func() {
		ussdResponderOnce = sync.Once{}
		ussdResponder = chatbot.Responder{}
	})
}

// postUSSD issues an Africa's Talking-shaped callback: POST
// application/x-www-form-urlencoded with the exact case-sensitive field
// names sessionId, serviceCode, phoneNumber, text.
func postUSSD(t *testing.T, d *deps, sessionID, serviceCode, phone, text string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{}
	form.Set("sessionId", sessionID)
	form.Set("serviceCode", serviceCode)
	form.Set("phoneNumber", phone)
	form.Set("text", text)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ussd/callback", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	d.ussdCallback(rr, req)
	return rr
}

func TestUSSDCallbackFullMenuWalk(t *testing.T) {
	stubUSSDResponder(t)
	d := &deps{}

	// New session (empty text): CON + the full root menu.
	rr := postUSSD(t, d, "sess-1", "*384*123#", "+2348012345678", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("menu status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", ct)
	}
	body := rr.Body.String()
	if !strings.HasPrefix(body, "CON ") {
		t.Fatalf("new-session body = %q, want CON prefix", body)
	}
	for _, want := range []string{"1. Check RSA balance", "9. Mortgage equity", "14. Track a case"} {
		if !strings.Contains(body, want) {
			t.Errorf("menu missing %q", want)
		}
	}

	// Select intent 1: END with that intent's canned reply.
	rr = postUSSD(t, d, "sess-1", "*384*123#", "+2348012345678", "1")
	if got := rr.Body.String(); got != "END REPLY-1" {
		t.Fatalf("balance reply = %q, want %q", got, "END REPLY-1")
	}
}

func TestUSSDCallbackInvalidInputRecovery(t *testing.T) {
	stubUSSDResponder(t)
	d := &deps{}

	// Garbage input: CON error line + re-shown menu.
	rr := postUSSD(t, d, "sess-2", "*384*123#", "+2348012345678", "99")
	body := rr.Body.String()
	if !strings.HasPrefix(body, "CON Invalid selection") {
		t.Fatalf("invalid-input body = %q, want CON Invalid selection...", body)
	}
	if !strings.Contains(body, "1. Check RSA balance") {
		t.Fatal("error response must re-show the menu")
	}

	// AT accumulates input: after the error the member typed 2, so the
	// cumulative text is "99*2" and the last segment wins.
	rr = postUSSD(t, d, "sess-2", "*384*123#", "+2348012345678", "99*2")
	if got := rr.Body.String(); got != "END REPLY-2" {
		t.Fatalf("recovered reply = %q, want %q", got, "END REPLY-2")
	}
}

func TestUSSDCallbackDeepPathAndMultiDigitCodes(t *testing.T) {
	stubUSSDResponder(t)
	d := &deps{}

	// Deep path: the last segment of the cumulative chain is the current
	// screen's answer (the menu is flat, so it resolves directly).
	rr := postUSSD(t, d, "sess-3", "*384*123#", "+2348012345678", "1*3")
	if got := rr.Body.String(); got != "END REPLY-3" {
		t.Fatalf(`"1*3" reply = %q, want %q`, got, "END REPLY-3")
	}

	// Two-digit codes (10-14) must not be split digit-by-digit.
	rr = postUSSD(t, d, "sess-3", "*384*123#", "+2348012345678", "14")
	if got := rr.Body.String(); got != "END REPLY-14" {
		t.Fatalf(`"14" reply = %q, want %q`, got, "END REPLY-14")
	}
	rr = postUSSD(t, d, "sess-3", "*384*123#", "+2348012345678", "10")
	if got := rr.Body.String(); got != "END REPLY-10" {
		t.Fatalf(`"10" reply = %q, want %q`, got, "END REPLY-10")
	}
}

func TestUSSDCallbackAfricaTalkingFieldNames(t *testing.T) {
	stubUSSDResponder(t)
	d := &deps{}

	// Exact camelCase AT fields; if the handler misread `text` it would
	// treat this as a new session and return the CON menu instead.
	rr := postUSSD(t, d, "ATUid_abc123", "*384*123#", "+2348012345678", "5")
	if got := rr.Body.String(); got != "END REPLY-5" {
		t.Fatalf("transfer-status reply = %q, want %q", got, "END REPLY-5")
	}
	// Privacy: the MSISDN must never be echoed back.
	if strings.Contains(rr.Body.String(), "2348012345678") {
		t.Fatal("response must never echo the phone number")
	}
}

func TestUSSDRespondPure(t *testing.T) {
	r := chatbot.Responder{Bundles: ussdTestBundles()}

	if got := ussdRespond(r, ""); !strings.HasPrefix(got, "CON Welcome to PFAEngine") {
		t.Fatalf("empty text = %q, want CON menu", got)
	}
	if got := ussdRespond(r, "  7 "); got != "END REPLY-7" {
		t.Fatalf("whitespace-padded input = %q, want END REPLY-7", got)
	}
	if got := ussdRespond(r, "0"); !strings.HasPrefix(got, "CON Invalid selection") {
		t.Fatalf("unknown code = %q, want CON Invalid selection", got)
	}
	if got := ussdRespond(r, "abc"); !strings.HasPrefix(got, "CON Invalid selection") {
		t.Fatalf("non-numeric = %q, want CON Invalid selection", got)
	}

	// Fallback chain: no bundles at all -> bare i18n key, never a panic.
	bare := chatbot.Responder{}
	if got := ussdRespond(bare, "1"); got != "END chat.balance" {
		t.Fatalf("bundle-less reply = %q, want END chat.balance", got)
	}
}

func TestUSSDMenuMatchesChatbotUSSDCode(t *testing.T) {
	// Parity guard: every menu line's digit is chatbot.USSDCode's mapping.
	idx := ussdCodeIndex()
	if len(idx) != len(ussdMenu) {
		t.Fatalf("code index has %d entries for %d menu items (duplicate codes?)", len(idx), len(ussdMenu))
	}
	for _, e := range ussdMenu {
		code := chatbot.USSDCode(e.intent)
		if code == "" {
			t.Errorf("intent %d has no USSD code", e.intent)
			continue
		}
		if idx[code] != e.intent {
			t.Errorf("code %s resolves to %d, want %d", code, idx[code], e.intent)
		}
		if _, ok := ussdIntentKey[e.intent]; !ok {
			t.Errorf("intent %d has no i18n key mapping", e.intent)
		}
	}
}

func TestUSSDFallbackBundlesComplete(t *testing.T) {
	// The embedded English fallback must cover every menu intent so members
	// never hear a bare message key over USSD.
	b := ussdFallbackBundles()[i18n.English]
	for _, e := range ussdMenu {
		if b.Messages[ussdIntentKey[e.intent]] == "" {
			t.Errorf("fallback bundle missing %q", ussdIntentKey[e.intent])
		}
	}
	// And the fallback responder answers END replies, not bare keys.
	r := chatbot.Responder{Bundles: ussdFallbackBundles()}
	if got := ussdRespond(r, "11"); !strings.HasPrefix(got, "END Update your next-of-kin") {
		t.Fatalf("fallback reply = %q", got)
	}
}
