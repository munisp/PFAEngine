// Package chatbot implements BR-09: a deterministic, no-LLM intent
// engine for the member chatbot with localized canned replies and USSD
// menu parity. Classification is pure keyword matching — deterministic,
// offline, and audit-friendly.
package chatbot

import (
	"strings"

	"github.com/munisp/pfaengine/internal/i18n"
)

// Intent is a classified member request.
type Intent int

const (
	IntentBalance Intent = iota
	IntentLastContribution
	IntentStatement
	IntentIAmAlive
	IntentTransferStatus
	// Tranche 19: innovation intents (member-facing innovation engines
	// surface through chatbot + WhatsApp + USSD parity).
	IntentProjection // advisory: retirement projection
	IntentGrow       // escalation + roundup: increase savings
	IntentFundChoice // glidepath: fund I-IV election
	IntentMortgage   // 25% RSA mortgage equity (PRA s.89(2))
	IntentClaims     // claimsfast: retirement claims
	IntentNominee    // succession: beneficiary nominations
	IntentLearn      // literacy centre
	// Tranche 20: case management + CRM intents — members raise complaints
	// and track case status from WhatsApp/USSD/chat.
	IntentComplaint  // raise a complaint (opens a COMPLAINT ops case)
	IntentCaseStatus // track an existing case by id
	IntentUnknown
)

// intentKey maps intents to i18n message keys for canned replies.
var intentKey = map[Intent]string{
	IntentBalance:          "chat.balance",
	IntentLastContribution: "chat.last_contribution",
	IntentStatement:        "chat.statement",
	IntentIAmAlive:         "chat.i_am_alive",
	IntentTransferStatus:   "chat.transfer_status",
	IntentProjection:       "chat.projection",
	IntentGrow:             "chat.grow",
	IntentFundChoice:       "chat.fund_choice",
	IntentMortgage:         "chat.mortgage",
	IntentClaims:           "chat.claims",
	IntentNominee:          "chat.nominee",
	IntentLearn:            "chat.learn",
	IntentComplaint:        "chat.complaint",
	IntentCaseStatus:       "chat.case_status",
	IntentUnknown:          "chat.unknown",
}

// keywordRule binds an intent to its trigger keywords (English + Nigerian
// Pidgin). Matching is case-insensitive substring matching on the
// lowercased message.
type keywordRule struct {
	intent   Intent
	keywords []string
}

// rules are evaluated longest-keyword-first: when a message matches
// keywords for several intents, the longest matching keyword wins, so a
// specific phrase ("last contribution") beats a generic one
// ("contribution" would still tie-break in its own favour as it is longer
// than any balance keyword it overlaps). Ties in length resolve to the
// earlier rule below.
var rules = []keywordRule{
	{IntentBalance, []string{"dey my account", "how much", "balance", "wetin dey my rsa"}},
	{IntentLastContribution, []string{"last contribution", "contribution", "remittance", "dey pay"}},
	{IntentStatement, []string{"statement", "e-statement"}},
	{IntentIAmAlive, []string{"i am alive", "dey alive", "liveness", "alive"}},
	{IntentTransferStatus, []string{"transfer", "moving my rsa", "switch pfa"}},
	{IntentProjection, []string{"retirement projection", "how much i go get", "project my pension", "retire with", "projection"}},
	{IntentGrow, []string{"increase my contribution", "round up", "roundup", "grow my pension", "save more", "step up"}},
	{IntentFundChoice, []string{"change my fund", "fund choice", "fund ii", "fund i", "aggressive fund", "glidepath"}},
	{IntentMortgage, []string{"mortgage", "house loan", "equity contribution", "buy house", "25%"}},
	{IntentClaims, []string{"retirement claim", "claim my pension", "retire", "benefit claim", "withdraw my pension"}},
	{IntentNominee, []string{"next of kin", "nominee", "beneficiary", "who go inherit"}},
	{IntentLearn, []string{"learn about pension", "teach me", "financial literacy", "how pension work", "learn"}},
	{IntentComplaint, []string{"i want to complain", "make a complaint", "lodge a complaint", "i no happy", "this no fair", "wahala", "report a problem", "complaint"}},
	{IntentCaseStatus, []string{"my case status", "case status", "track my case", "my ticket", "complaint status", "wetin happen to my case"}},
}

// Classify returns the intent for msg using longest-keyword-first
// matching on the lowercased message. Returns IntentUnknown when no
// keyword matches.
func Classify(msg string) Intent {
	low := strings.ToLower(msg)
	best := IntentUnknown
	bestLen := 0
	for _, r := range rules {
		for _, kw := range r.keywords {
			if len(kw) > bestLen && strings.Contains(low, kw) {
				best, bestLen = r.intent, len(kw)
			}
		}
	}
	return best
}

// Responder replies in the member's locale with graceful fallback:
// requested locale → English → the bare message key (never panics).
type Responder struct {
	Bundles map[i18n.Locale]i18n.Bundle
}

// Reply returns the localized canned answer for the message's intent.
func (r Responder) Reply(loc i18n.Locale, msg string) string {
	key := intentKey[Classify(msg)]
	if b, ok := r.Bundles[loc]; ok {
		if m, ok := b.Messages[key]; ok && m != "" {
			return m
		}
	}
	if b, ok := r.Bundles[i18n.English]; ok {
		if m, ok := b.Messages[key]; ok && m != "" {
			return m
		}
	}
	return key
}

// USSDCode returns the USSD menu digit with the same semantics as the
// chatbot intent (USSD parity):
//
//	1 = balance, 2 = last contribution, 3 = statement,
//	4 = I-Am-Alive, 5 = transfer status; "" for unknown.
func USSDCode(i Intent) string {
	switch i {
	case IntentBalance:
		return "1"
	case IntentLastContribution:
		return "2"
	case IntentStatement:
		return "3"
	case IntentIAmAlive:
		return "4"
	case IntentTransferStatus:
		return "5"
	case IntentProjection:
		return "6"
	case IntentGrow:
		return "7"
	case IntentFundChoice:
		return "8"
	case IntentMortgage:
		return "9"
	case IntentClaims:
		return "10"
	case IntentNominee:
		return "11"
	case IntentLearn:
		return "12"
	case IntentComplaint:
		return "13"
	case IntentCaseStatus:
		return "14"
	}
	return ""
}
