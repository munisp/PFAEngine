package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/munisp/pfaengine/internal/events"
)

// TestKafkaKeyedOrderingIntegration is gated on TEST_KAFKA_BROKERS (e.g.
// "localhost:9092" or "kafka:9092" in compose); it is skipped otherwise.
//
// It publishes 100 keyed envelopes (10 aggregate keys x 10 sequence
// numbers) through the production Publisher (Hash balancer, RequireAll
// acks) and consumes them back with a fresh consumer group, asserting:
//  1. per-key ordering — keys sharing a hash partition arrive in produce
//     order (AR-01 per-aggregate ordering guarantee), and
//  2. payload integrity — every consumed envelope ID equals the exact
//     envelope published at that sequence position for that key.
func TestKafkaKeyedOrderingIntegration(t *testing.T) {
	brokers := os.Getenv("TEST_KAFKA_BROKERS")
	if brokers == "" {
		t.Skip("set TEST_KAFKA_BROKERS to run the Kafka integration test")
	}
	const keys, perKey = 10, 10
	topic := fmt.Sprintf("pfaengine.it.%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pub := NewPublisher(brokers)
	defer pub.Close()

	want := map[string][]string{} // aggregateID -> envelope IDs in produce order
	for k := 0; k < keys; k++ {
		agg := fmt.Sprintf("agg-%d", k)
		for s := 0; s < perKey; s++ {
			e := events.New(topic, "t1", agg, "integration-test", map[string]int{"seq": s})
			if err := pub.Publish(ctx, e); err != nil {
				t.Fatalf("publish %s seq %d: %v", agg, s, err)
			}
			want[agg] = append(want[agg], e.ID)
		}
	}

	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  strings.Split(brokers, ","),
		Topic:    topic,
		GroupID:  "it-" + topic, // fresh group -> starts at first offset
		MinBytes: 1,
		MaxBytes: 10 << 20,
	})
	defer r.Close()

	got := map[string][]string{}
	total := keys * perKey
	for i := 0; i < total; i++ {
		m, err := r.ReadMessage(ctx)
		if err != nil {
			t.Fatalf("consume %d/%d: %v", i, total, err)
		}
		var e events.Envelope
		if err := json.Unmarshal(m.Value, &e); err != nil {
			t.Fatalf("payload corrupt at message %d: %v", i, err)
		}
		var p struct {
			Seq int `json:"seq"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("envelope payload corrupt at message %d: %v", i, err)
		}
		ids, ok := want[e.AggregateID]
		if !ok || p.Seq >= len(ids) {
			t.Fatalf("unexpected message key=%q seq=%d", e.AggregateID, p.Seq)
		}
		if ids[p.Seq] != e.ID {
			t.Fatalf("payload integrity: key %s seq %d has envelope %s, want %s",
				e.AggregateID, p.Seq, e.ID, ids[p.Seq])
		}
		got[e.AggregateID] = append(got[e.AggregateID], e.ID)
	}

	for agg, ids := range want {
		if len(got[agg]) != perKey {
			t.Fatalf("key %s: consumed %d messages, want %d", agg, len(got[agg]), perKey)
		}
		for i, id := range ids {
			if got[agg][i] != id {
				t.Fatalf("key %s out of order at position %d: got %s, want %s",
					agg, i, got[agg][i], id)
			}
		}
	}
}
