// Package ollama integrates a self-hosted Ollama LLM server as an optional
// augmentation layer for the chatbot and ops tooling. Design rules:
//   - deterministic chatbot intents (internal/chatbot) ALWAYS run first;
//     Ollama only rephrases the canned answer into natural language
//   - fail-closed: any error/timeout returns ok=false and the canned reply
//     is used verbatim — member-facing flows never depend on the LLM
//   - no PII leaves the platform unless the operator opts in: PII stripping
//     replaces RSA PINs, names and amounts before the prompt is built
//
// Value: localized, natural-language member support in EN/Pidgin/Yoruba/
// Hausa/Igbo without per-token API cost (data-residency friendly for
// PenCom-regulated data).
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type Client struct {
	base  string
	model string
	hc    *http.Client
}

func New(base, model string) *Client {
	if model == "" {
		model = "llama3.2:3b"
	}
	return &Client{base: strings.TrimRight(base, "/"), model: model,
		hc: &http.Client{Timeout: 10 * time.Second}}
}

var (
	rePIN   = regexp.MustCompile(`PEN\d+`)
	reNaira = regexp.MustCompile(`\d[\d,]*(\.\d+)?\s?(NGN|kobo)`)
)

// StripPII removes identifiers/amounts before any prompt leaves the process.
func StripPII(s string) string {
	s = rePIN.ReplaceAllString(s, "[PIN]")
	s = reNaira.ReplaceAllString(s, "[AMOUNT]")
	return s
}

type genReq struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type genResp struct {
	Response string `json:"response"`
}

// Rephrase rewrites a canned chatbot reply naturally. ok=false → use canned.
func (c *Client) Rephrase(ctx context.Context, canned, lang string) (string, bool) {
	prompt := "Rephrase this pension-fund customer-service reply in warm, simple " +
		lang + " (keep all facts identical, one short paragraph):\n\n" + StripPII(canned)
	body, _ := json.Marshal(genReq{Model: c.model, Prompt: prompt})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", false
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	var out genResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Response == "" {
		return "", false
	}
	return strings.TrimSpace(out.Response), true
}
