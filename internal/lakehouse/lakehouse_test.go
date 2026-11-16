package lakehouse

import (
	"bytes"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

func at(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
}

func TestMemorySinkConcurrent(t *testing.T) {
	s := NewMemorySink()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Write(Event{TenantID: "t1", Kind: KindContribution}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := len(s.Events()); got != 50 {
		t.Fatalf("events = %d, want 50", got)
	}
}

func TestParquetLine(t *testing.T) {
	e := Event{TenantID: "t1", Kind: KindContribution, At: at(2026, 3, 5),
		AmountKobo: money.NGN(18_500), Meta: map[string]string{"member_id": "m1"}}
	line := ParquetLine(e)
	if !bytes.HasSuffix(line, []byte("\n")) {
		t.Fatalf("line must end with newline: %q", line)
	}
	var back Event
	if err := json.Unmarshal(bytes.TrimSpace(line), &back); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if back.TenantID != "t1" || back.AmountKobo != e.AmountKobo || back.Meta["member_id"] != "m1" {
		t.Fatalf("round-trip mismatch: %+v", back)
	}
}

func TestAggregationsEmpty(t *testing.T) {
	var ev []Event
	if got := ContributionsByMonth(ev); len(got) != 0 {
		t.Fatalf("contributions: %v", got)
	}
	if got := EmployerDelinquency(ev); len(got) != 0 {
		t.Fatalf("delinquency: %v", got)
	}
	if got := CohortRetention(ev); len(got) != 0 {
		t.Fatalf("retention: %v", got)
	}
	d := BuildDashboard(ev)
	if d.AUM != 0 || d.ActiveMembers != 0 || len(d.FundMix) != 0 {
		t.Fatalf("empty dashboard: %+v", d)
	}
}

func eventsFixture() []Event {
	return []Event{
		{TenantID: "t1", Kind: KindContribution, At: at(2026, 1, 10), AmountKobo: money.NGN(10_000), Meta: map[string]string{"member_id": "m1", "employer_id": "e1"}},
		{TenantID: "t1", Kind: KindContribution, At: at(2026, 1, 20), AmountKobo: money.NGN(5_000), Meta: map[string]string{"member_id": "m2", "employer_id": "e1"}},
		{TenantID: "t1", Kind: KindContribution, At: at(2026, 2, 10), AmountKobo: money.NGN(20_000), Meta: map[string]string{"member_id": "m1", "employer_id": "e2"}},
		{TenantID: "t1", Kind: KindPenaltyApplied, At: at(2026, 2, 15), AmountKobo: money.NGN(400), Meta: map[string]string{"employer_id": "e2"}},
		{TenantID: "t1", Kind: KindPenaltyApplied, At: at(2026, 2, 16), AmountKobo: money.NGN(600), Meta: map[string]string{"employer_id": "e2"}},
		{TenantID: "t1", Kind: KindMemberActive, At: at(2026, 2, 1), Meta: map[string]string{"member_id": "m1", "join_year": "2024"}},
		{TenantID: "t1", Kind: KindMemberActive, At: at(2026, 2, 1), Meta: map[string]string{"member_id": "m2", "join_year": "2025"}},
		{TenantID: "t1", Kind: KindMemberActive, At: at(2026, 2, 1), Meta: map[string]string{"member_id": "m3", "join_year": "2024"}},
		{TenantID: "t1", Kind: KindUnitAllocation, At: at(2026, 2, 11), AmountKobo: money.NGN(12_000), Meta: map[string]string{"fund_code": "II"}},
		{TenantID: "t1", Kind: KindUnitAllocation, At: at(2026, 2, 11), AmountKobo: money.NGN(8_000), Meta: map[string]string{"fund_code": "V"}},
		{TenantID: "t1", Kind: KindBenefitPaid, At: at(2026, 2, 12), AmountKobo: money.NGN(1_000)},
		{TenantID: "t1", Kind: KindFeeCharged, At: at(2026, 2, 12), AmountKobo: money.NGN(100)},
		// Other tenant — must not leak into t1 dashboards.
		{TenantID: "t2", Kind: KindContribution, At: at(2026, 2, 10), AmountKobo: money.NGN(999_999), Meta: map[string]string{"member_id": "x1"}},
		{TenantID: "t2", Kind: KindMemberActive, At: at(2026, 2, 1), Meta: map[string]string{"member_id": "x1", "join_year": "2024"}},
	}
}

func TestContributionsByMonth(t *testing.T) {
	got := ContributionsByMonth(eventsFixture())
	want := map[string]money.Kobo{
		"2026-01": money.NGN(15_000),
		"2026-02": money.NGN(20_000 + 999_999),
	}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("month %s = %v, want %v", k, got[k], v)
		}
	}
}

func TestEmployerDelinquency(t *testing.T) {
	got := EmployerDelinquency(eventsFixture())
	if len(got) != 1 || got["e2"] != money.NGN(1_000) {
		t.Fatalf("delinquency %v", got)
	}
}

func TestCohortRetention(t *testing.T) {
	got := CohortRetention(eventsFixture())
	want := map[string]int{"2024": 3, "2025": 1}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("cohort %s = %d, want %d", k, got[k], v)
		}
	}
}

func TestBuildDashboardTenantIsolation(t *testing.T) {
	d := BuildDashboard(FilterTenant(eventsFixture(), "t1"))
	// AUM = 35_000 contributions − 1_000 benefit − 100 fee.
	if d.AUM != money.NGN(33_900) {
		t.Errorf("AUM = %v, want 33900 NGN", d.AUM)
	}
	if d.MonthlyContributions != money.NGN(20_000) { // latest month = 2026-02
		t.Errorf("monthly = %v, want 20000 NGN", d.MonthlyContributions)
	}
	if d.PenaltiesOutstanding != money.NGN(1_000) {
		t.Errorf("penalties = %v", d.PenaltiesOutstanding)
	}
	if d.ActiveMembers != 3 {
		t.Errorf("active = %d, want 3", d.ActiveMembers)
	}
	if d.FundMix["II"] != money.NGN(12_000) || d.FundMix["V"] != money.NGN(8_000) {
		t.Errorf("fund mix %v", d.FundMix)
	}

	d2 := BuildDashboard(FilterTenant(eventsFixture(), "t2"))
	if d2.AUM != money.NGN(999_999) || d2.ActiveMembers != 1 {
		t.Errorf("t2 dashboard %+v", d2)
	}
}
