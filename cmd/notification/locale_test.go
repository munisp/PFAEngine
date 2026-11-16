package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/munisp/pfaengine/internal/chatbot"
	"github.com/munisp/pfaengine/internal/i18n"
	"github.com/munisp/pfaengine/internal/notify"
	"github.com/munisp/pfaengine/internal/prefs"
	"github.com/munisp/pfaengine/internal/tenancy"
	"github.com/munisp/pfaengine/internal/vendors"
)

// newLocaleTestDeps builds deps with both English and Pidgin bundles and a
// prefs-backed resolveLocale, so webhook replies follow the member's locale.
func newLocaleTestDeps(t *testing.T, memberPrefs *prefs.Store) (*deps, *[]sentLog) {
	t.Helper()
	reg := tenancy.NewRegistry()
	if _, err := reg.Provision(tenancy.Tenant{ID: "default", Name: "platform"}); err != nil {
		t.Fatal(err)
	}
	var sent []sentLog
	d := &deps{
		svc:         notify.NewService(&notify.MockSender{}),
		wa:          &vendors.Client{AppSecret: testAppSecret, PhoneNumberID: "pn-1", AccessToken: "tok"},
		verifyToken: "verify-tok",
		tenants:     reg,
		locale:      i18n.English,
		responder: chatbot.Responder{Bundles: map[i18n.Locale]i18n.Bundle{
			i18n.English: {Locale: i18n.English, Messages: map[string]string{
				"chat.balance": "Your RSA balance is on your latest statement.",
			}},
			i18n.Pidgin: {Locale: i18n.Pidgin, Messages: map[string]string{
				"chat.balance": "Your RSA balance dey your latest statement.",
			}},
		}},
		resolveLocale: memberPrefs.LocaleForPhone,
		sendText: func(ctx context.Context, to, body string) (string, error) {
			sent = append(sent, sentLog{to, body})
			return "wamid-test-1", nil
		},
	}
	return d, &sent
}

func postWebhook(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/whatsapp", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestWebhookRepliesInMemberLocale: a member who set pcm-NG gets Pidgin
// replies when they message from their linked phone.
func TestWebhookRepliesInMemberLocale(t *testing.T) {
	memberPrefs := prefs.NewStore()
	memberPrefs.LinkPhone("+2348030000000", "prs-1") // member linked at onboarding
	memberPrefs.Set("prs-1", i18n.Pidgin)            // member chose Pidgin

	d, sent := newLocaleTestDeps(t, memberPrefs)
	h := d.handler()

	rr := postWebhook(t, h, inboundPayload("how much dey my account"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(*sent) != 1 {
		t.Fatalf("SendText calls = %d, want 1", len(*sent))
	}
	if got := (*sent)[0].body; got != "Your RSA balance dey your latest statement." {
		t.Fatalf("reply = %q, want Pidgin bundle text", got)
	}
}

// TestWebhookUnknownSenderFallsBackToEnglish: numbers with no member
// mapping get the default English replies.
func TestWebhookUnknownSenderFallsBackToEnglish(t *testing.T) {
	memberPrefs := prefs.NewStore() // empty: unknown sender
	d, sent := newLocaleTestDeps(t, memberPrefs)
	h := d.handler()

	rr := postWebhook(t, h, inboundPayload("balance please"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(*sent) != 1 {
		t.Fatalf("SendText calls = %d, want 1", len(*sent))
	}
	if got := (*sent)[0].body; got != "Your RSA balance is on your latest statement." {
		t.Fatalf("reply = %q, want English bundle text", got)
	}
}

// TestWebhookEnglishMemberAfterPidginMember ensures the resolution is per
// sender, not sticky: an English-locale member messaging after a Pidgin one
// still gets English.
func TestWebhookLocaleIsPerSender(t *testing.T) {
	memberPrefs := prefs.NewStore()
	memberPrefs.LinkPhone("+2348030000000", "prs-1")
	memberPrefs.Set("prs-1", i18n.Pidgin)
	memberPrefs.LinkPhone("+2348050000000", "prs-2") // default English

	d, sent := newLocaleTestDeps(t, memberPrefs)
	h := d.handler()

	if rr := postWebhook(t, h, inboundPayload("balance")); rr.Code != http.StatusOK {
		t.Fatalf("first: status=%d", rr.Code)
	}
	// Second message from the other member (rebind inbound sender).
	body := strings.Replace(inboundPayload("balance"), "2348030000000", "2348050000000", 1)
	if rr := postWebhook(t, h, body); rr.Code != http.StatusOK {
		t.Fatalf("second: status=%d", rr.Code)
	}
	if len(*sent) != 2 {
		t.Fatalf("SendText calls = %d, want 2", len(*sent))
	}
	if got := (*sent)[0].body; !strings.Contains(got, "dey your latest statement") {
		t.Fatalf("first reply = %q, want Pidgin", got)
	}
	if got := (*sent)[1].body; !strings.Contains(got, "is on your latest statement") {
		t.Fatalf("second reply = %q, want English", got)
	}
}
