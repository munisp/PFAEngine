// Package embedded implements the open-pensions developer layer: scoped
// API keys and webhook subscriptions so payroll providers, gig platforms,
// HR tools and banks can embed pension contributions and balance checks
// directly in their products — the distribution moat for the platform.
package embedded

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Scope is an API permission.
type Scope string

const (
	ScopeContribute  Scope = "contributions:write"
	ScopeBalanceRead Scope = "balances:read"
	ScopeEnrol       Scope = "enrolment:write"
	ScopeWebhooks    Scope = "webhooks:manage"
)

var (
	ErrNoKey        = errors.New("embedded: api key not found")
	ErrRevoked      = errors.New("embedded: api key revoked")
	ErrScopeDenied  = errors.New("embedded: scope not granted")
	ErrNoWebhook    = errors.New("embedded: webhook not found")
)

// Key is an issued API key. Only the SHA-256 hash is stored.
type Key struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Label     string    `json:"label"` // e.g. "payroll-partner-acme"
	Hash      string    `json:"-"`
	Scopes    []Scope   `json:"scopes"`
	Sandbox   bool      `json:"sandbox"`
	Revoked   bool      `json:"revoked"`
	CreatedAt time.Time `json:"created_at"`
}

// Webhook is an event subscription.
type Webhook struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	URL       string    `json:"url"`
	Events    []string  `json:"events"` // contribution.posted | benefit.approved | ...
	Secret    string    `json:"-"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

// Delivery is one webhook delivery attempt (audit).
type Delivery struct {
	WebhookID string    `json:"webhook_id"`
	Event     string    `json:"event"`
	Status    int       `json:"status"`
	At        time.Time `json:"at"`
}

// Engine manages keys, webhooks and deliveries.
type Engine struct {
	mu         sync.Mutex
	keys       map[string]*Key // tenantID|id
	webhooks   map[string]*Webhook
	deliveries []Delivery
	seq        int
}

func NewEngine() *Engine {
	return &Engine{keys: map[string]*Key{}, webhooks: map[string]*Webhook{}}
}

// IssueKey creates a key and returns the plaintext token once — only the
// hash is retained.
func (e *Engine) IssueKey(tenantID, label string, scopes []Scope, sandbox bool, at time.Time) (Key, string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return Key{}, "", err
	}
	token := "pfa_" + hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	k := &Key{ID: fmt.Sprintf("key-%06d", e.seq), TenantID: tenantID, Label: label,
		Hash: hex.EncodeToString(sum[:]), Scopes: scopes, Sandbox: sandbox, CreatedAt: at}
	e.keys[tenantID+"|"+k.ID] = k
	return *k, token, nil
}

// Authorize checks a presented token for a scope.
func (e *Engine) Authorize(tenantID, token string, scope Scope) (Key, error) {
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, k := range e.keys {
		if k.TenantID == tenantID && k.Hash == hash {
			if k.Revoked {
				return Key{}, ErrRevoked
			}
			for _, s := range k.Scopes {
				if s == scope {
					return *k, nil
				}
			}
			return Key{}, ErrScopeDenied
		}
	}
	return Key{}, ErrNoKey
}

// RevokeKey disables a key.
func (e *Engine) RevokeKey(tenantID, keyID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	k, ok := e.keys[tenantID+"|"+keyID]
	if !ok {
		return ErrNoKey
	}
	k.Revoked = true
	return nil
}

// Subscribe registers a webhook.
func (e *Engine) Subscribe(tenantID, url string, events []string, at time.Time) Webhook {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	w := &Webhook{ID: fmt.Sprintf("wh-%06d", e.seq), TenantID: tenantID, URL: url,
		Events: events, Secret: hex.EncodeToString(raw), Active: true, CreatedAt: at}
	e.webhooks[tenantID+"|"+w.ID] = w
	return *w
}

// Dispatch fans an event out to matching active webhooks, recording
// deliveries (transport is performed by the caller/notifier).
func (e *Engine) Dispatch(tenantID, event string, at time.Time) []Delivery {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Delivery
	for _, w := range e.webhooks {
		if w.TenantID != tenantID || !w.Active {
			continue
		}
		for _, ev := range w.Events {
			if ev == event {
				d := Delivery{WebhookID: w.ID, Event: event, Status: 202, At: at}
				e.deliveries = append(e.deliveries, d)
				out = append(out, d)
			}
		}
	}
	return out
}

// Webhooks lists a tenant's subscriptions.
func (e *Engine) Webhooks(tenantID string) []Webhook {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Webhook
	for _, w := range e.webhooks {
		if w.TenantID == tenantID {
			out = append(out, *w)
		}
	}
	return out
}
