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
	// March is the 3rd month of Q1: rolls to next quarter's ETD (30 Jun).
	q, err := s.SubmitToCycle(r.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(r.ID)
	if got.State != QueuedForCycle || q != "2026-Q2" || !got.ETD.Equal(time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("expected queued for Q2 ETD 2026-06-30, got %s %s %s", got.State, q, got.ETD)
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
	// April is month 1 of Q2: joins the current quarter's cycle, ETD 30 Jun.
	mid := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	r, _ := s.Initiate("t1", "rsa-2", "PEN200", Incoming, "pfa-a", money.NGN(50000), mid)
	s.Validate(r.ID)
	q, err := s.SubmitToCycle(r.ID, mid)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(r.ID)
	if got.State != SubmittedToRTS || q != "2026-Q2" || !got.ETD.Equal(time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("expected submitted for Q2 ETD 2026-06-30, got %s %s %s", got.State, q, got.ETD)
	}
}

func TestEffectiveTransferDate(t *testing.T) {
	cases := []struct {
		name string
		at   time.Time
		want time.Time
	}{
		{"Q1 month 1", time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)},
		{"Q1 month 2", time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)},
		{"Q1 month 3 rolls to Q2", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)},
		{"Q2 month 1", time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)},
		{"Q3 month 3 rolls to Q4", time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)},
		{"Q4 month 3 rolls across year", time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), time.Date(2027, 3, 31, 0, 0, 0, 0, time.UTC)},
		{"Q4 month 2", time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EffectiveTransferDate(tc.at); !got.Equal(tc.want) {
				t.Fatalf("EffectiveTransferDate(%s) = %s, want %s", tc.at, got, tc.want)
			}
		})
	}
}

func TestNetPositions(t *testing.T) {
	reqs := []Request{
		{Direction: Outgoing, CounterpartyPFA: "pfa-a", Value: money.NGN(1000000)},
		{Direction: Outgoing, CounterpartyPFA: "pfa-a", Value: money.NGN(250000)},
		{Direction: Incoming, CounterpartyPFA: "pfa-a", Value: money.NGN(500000)},
		{Direction: Incoming, CounterpartyPFA: "pfa-b", Value: money.NGN(800000)},
		{Direction: Incoming, CounterpartyPFA: "pfa-c", Value: money.NGN(300000)},
		{Direction: Outgoing, CounterpartyPFA: "pfa-c", Value: money.NGN(300000)},
	}
	net := NetPositions(reqs)
	if net["pfa-a"] != -money.NGN(750000) { // payable
		t.Fatalf("pfa-a net %s", net["pfa-a"].Naira())
	}
	if net["pfa-b"] != money.NGN(800000) { // receivable
		t.Fatalf("pfa-b net %s", net["pfa-b"].Naira())
	}
	if net["pfa-c"] != 0 { // fully netted
		t.Fatalf("pfa-c net %s", net["pfa-c"].Naira())
	}
}
