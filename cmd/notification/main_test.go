package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/munisp/pfaengine/internal/chatbot"
	"github.com/munisp/pfaengine/internal/i18n"
	"github.com/munisp/pfaengine/internal/notify"
	"github.com/munisp/pfaengine/internal/tenancy"
	"github.com/munisp/pfaengine/internal/vendors"
)

const testAppSecret = "test-app-secret"

type sentLog struct{ to, body string }

func newTestDeps(t *testing.T) (*deps, *[]sentLog) {
	t.Helper()
	reg := tenancy.NewRegistry()
	if _, err := reg.Provision(tenancy.Tenant{ID: "default", Name: "platform"}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Provision(tenancy.Tenant{ID: "t-off", Name: "off",
		FeatureFlags: map[string]bool{"whatsapp": false}}); err != nil {
		t.Fatal(err)
	}
	var sent []sentLog
	d := &deps{
		svc:         notify.NewService(&notify.MockSender{}),
		wa:          &vendors.Client{AppSecret: testAppSecret, PhoneNumberID: "pn-1", AccessToken: "tok"},
		verifyToken: "verify-tok",
		tenants:     reg,
		locale:      i18n.Pidgin,
		responder: chatbot.Responder{Bundles: map[i18n.Locale]i18n.Bundle{
			i18n.Pidgin: {Locale: i18n.Pidgin, Messages: map[string]string{
				"chat.balance": "Your RSA balance dey your latest statement.",
				"chat.unknown": "Sorry, I no understand.",
			}},
		}},
		sendText: func(ctx context.Context, to, body string) (string, error) {
			sent = append(sent, sentLog{to, body})
			return "wamid-test-1", nil
		},
	}
	return d, &sent
}

func sign(body string) string {
	mac := hmac.New(sha256.New, []byte(testAppSecret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func inboundPayload(text string) string {
	return `{"object":"whatsapp_business_account","entry":[{"id":"e1","changes":[{"field":"messages","value":{` +
		`"metadata":{"phone_number_id":"pn-1"},` +
		`"messages":[{"from":"2348030000000","id":"wamid.1","timestamp":"1735689600","type":"text",` +
		`"text":{"body":` + quoteJSON(text) + `}}]}}]}]}`
}

// quoteJSON quotes s as a JSON string literal.
func quoteJSON(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestWebhookChallenge(t *testing.T) {
	d, _ := newTestDeps(t)
	h := d.handler()

	req := httptest.NewRequest(http.MethodGet,
		"/webhooks/whatsapp?hub.mode=subscribe&hub.verify_token=verify-tok&hub.challenge=1158201444", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "1158201444" {
		t.Fatalf("challenge: status=%d body=%q", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet,
		"/webhooks/whatsapp?hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=1158201444", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("bad token: status=%d, want 403", rr.Code)
	}
}

func TestWebhookInboundEndToEnd(t *testing.T) {
	d, sent := newTestDeps(t)
	h := d.handler()

	body := inboundPayload("how much dey my account")
	req := httptest.NewRequest(http.MethodPost, "/webhooks/whatsapp", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Status   string `json:"status"`
		Messages int    `json:"messages"`
		Replies  int    `json:"replies"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Messages != 1 || out.Replies != 1 {
		t.Fatalf("out=%+v", out)
	}
	if len(*sent) != 1 {
		t.Fatalf("SendText calls = %d, want 1", len(*sent))
	}
	s := (*sent)[0]
	if s.to != "2348030000000" || s.body != "Your RSA balance dey your latest statement." {
		t.Errorf("SendText(%q, %q)", s.to, s.body)
	}
}

func TestWebhookInboundBadSignature(t *testing.T) {
	d, sent := newTestDeps(t)
	h := d.handler()

	body := inboundPayload("balance")
	req := httptest.NewRequest(http.MethodPost, "/webhooks/whatsapp", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256="+strings.Repeat("0", 64))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", rr.Code)
	}
	if len(*sent) != 0 {
		t.Fatal("SendText must not be called on bad signature")
	}
}

func TestWebhookFeatureFlagGate(t *testing.T) {
	d, sent := newTestDeps(t)
	h := d.handler()

	body := inboundPayload("balance")
	req := httptest.NewRequest(http.MethodPost, "/webhooks/whatsapp", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign(body))
	req.Header.Set("X-Tenant-ID", "t-off") // whatsapp disabled
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", rr.Code)
	}
	if len(*sent) != 0 {
		t.Fatal("SendText must not be called when the flag is off")
	}

	// Unknown tenant defaults to enabled (opt-out model).
	req = httptest.NewRequest(http.MethodPost, "/webhooks/whatsapp", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign(body))
	req.Header.Set("X-Tenant-ID", "t-unknown")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unknown tenant: status=%d, want 200", rr.Code)
	}
}

func TestWebhookIgnoresStatusUpdates(t *testing.T) {
	d, sent := newTestDeps(t)
	h := d.handler()
	body := `{"object":"whatsapp_business_account","entry":[{"id":"e1","changes":[{"field":"messages","value":{` +
		`"statuses":[{"id":"wamid.1","status":"delivered","timestamp":"1735689601"}]}}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/whatsapp", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(*sent) != 0 {
		t.Fatal("status updates must not trigger replies")
	}
}

func TestExistingEndpointsStillWork(t *testing.T) {
	d, _ := newTestDeps(t)
	d.svc.RegisterTemplate(notify.Template{ID: "contrib-received", Channel: notify.SMS,
		Name: "Contribution received", Body: "Dear {{name}}, got {{amount}}.", Version: 1})
	h := d.handler()

	// Tenant middleware still enforced on API routes.
	req := httptest.NewRequest(http.MethodPost, "/v1/notifications/send",
		strings.NewReader(`{"template_id":"contrib-received","to":"2348030000000","vars":{"name":"Ada","amount":"NGN 144,000"}}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("no tenant: status=%d, want 400", rr.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/notifications/send",
		strings.NewReader(`{"template_id":"contrib-received","to":"2348030000000","vars":{"name":"Ada","amount":"NGN 144,000"}}`))
	req.Header.Set("X-Tenant-ID", "t1")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("send: status=%d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/notifications/audit", nil)
	req.Header.Set("X-Tenant-ID", "t1")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "contrib-received") {
		t.Fatalf("audit: status=%d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("healthz: status=%d", rr.Code)
	}
}
