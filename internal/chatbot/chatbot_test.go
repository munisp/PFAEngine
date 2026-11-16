package chatbot

import (
	"testing"

	"github.com/munisp/pfaengine/internal/i18n"
)

func TestClassifyMatrix(t *testing.T) {
	cases := []struct {
		msg  string
		want Intent
	}{
		{"What is my balance?", IntentBalance},
		{"BALANCE please", IntentBalance},
		{"how much dey my account", IntentBalance}, // pidgin
		{"wetin dey my RSA?", IntentBalance},       // pidgin
		{"when did my last contribution land?", IntentLastContribution},
		{"show my contribution history", IntentLastContribution},
		{"my employer remittance status", IntentLastContribution},
		{"dem don dey pay?", IntentLastContribution}, // pidgin
		{"send me my statement", IntentStatement},
		{"I need an E-Statement for 2025", IntentStatement},
		{"confirm I am alive", IntentIAmAlive},
		{"I dey alive o", IntentIAmAlive}, // pidgin
		{"liveness check", IntentIAmAlive},
		{"where is my transfer?", IntentTransferStatus},
		{"I want to switch PFA", IntentTransferStatus},
		{"hello there", IntentUnknown},
		{"", IntentUnknown},
	}
	for _, c := range cases {
		if got := Classify(c.msg); got != c.want {
			t.Errorf("Classify(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

func TestClassifyLongestKeywordFirst(t *testing.T) {
	// "last contribution" (17 chars) beats any shorter overlapping match.
	if got := Classify("my last contribution please"); got != IntentLastContribution {
		t.Fatalf("got %v", got)
	}
	// "i am alive" (10 chars) beats bare "alive" semantics-wise and is
	// still IntentIAmAlive — here we check the specific phrase wins over
	// "transfer" if both appear: "statement" (9) vs "transfer" (8).
	if got := Classify("transfer my statement"); got != IntentStatement {
		t.Fatalf("got %v", got)
	}
}

func TestUSSDMapping(t *testing.T) {
	cases := []struct {
		i    Intent
		want string
	}{
		{IntentBalance, "1"},
		{IntentLastContribution, "2"},
		{IntentStatement, "3"},
		{IntentIAmAlive, "4"},
		{IntentTransferStatus, "5"},
		{IntentUnknown, ""},
	}
	for _, c := range cases {
		if got := USSDCode(c.i); got != c.want {
			t.Errorf("USSDCode(%v) = %q, want %q", c.i, got, c.want)
		}
	}
}

func TestResponderReply(t *testing.T) {
	r := Responder{Bundles: map[i18n.Locale]i18n.Bundle{
		i18n.English: {Locale: i18n.English, Messages: map[string]string{
			"chat.balance": "Your RSA balance is available on your statement.",
			"chat.unknown": "Sorry, I did not understand. Dial *789# for the menu.",
		}},
		i18n.Pidgin: {Locale: i18n.Pidgin, Messages: map[string]string{
			"chat.balance": "Check your statement to see the money wey dey your RSA.",
		}},
	}}

	// Localized pidgin hit.
	if got := r.Reply(i18n.Pidgin, "how much dey my account"); got != "Check your statement to see the money wey dey your RSA." {
		t.Fatalf("pidgin reply: %q", got)
	}
	// Pidgin requested, key missing → English fallback.
	got := r.Reply(i18n.Pidgin, "hello")
	if got != "Sorry, I did not understand. Dial *789# for the menu." {
		t.Fatalf("english fallback: %q", got)
	}
	// No bundle at all → bare key fallback (never panics).
	if got := (Responder{}).Reply(i18n.Yoruba, "balance"); got != "chat.balance" {
		t.Fatalf("key fallback: %q", got)
	}
}
