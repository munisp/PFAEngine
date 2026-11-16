// Package webhk implements INT-09 / §6.5: signed webhooks with HMAC-SHA256,
// per-endpoint secrets, event filtering, and delivery log.
package webhk

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/events"
)

type Endpoint struct {
	ID       string   `json:"id"`
	TenantID string   `json:"tenant_id"`
	URL      string   `json:"url"`
	Secret   string   `json:"-"`
	Events   []string `json:"events"`
	Active   bool     `json:"active"`
}

type Delivery struct {
	ID         string    `json:"id"`
	EndpointID string    `json:"endpoint_id"`
	Event      string    `json:"event"`
	Payload    []byte    `json:"payload"`
	Signature  string    `json:"signature"`
	Delivered  bool      `json:"delivered"`
	At         time.Time `json:"at"`
}

// Sign computes the X-Signature header value.
func Sign(secret string, payload []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	return "sha256=" + hex.EncodeToString(h.Sum(nil))
}

// VerifySignature lets receivers validate deliveries.
func VerifySignature(secret string, payload []byte, signature string) bool {
	return hmac.Equal([]byte(Sign(secret, payload)), []byte(signature))
}

type Dispatcher struct {
	mu         sync.Mutex
	endpoints  map[string]*Endpoint
	deliveries []Delivery
	sender     func(url string, payload []byte, signature string) error
	seq        int
}

func NewDispatcher(sender func(url string, payload []byte, signature string) error) *Dispatcher {
	return &Dispatcher{endpoints: map[string]*Endpoint{}, sender: sender}
}

func (d *Dispatcher) Register(e Endpoint) *Endpoint {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seq++
	e.ID = fmt.Sprintf("wh-%d", d.seq)
	e.Active = true
	cp := e
	d.endpoints[e.ID] = &cp
	return &cp
}

// Dispatch fans an event out to matching endpoints with signatures.
func (d *Dispatcher) Dispatch(env events.Envelope) []Delivery {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []Delivery
	for _, ep := range d.endpoints {
		if !ep.Active || ep.TenantID != env.TenantID {
			continue
		}
		match := false
		for _, ev := range ep.Events {
			if ev == env.Topic || ev == "*" {
				match = true
			}
		}
		if !match {
			continue
		}
		body, _ := json.Marshal(env)
		sig := Sign(ep.Secret, body)
		d.seq++
		dlv := Delivery{
			ID: fmt.Sprintf("whd-%d", d.seq), EndpointID: ep.ID, Event: env.Topic,
			Payload: body, Signature: sig, At: time.Now().UTC(),
		}
		if d.sender == nil || d.sender(ep.URL, body, sig) == nil {
			dlv.Delivered = true
		}
		d.deliveries = append(d.deliveries, dlv)
		out = append(out, dlv)
	}
	return out
}

func (d *Dispatcher) Deliveries() []Delivery {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Delivery{}, d.deliveries...)
}
