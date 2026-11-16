package vendors

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is the Meta WhatsApp Cloud API adapter (INT-07): template/text
// sends over the Graph API plus webhook verification and inbound parsing
// for the member chatbot channel.
type Client struct {
	PhoneNumberID string
	AccessToken   string
	AppSecret     string // signs webhook deliveries (X-Hub-Signature-256)
	BaseURL       string // https://graph.facebook.com/v21.0
	HTTP          *http.Client
}

// InboundMessage is one message extracted from a Cloud API webhook payload.
type InboundMessage struct {
	From      string    `json:"from"` // sender E.164 without '+'
	Text      string    `json:"text"` // body for type=text; empty otherwise
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"` // text | image | audio | ...
}

type waText struct {
	Body string `json:"body"`
}

type waParameter struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type waComponent struct {
	Type       string        `json:"type"`
	Parameters []waParameter `json:"parameters,omitempty"`
}

type waTemplate struct {
	Name       string        `json:"name"`
	Language   waLanguage    `json:"language"`
	Components []waComponent `json:"components,omitempty"`
}

type waLanguage struct {
	Code string `json:"code"`
}

type waSendReq struct {
	MessagingProduct string      `json:"messaging_product"`
	RecipientType    string      `json:"recipient_type,omitempty"`
	To               string      `json:"to"`
	Type             string      `json:"type"`
	Template         *waTemplate `json:"template,omitempty"`
	Text             *waText     `json:"text,omitempty"`
}

type waSendResp struct {
	Messages []struct {
		ID string `json:"id"`
	} `json:"messages"`
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimSuffix(c.BaseURL, "/")
	}
	return "https://graph.facebook.com/v21.0"
}

// SendTemplate delivers a pre-approved WhatsApp template (Meta requires
// templates for business-initiated messages). params become the ordered
// body-component {{1}}..{{n}} substitutions.
func (c *Client) SendTemplate(ctx context.Context, toE164, template, lang string, params []string) (string, error) {
	if lang == "" {
		lang = "en"
	}
	tpl := &waTemplate{Name: template, Language: waLanguage{Code: lang}}
	if len(params) > 0 {
		ps := make([]waParameter, len(params))
		for i, p := range params {
			ps[i] = waParameter{Type: "text", Text: p}
		}
		tpl.Components = []waComponent{{Type: "body", Parameters: ps}}
	}
	return c.send(ctx, waSendReq{
		MessagingProduct: "whatsapp",
		RecipientType:    "individual",
		To:               toE164,
		Type:             "template",
		Template:         tpl,
	})
}

// SendText delivers a free-form text message (only valid inside the 24-hour
// customer-service window, e.g. replying to an inbound webhook message).
func (c *Client) SendText(ctx context.Context, toE164, body string) (string, error) {
	return c.send(ctx, waSendReq{
		MessagingProduct: "whatsapp",
		RecipientType:    "individual",
		To:               toE164,
		Type:             "text",
		Text:             &waText{Body: body},
	})
}

// send posts to /{phoneNumberId}/messages with the Bearer token and returns
// the wamid of the first accepted message.
func (c *Client) send(ctx context.Context, payload waSendReq) (string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.base()+"/"+c.PhoneNumberID+"/messages", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("whatsapp: %s: %s", resp.Status, string(raw))
	}
	var out waSendResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("whatsapp: bad response: %w", err)
	}
	if len(out.Messages) == 0 {
		return "", fmt.Errorf("whatsapp: no message id in response: %s", string(raw))
	}
	return out.Messages[0].ID, nil
}

// VerifyWebhookSignature validates the X-Hub-Signature-256 header Meta
// attaches to every webhook POST: "sha256=" + hex(HMAC-SHA256(body,
// AppSecret)). The comparison is constant-time.
func (c *Client) VerifyWebhookSignature(body []byte, signatureHeader string) bool {
	if c.AppSecret == "" || !strings.HasPrefix(signatureHeader, "sha256=") {
		return false
	}
	mac := hmac.New(sha256.New, []byte(c.AppSecret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(signatureHeader))
}

// VerifyChallenge answers the Meta webhook subscription handshake
// (GET with hub.mode=subscribe, hub.verify_token, hub.challenge). It returns
// the challenge to echo back, and whether the handshake is valid.
func (c *Client) VerifyChallenge(query url.Values, verifyToken string) (string, bool) {
	if query.Get("hub.mode") != "subscribe" {
		return "", false
	}
	if verifyToken == "" || query.Get("hub.verify_token") != verifyToken {
		return "", false
	}
	challenge := query.Get("hub.challenge")
	if challenge == "" {
		return "", false
	}
	return challenge, true
}

// waWebhookPayload mirrors the Cloud API webhook envelope. Status updates
// (value.statuses) and non-message fields are deliberately not modelled —
// they are ignored.
type waWebhookPayload struct {
	Object string `json:"object"`
	Entry  []struct {
		Changes []struct {
			Field string `json:"field"`
			Value struct {
				Messages []struct {
					From      string `json:"from"`
					ID        string `json:"id"`
					Timestamp string `json:"timestamp"`
					Type      string `json:"type"`
					Text      *struct {
						Body string `json:"body"`
					} `json:"text"`
				} `json:"messages"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

// ParseInbound extracts the inbound messages from a webhook payload. Only
// entries with field=messages contribute; status/delivery updates and other
// event types are ignored. Text-less message types parse with empty Text.
func (c *Client) ParseInbound(body []byte) ([]InboundMessage, error) {
	var p waWebhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("whatsapp: bad webhook payload: %w", err)
	}
	var out []InboundMessage
	for _, e := range p.Entry {
		for _, ch := range e.Changes {
			if ch.Field != "messages" {
				continue
			}
			for _, m := range ch.Value.Messages {
				msg := InboundMessage{From: m.From, Type: m.Type}
				if m.Text != nil {
					msg.Text = m.Text.Body
				}
				if ts, err := strconv.ParseInt(m.Timestamp, 10, 64); err == nil {
					msg.Timestamp = time.Unix(ts, 0).UTC()
				}
				out = append(out, msg)
			}
		}
	}
	return out, nil
}
