package vendors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AfricasTalking implements the USSD session handler (INT-08) and the SMS
// sender: aggregator callbacks map to member-service API operations.
// Session protocol: text is the "*" - joined input chain.
type AfricasTalking struct {
	APIKey    string
	Username  string
	ShortCode string
	BaseURL   string // https://api.africastalking.com (sandbox: api.sandbox.africastalking.com)
	HTTP      *http.Client
}

// SendSMS posts a message to the Africa's Talking messaging endpoint:
// application/x-www-form-urlencoded username/to/message body with the
// apiKey header credential (aggregator auth scheme).
func (at *AfricasTalking) SendSMS(ctx context.Context, to, message string) (string, error) {
	hc := at.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	form := url.Values{}
	form.Set("username", at.Username)
	form.Set("to", to)
	form.Set("message", message)
	req, err := http.NewRequestWithContext(ctx, "POST", at.BaseURL+"/version1/messaging", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("apiKey", at.APIKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("africastalking: %s: %s", resp.Status, string(raw))
	}
	var out struct {
		SMSMessageData struct {
			Recipients []struct {
				MessageID string `json:"messageId"`
				Status    string `json:"status"`
			} `json:"Recipients"`
		} `json:"SMSMessageData"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("africastalking: bad response: %w", err)
	}
	if len(out.SMSMessageData.Recipients) == 0 {
		return "", fmt.Errorf("africastalking: no recipients in response: %s", string(raw))
	}
	return out.SMSMessageData.Recipients[0].MessageID, nil
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
