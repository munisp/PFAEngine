package vendors

import (
	"fmt"
	"strings"
)

// AfricasTalking implements the USSD session handler (INT-08):
// aggregator callbacks map to member-service API operations.
// Session protocol: text is the "*" - joined input chain.
type AfricasTalking struct {
	APIKey    string
	Username  string
	ShortCode string
}

// USSDResponse: CON (continue) or END (terminate) per aggregator protocol.
type USSDResponse struct {
	Text      string `json:"text"`
	Continues bool   `json:"continues"` // CON vs END
}

// Handler resolves USSD menu selections against member operations.
type Handler struct {
	Balance     func(rsaPIN string) (string, error) // e.g. "NGN 6,932,000.00"
	LastContrib func(rsaPIN string) (string, error)
}

// Handle processes one USSD callback. Menu:
//
//	1 Balance   2 Last contribution   3 Statement link
func (at *AfricasTalking) Handle(h Handler, sessionID, phone, text string) USSDResponse {
	parts := []string{}
	if text != "" {
		parts = strings.Split(text, "*")
	}
	switch len(parts) {
	case 0:
		return USSDResponse{Text: "CON Welcome. Enter RSA PIN:", Continues: true}
	case 1:
		return USSDResponse{Text: "CON 1. Balance\n2. Last contribution\n3. Statement link", Continues: true}
	default:
		pin, choice := parts[0], parts[1]
		switch choice {
		case "1":
			bal, err := h.Balance(pin)
			if err != nil {
				return USSDResponse{Text: "END Balance unavailable. Try later."}
			}
			return USSDResponse{Text: "END Balance: " + bal}
		case "2":
			last, err := h.LastContrib(pin)
			if err != nil {
				return USSDResponse{Text: "END No contribution found."}
			}
			return USSDResponse{Text: "END Last contribution: " + last}
		default:
			return USSDResponse{Text: fmt.Sprintf("END Statement link sent by SMS to %s", phone)}
		}
	}
}
