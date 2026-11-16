// notification-service: templated SMS/email/push/WhatsApp with audit
// trail (§4, FR-503, INT-07) and the Meta WhatsApp Cloud API webhook:
// GET verifies the subscription challenge, POST receives inbound member
// messages (signature-verified) and replies through the chatbot intent
// engine. The webhook is gated by the per-tenant "whatsapp" feature flag
// (opt-out, NFR-20).
package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/munisp/pfaengine/internal/appx"
	"github.com/munisp/pfaengine/internal/chatbot"
	"github.com/munisp/pfaengine/internal/i18n"
	"github.com/munisp/pfaengine/internal/notify"
	"github.com/munisp/pfaengine/internal/tenancy"
	"github.com/munisp/pfaengine/internal/vendors"
)

// deps bundles the collaborators the handlers operate on, so the mux is
// testable without env vars or network access.
type deps struct {
	svc         *notify.Service
	wa          *vendors.Client
	verifyToken string
	tenants     *tenancy.Registry
	responder   chatbot.Responder
	locale      i18n.Locale
	// sendText delivers a chatbot reply; injectable for tests. When nil the
	// reply is computed but not sent (WhatsApp send not configured).
	sendText func(ctx context.Context, to, body string) (string, error)
}

func main() {
	var sender notify.Sender = &notify.MockSender{}
	if os.Getenv("TERMII_API_KEY") != "" { // INT-07
		sender = &vendors.Termii{
			APIKey: os.Getenv("TERMII_API_KEY"), SenderID: appx.Env("TERMII_SENDER_ID", "PFAEngine"),
			BaseURL: appx.Env("TERMII_BASE_URL", "https://v3.api.termii.com"),
		}
	}
	svc := notify.NewService(sender)
	svc.RegisterTemplate(notify.Template{ID: "contrib-received", Channel: notify.SMS, Name: "Contribution received", Body: "Dear {{name}}, your RSA received NGN {{amount}} for {{period}}.", Version: 1})
	svc.RegisterTemplate(notify.Template{ID: "liveness-reminder", Channel: notify.WhatsApp, Name: "liveness_reminder", Body: "Dear {{name}}, your annual liveness verification is due. Open the app to verify.", Version: 1})

	wa := &vendors.Client{
		PhoneNumberID: os.Getenv("WHATSAPP_PHONE_NUMBER_ID"),
		AccessToken:   os.Getenv("WHATSAPP_ACCESS_TOKEN"),
		AppSecret:     os.Getenv("WHATSAPP_APP_SECRET"),
		BaseURL:       appx.Env("WHATSAPP_BASE_URL", "https://graph.facebook.com/v21.0"),
	}
	d := &deps{
		svc:         svc,
		wa:          wa,
		verifyToken: os.Getenv("WHATSAPP_VERIFY_TOKEN"),
		tenants:     tenancy.NewRegistry(),
		locale:      i18n.English,
	}
	if bundles, err := i18n.LoadDir(appx.Env("I18N_DIR", "i18n")); err != nil {
		log.Printf("[notification] i18n bundles unavailable (%v); replies fall back to message keys", err)
	} else {
		d.responder = chatbot.Responder{Bundles: bundles}
	}
	if wa.AccessToken != "" {
		d.sendText = wa.SendText
		svc.SendWhatsApp = func(ctx context.Context, to, template string, params []string) error {
			_, err := wa.SendTemplate(ctx, to, template, "en", params)
			return err
		}
	}
	// The platform tenant: webhook callbacks carry no tenant header, so the
	// "default" tenant's flags govern the WhatsApp channel unless a caller
	// pins X-Tenant-ID.
	if _, err := d.tenants.Provision(tenancy.Tenant{ID: "default", Name: "PFAEngine platform"}); err != nil {
		log.Fatalf("[notification] provision default tenant: %v", err)
	}

	appx.Serve("notification", appx.Env("PORT", "8089"), d.handler())
}

// handler routes Meta webhook callbacks and health probes around the
// tenant middleware (they carry no X-Tenant-ID) while every API endpoint
// stays tenant-scoped.
func (d *deps) handler() http.Handler {
	mux := http.NewServeMux()
	appx.Health(mux, "notification")

	mux.HandleFunc("POST /v1/notifications/send", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			TemplateID string            `json:"template_id"`
			To         string            `json:"to"`
			Vars       map[string]string `json:"vars"`
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		rec, err := d.svc.Send(appx.TenantOf(r), in.TemplateID, in.To, in.Vars)
		if err != nil {
			appx.Err(w, 422, "SEND_FAILED", err)
			return
		}
		appx.JSON(w, 200, rec)
	})

	mux.HandleFunc("GET /v1/notifications/audit", func(w http.ResponseWriter, r *http.Request) {
		appx.JSON(w, 200, d.svc.AuditTrail(appx.TenantOf(r)))
	})

	mux.HandleFunc("GET /webhooks/whatsapp", d.whatsappChallenge)
	mux.HandleFunc("POST /webhooks/whatsapp", d.whatsappInbound)

	api := appx.TenantMiddleware(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/webhooks/") ||
			r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			mux.ServeHTTP(w, r)
			return
		}
		api.ServeHTTP(w, r)
	})
}

// whatsappEnabled gates the webhook on the tenant's "whatsapp" feature flag
// (opt-out: absent flag means enabled). The tenant comes from X-Tenant-ID
// when present, else the platform "default" tenant.
func (d *deps) whatsappEnabled(r *http.Request) bool {
	tid := r.Header.Get("X-Tenant-ID")
	if tid == "" {
		tid = "default"
	}
	if d.tenants == nil {
		return true
	}
	if t, ok := d.tenants.Get(tid); ok {
		return t.FeatureEnabled("whatsapp")
	}
	return true
}

// whatsappChallenge answers Meta's subscription handshake:
// GET ?hub.mode=subscribe&hub.verify_token=...&hub.challenge=...
func (d *deps) whatsappChallenge(w http.ResponseWriter, r *http.Request) {
	if !d.whatsappEnabled(r) {
		appx.Err(w, http.StatusForbidden, "FEATURE_DISABLED", errors.New("whatsapp channel disabled for tenant"))
		return
	}
	challenge, ok := d.wa.VerifyChallenge(r.URL.Query(), d.verifyToken)
	if !ok {
		appx.Err(w, http.StatusForbidden, "CHALLENGE_FAILED", errors.New("webhook verification failed"))
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(challenge))
}

// whatsappInbound receives Cloud API webhook deliveries: the
// X-Hub-Signature-256 HMAC is verified against the app secret (401 on
// mismatch), then each inbound text message is classified by the chatbot
// and answered via SendText inside the 24-hour service window.
func (d *deps) whatsappInbound(w http.ResponseWriter, r *http.Request) {
	if !d.whatsappEnabled(r) {
		appx.Err(w, http.StatusForbidden, "FEATURE_DISABLED", errors.New("whatsapp channel disabled for tenant"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		appx.Err(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	if !d.wa.VerifyWebhookSignature(body, r.Header.Get("X-Hub-Signature-256")) {
		appx.Err(w, http.StatusUnauthorized, "BAD_SIGNATURE", errors.New("invalid X-Hub-Signature-256"))
		return
	}
	msgs, err := d.wa.ParseInbound(body)
	if err != nil {
		appx.Err(w, http.StatusBadRequest, "BAD_PAYLOAD", err)
		return
	}
	replies := 0
	for _, m := range msgs {
		if m.Type != "text" {
			continue
		}
		reply := d.responder.HandleWhatsApp(d.locale, m)
		if d.sendText != nil {
			if _, err := d.sendText(r.Context(), m.From, reply); err != nil {
				appx.Err(w, http.StatusBadGateway, "WHATSAPP_SEND_FAILED", err)
				return
			}
		}
		replies++
	}
	appx.JSON(w, http.StatusOK, map[string]any{"status": "ok", "messages": len(msgs), "replies": replies})
}
