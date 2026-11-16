package transfer

import (
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

func Test365DayRule(t *testing.T) {
	s := NewService()
	now := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	r, err := s.Initiate("t1", "rsa-1", "PEN100", Outgoing, "pfa-b", money.NGN(1000000), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitToCycle(r.ID, now); err != nil { // March = window open
		t.Fatal(err)
	}
	if err := s.Settle(r.ID, now.Add(10*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Second transfer within 365 days must fail.
	_, err = s.Initiate("t1", "rsa-1", "PEN100", Outgoing, "pfa-c", money.NGN(1000000), now.Add(100*24*time.Hour))
	if err == nil {
		t.Fatal("365-day rule not enforced")
	}
	// After 365 days it succeeds.
	if _, err := s.Initiate("t1", "rsa-1", "PEN100", Outgoing, "pfa-c", money.NGN(1000000), now.Add(400*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func TestQuarterlyQueueing(t *testing.T) {
	s := NewService()
	mid := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC) // April: window closed
	r, _ := s.Initiate("t1", "rsa-2", "PEN200", Incoming, "pfa-a", money.NGN(50000), mid)
	s.Validate(r.ID)
	q, err := s.SubmitToCycle(r.ID, mid)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(r.ID)
	if got.State != QueuedForCycle || q != "2026-Q2" {
		t.Fatalf("expected queued for Q2, got %s %s", got.State, q)
	}
}
