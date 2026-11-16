// Package stream binds the event backbone to Kafka (AR-01). Falls back to
// the in-process bus when KAFKA_BROKERS is unset (dev/tests).
package stream

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/munisp/pfaengine/internal/events"
)

// Publisher publishes envelopes to Kafka topics.
type Publisher struct {
	writers map[string]*kafka.Writer
	brokers []string
}

func NewPublisher(brokersCSV string) *Publisher {
	return &Publisher{writers: map[string]*kafka.Writer{}, brokers: strings.Split(brokersCSV, ",")}
}

func (p *Publisher) writerFor(topic string) *kafka.Writer {
	if w, ok := p.writers[topic]; ok {
		return w
	}
	w := &kafka.Writer{
		Addr:         kafka.TCP(p.brokers...),
		Topic:        topic,
		Balancer:     &kafka.Hash{}, // key-partitioned: per-aggregate ordering
		BatchTimeout: 10 * time.Millisecond,
		RequiredAcks: kafka.RequireAll, // ledger-grade durability
	}
	p.writers[topic] = w
	return w
}

func (p *Publisher) Publish(ctx context.Context, e events.Envelope) error {
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return p.writerFor(e.Topic).WriteMessages(ctx, kafka.Message{
		Key:   []byte(e.TenantID + ":" + e.AggregateID),
		Value: body,
	})
}

func (p *Publisher) Close() {
	for _, w := range p.writers {
		_ = w.Close()
	}
}

// Subscriber consumes a topic group into a handler (with retry).
func Subscribe(ctx context.Context, brokersCSV, topic, group string, handle func(events.Envelope)) {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  strings.Split(brokersCSV, ","),
		Topic:    topic,
		GroupID:  groupID(group, topic),
		MinBytes: 1, MaxBytes: 10 << 20,
		CommitInterval: time.Second,
	})
	go func() {
		defer r.Close()
		for {
			m, err := r.ReadMessage(ctx)
			if err != nil {
				log.Printf("[stream] %s reader stopped: %v", topic, err)
				return
			}
			var e events.Envelope
			if err := json.Unmarshal(m.Value, &e); err != nil {
				log.Printf("[stream] bad envelope on %s: %v", topic, err)
				continue
			}
			handle(e)
		}
	}()
}

func groupID(base, topic string) string { return base + "-" + topic }
