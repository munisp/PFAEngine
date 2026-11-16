package penalty

import (
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

func TestWorkingDayDeadline(t *testing.T) {
	cases := []struct {
		name string
		paid time.Time
		n    int
		want time.Time
	}{
		{"midweek", time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), 7, time.Date(2026, 1, 14, 0, 0, 0, 0, time.UTC)},              // Mon -> Wed next week
		{"friday skips weekend", time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC), 7, time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)}, // Fri -> Tue week after
		{"one day", time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC), 1, time.Date(2026, 1, 12, 0, 0, 0, 0, time.UTC)},              // Fri -> Mon
		{"month boundary", time.Date(2026, 1, 30, 0, 0, 0, 0, time.UTC), 7, time.Date(2026, 2, 10, 0, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WorkingDayDeadline(tc.paid, tc.n); !got.Equal(tc.want) {
				t.Fatalf("WorkingDayDeadline(%s, %d) = %s, want %s", tc.paid, tc.n, got, tc.want)
			}
		})
	}
}

// arrearsOn builds arrears for salary paid Friday 2026-01-09; deadline is
// Tuesday 2026-01-20.
func arrearsOn(asOf time.Time) Arrears {
	return Arrears{
		EmployerID: "emp-1",
		Principal:  money.NGN(500000),
		SalaryPaid: time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC),
		AsOf:       asOf,
	}
}

func TestMonthsOverdue(t *testing.T) {
	dl := time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC) // computed deadline
	cases := []struct {
		name string
		asOf time.Time
		want int
	}{
		{"before deadline", dl.AddDate(0, 0, -1), 0},
		{"exactly at deadline", dl, 0},
		{"one day past", dl.AddDate(0, 0, 1), 1}, // part thereof rounds up
		{"exactly one month past", dl.AddDate(0, 1, 0), 1},
		{"one month and a day", dl.AddDate(0, 1, 1), 2},
		{"three whole months", dl.AddDate(0, 3, 0), 3},
		{"three months plus", dl.AddDate(0, 3, 5), 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := arrearsOn(tc.asOf).MonthsOverdue(); got != tc.want {
				t.Fatalf("MonthsOverdue() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestPenalty(t *testing.T) {
	dl := time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)
	// Not overdue: zero penalty.
	if p := arrearsOn(dl).Penalty(DefaultPenaltyBpsPerMonth); p != 0 {
		t.Fatalf("expected 0 at deadline, got %s", p.Naira())
	}
	// 2% per month or part thereof on ₦500,000 => ₦10,000 per month.
	a := arrearsOn(dl.AddDate(0, 2, 10)) // 3 months/part
	want := money.NGN(30000)
	if p := a.Penalty(DefaultPenaltyBpsPerMonth); p != want {
		t.Fatalf("Penalty() = %s, want %s", p.Naira(), want.Naira())
	}
}

func TestPenComReportable(t *testing.T) {
	dl := time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		asOf time.Time
		want bool
	}{
		{"at deadline", dl, false},
		{"13 days past", dl.AddDate(0, 0, 13), false},
		{"exactly 14 days past", dl.AddDate(0, 0, 14), false}, // due >14 days only
		{"15 days past", dl.AddDate(0, 0, 15), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PenComReportable(arrearsOn(tc.asOf)); got != tc.want {
				t.Fatalf("PenComReportable() = %v, want %v", got, tc.want)
			}
		})
	}
}
