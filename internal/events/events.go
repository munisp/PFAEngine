// Package events implements the append-only event backbone (AR-01, §3.1
// event-driven decision). In-process bus for single-binary/dev; the same
// envelopes map 1:1 onto Kafka topics in deploy/.
package events

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// Canonical topics (Kafka topic names in deploy/kafka-topics.yaml).
const (
	TopicMemberCreated         = "member.created"
	TopicKYCVerified           = "kyc.verified"
	TopicRSAOpened             = "rsa.opened"
	TopicConsentRecorded       = "consent.recorded"
	TopicContributionScheduled = "contribution.scheduled"
	TopicContributionPosted    = "contribution.posted"
	TopicReconException        = "recon.exception.raised"
	TopicReconResolved         = "recon.exception.resolved"
	TopicLedgerPosted          = "ledger.entry.posted"
	TopicNAVPublished          = "fund.nav.published"
	TopicClaimSubmitted        = "claim.submitted"
	TopicClaimDecided          = "claim.decided"
	TopicTransferStateChanged  = "transfer.state.changed"
	TopicLivenessDue           = "retiree.liveness.due"
	TopicLivenessCompleted     = "retiree.liveness.completed"
	TopicRetireeSuspended      = "retiree.suspended"
	TopicStatementGenerated    = "statement.generated"
	TopicNotificationSent      = "notification.sent"
	TopicTenantProvisioned     = "tenant.provisioned"
	TopicEmployerDefault       = "employer.default.detected"
	TopicScreeningFlag         = "aml.screening.flagged"
)

// Envelope is the canonical event envelope carried on the backbone.
type Envelope struct {
	ID          string          `json:"id"`
	Topic       string          `json:"topic"`
	TenantID    string          `json:"tenant_id"`
	AggregateID string          `json:"aggregate_id"`
	Actor       string          `json:"actor"`
	OccurredAt  time.Time       `json:"occurred_at"`
	RuleVersion string          `json:"rule_version,omitempty"` // SCM-15 traceability
	Payload     json.RawMessage `json:"payload"`
}

func NewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// New builds an envelope, marshalling the payload.
func New(topic, tenantID, aggregateID, actor string, payload any) Envelope {
	b, _ := json.Marshal(payload)
	return Envelope{
		ID: NewID(), Topic: topic, TenantID: tenantID, AggregateID: aggregateID,
		Actor: actor, OccurredAt: time.Now().UTC(), Payload: b,
	}
}

// Bus is a synchronous in-process pub/sub with durable per-topic log.
type Bus struct {
	mu   sync.RWMutex
	log  []Envelope
	subs map[string][]func(Envelope)
}

func NewBus() *Bus { return &Bus{subs: map[string][]func(Envelope){}} }

func (b *Bus) Publish(e Envelope) {
	b.mu.Lock()
	b.log = append(b.log, e)
	handlers := append([]func(Envelope){}, b.subs[e.Topic]...)
	b.mu.Unlock()
	for _, h := range handlers {
		h(e)
	}
}

func (b *Bus) Subscribe(topic string, h func(Envelope)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[topic] = append(b.subs[topic], h)
}

// Replay returns the append-only topic log (regulator replay, NFR-05).
func (b *Bus) Replay(topic string) []Envelope {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []Envelope
	for _, e := range b.log {
		if topic == "" || e.Topic == topic {
			out = append(out, e)
		}
	}
	return out
}
