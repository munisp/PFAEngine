// Package mlscore is the Go client for the Python ML inference service
// (ml/pfa_ml/serve.py). It is FAIL-CLOSED: on any error, timeout, or
// degraded response, Score returns ok=false and callers must fall back to
// the rules engine (internal/fraud). The ML score and rules score are
// fused by callers: final = max(rulesScore, mlScore*100) unless the ML
// service flags the txn outright.
package mlscore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Txn is the inference-time transaction view shared with pfa_ml.features.
// Amounts are integer kobo, matching the training data contract.
type Txn struct {
	TxnID       string `json:"txn_id"`
	MemberID    string `json:"member_id"`
	Kind        string `json:"kind"` // contribution|withdrawal|transfer|inquiry|beneficiary_change
	AmountKobo  int64  `json:"amount_kobo"`
	Channel     string `json:"channel"` // ussd|mobile|web|branch|agent
	State       string `json:"state"`
	Day         int    `json:"day"`
	MinuteOfDay int    `json:"minute_of_day"`
	DeviceID    string `json:"device_id"`
}

// ScoreResponse mirrors serve.py /score.
type ScoreResponse struct {
	Score     float64 `json:"score"`
	Threshold float64 `json:"threshold"`
	Flagged   bool    `json:"flagged"`
	Model     string  `json:"model"`
	Arm       string  `json:"arm"`
	Degraded  bool    `json:"degraded"`
	Reason    string  `json:"reason,omitempty"`
}

type Client struct {
	base string
	hc   *http.Client
}

// New builds a client; timeout of 800ms keeps ML scoring off the critical
// path latency budget.
func New(base string) *Client {
	return &Client{base: base, hc: &http.Client{Timeout: 800 * time.Millisecond}}
}

// Score returns the fusion model's fraud probability for txn.
// ok=false means: use the rules engine fallback (service down or degraded).
func (c *Client) Score(ctx context.Context, memberID string, txn Txn, history []Txn) (ScoreResponse, bool) {
	body, _ := json.Marshal(map[string]any{
		"member_id": memberID, "txn": txn, "history": history,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/score", bytes.NewReader(body))
	if err != nil {
		return ScoreResponse{}, false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return ScoreResponse{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ScoreResponse{}, false
	}
	var out ScoreResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ScoreResponse{}, false
	}
	if out.Degraded {
		return out, false
	}
	return out, true
}

// Health reports the champion model version ("" when degraded).
func (c *Client) Health(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/healthz", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var h struct {
		Status   string  `json:"status"`
		Champion *string `json:"champion"`
		Degraded bool    `json:"degraded"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return "", err
	}
	if h.Degraded || h.Champion == nil {
		return "", fmt.Errorf("ml service degraded")
	}
	return *h.Champion, nil
}
