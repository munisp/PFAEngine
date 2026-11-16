package chatbot

import (
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/i18n"
	"github.com/munisp/pfaengine/internal/vendors"
)

func TestHandleWhatsApp(t *testing.T) {
	r := Responder{Bundles: map[i18n.Locale]i18n.Bundle{
		i18n.English: {Locale: i18n.English, Messages: map[string]string{
			"chat.balance": "Your RSA balance is on your latest statement.",
			"chat.unknown": "Sorry, I did not understand.",
		}},
		i18n.Pidgin: {Locale: i18n.Pidgin, Messages: map[string]string{
			"chat.balance": "Your RSA balance dey your latest statement.",
		}},
	}}
	msg := func(text string) vendors.InboundMessage {
		return vendors.InboundMessage{From: "2348030000000", Text: text, Type: "text", Timestamp: time.Unix(1735689600, 0)}
	}
	cases := []struct {
		name string
		loc  i18n.Locale
		msg  vendors.InboundMessage
		want string
	}{
		{"pidgin balance", i18n.Pidgin, msg("how much dey my account"), "Your RSA balance dey your latest statement."},
		{"english balance", i18n.English, msg("what is my balance?"), "Your RSA balance is on your latest statement."},
		{"pidgin unknown falls back to english", i18n.Pidgin, msg("hello there"), "Sorry, I did not understand."},
		{"non-text message classifies unknown", i18n.English,
			vendors.InboundMessage{From: "2348030000000", Type: "image"}, "Sorry, I did not understand."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.HandleWhatsApp(tc.loc, tc.msg); got != tc.want {
				t.Errorf("HandleWhatsApp = %q, want %q", got, tc.want)
			}
		})
	}
}
