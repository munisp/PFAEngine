package chatbot

import (
	"github.com/munisp/pfaengine/internal/i18n"
	"github.com/munisp/pfaengine/internal/vendors"
)

// HandleWhatsApp bridges an inbound WhatsApp message (INT-07) to a
// localized canned reply: the text is classified by the same deterministic
// keyword engine — English and Nigerian Pidgin keywords — that powers the
// chatbot and USSD menu parity, and the intent's canned reply is returned
// in the member's locale. Non-text messages (images, audio, status probes)
// carry an empty Text and classify to IntentUnknown, yielding the
// help/menu reply.
func (r Responder) HandleWhatsApp(loc i18n.Locale, msg vendors.InboundMessage) string {
	return r.Reply(loc, msg.Text)
}
