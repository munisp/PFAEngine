// notification-service: templated SMS/email/push/WhatsApp with audit
// trail (§4, FR-503, INT-07).
package main

import (
	"net/http"

	"github.com/munisp/pfaengine/internal/appx"
	"github.com/munisp/pfaengine/internal/notify"
)

func main() {
	svc := notify.NewService(&notify.MockSender{})
	svc.RegisterTemplate(notify.Template{ID: "contrib-received", Channel: notify.SMS, Name: "Contribution received", Body: "Dear {{name}}, your RSA received NGN {{amount}} for {{period}}.", Version: 1})
	svc.RegisterTemplate(notify.Template{ID: "liveness-reminder", Channel: notify.WhatsApp, Name: "I Am Alive reminder", Body: "Dear {{name}}, your annual liveness verification is due. Open the app to verify.", Version: 1})

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
		rec, err := svc.Send(appx.TenantOf(r), in.TemplateID, in.To, in.Vars)
		if err != nil {
			appx.Err(w, 422, "SEND_FAILED", err)
			return
		}
		appx.JSON(w, 200, rec)
	})

	mux.HandleFunc("GET /v1/notifications/audit", func(w http.ResponseWriter, r *http.Request) {
		appx.JSON(w, 200, svc.AuditTrail(appx.TenantOf(r)))
	})

	appx.Serve("notification", appx.Env("PORT", "8089"), appx.TenantMiddleware(mux))
}
