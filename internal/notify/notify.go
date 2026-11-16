// Package notify implements FR-503 / INT-07: templated multi-channel
// notifications (SMS/email/push/WhatsApp) with a full template audit trail.
package notify

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type Channel string

const (
	SMS      Channel = "SMS"
	Email    Channel = "EMAIL"
	Push     Channel = "PUSH"
	WhatsApp Channel = "WHATSAPP"
)

type Template struct {
	ID      string  `json:"id"`
	Channel Channel `json:"channel"`
	Name    string  `json:"name"`
	Body    string  `json:"body"` // {{var}} placeholders
	Version int     `json:"version"`
}

// Render substitutes {{var}} placeholders.
func Render(t Template, vars map[string]string) string {
	out := t.Body
	for k := range vars {
		out = strings.ReplaceAll(out, "{{"+k+"}}", vars[k])
	}
	return out
}

// Sender delivers a message; MockSender logs deliveries (Termii/Twilio
// adapters plug in behind the same interface, INT-07).
type Sender interface {
	Send(ch Channel, to, body string) (deliveryID string, err error)
}

type MockSender struct {
	mu        sync.Mutex
	Delivered []string
	Fail      bool
}

func (m *MockSender) Send(ch Channel, to, body string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail {
		return "", fmt.Errorf("notify: gateway error")
	}
	id := fmt.Sprintf("dlv-%d", len(m.Delivered)+1)
	m.Delivered = append(m.Delivered, fmt.Sprintf("%s|%s|%s", ch, to, body))
	return id, nil
}

// DeliveryRecord is the template audit trail (FR-503).
type DeliveryRecord struct {
	ID              string    `json:"id"`
	TenantID        string    `json:"tenant_id"`
	TemplateID      string    `json:"template_id"`
	TemplateVersion int       `json:"template_version"`
	Channel         Channel   `json:"channel"`
	To              string    `json:"to"`
	Body            string    `json:"body"`
	DeliveryID      string    `json:"delivery_id"`
	At              time.Time `json:"at"`
}

type Service struct {
	mu        sync.Mutex
	templates map[string]Template
	records   []DeliveryRecord
	sender    Sender
	seq       int
}

func NewService(sender Sender) *Service {
	return &Service{templates: map[string]Template{}, sender: sender}
}

func (s *Service) RegisterTemplate(t Template) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.templates[t.ID] = t
}

func (s *Service) Send(tenantID, templateID, to string, vars map[string]string) (*DeliveryRecord, error) {
	s.mu.Lock()
	t, ok := s.templates[templateID]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("notify: template %s not found", templateID)
	}
	body := Render(t, vars)
	dlvID, err := s.sender.Send(t.Channel, to, body)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	rec := &DeliveryRecord{
		ID: fmt.Sprintf("ntf-%d", s.seq), TenantID: tenantID, TemplateID: templateID,
		TemplateVersion: t.Version, Channel: t.Channel, To: to, Body: body,
		DeliveryID: dlvID, At: time.Now().UTC(),
	}
	s.records = append(s.records, *rec)
	return rec, nil
}

// AuditTrail returns the template delivery audit trail (FR-503).
func (s *Service) AuditTrail(tenantID string) []DeliveryRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []DeliveryRecord
	for _, r := range s.records {
		if r.TenantID == tenantID {
			out = append(out, r)
		}
	}
	return out
}
