package vendors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/munisp/pfaengine/internal/notify"
)

// Termii implements notify.Sender for SMS (INT-07).
type Termii struct {
	APIKey   string
	SenderID string
	BaseURL  string // https://v3.api.termii.com
	HTTP     *http.Client
}

type termiiSendReq struct {
	To      string `json:"to"`
	From    string `json:"from"`
	SMS     string `json:"sms"`
	Type    string `json:"type"`
	Channel string `json:"channel"`
	APIKey  string `json:"api_key"`
}

type termiiSendResp struct {
	MessageID string `json:"message_id"`
	Code      string `json:"code"`
}

func (t *Termii) Send(ch notify.Channel, to, body string) (string, error) {
	if ch != notify.SMS {
		return "", fmt.Errorf("termii: channel %s unsupported by this adapter", ch)
	}
	hc := t.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	b, _ := json.Marshal(termiiSendReq{To: to, From: t.SenderID, SMS: body, Type: "plain", Channel: "generic", APIKey: t.APIKey})
	req, _ := http.NewRequestWithContext(context.Background(), "POST", t.BaseURL+"/api/sms/send", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("termii: %s: %s", resp.Status, string(raw))
	}
	var out termiiSendResp
	_ = json.Unmarshal(raw, &out)
	return out.MessageID, nil
}
