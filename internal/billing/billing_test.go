package billing

import (
	"testing"

	"github.com/munisp/pfaengine/internal/money"
)

var testPlan = Plan{
	Code:                "GROWTH",
	BaseMonthly:         money.NGN(250000), // ₦250,000
	PerActiveMember:     money.NGN(120),    // ₦120/member
	PerActiveMemberFree: 1000,
}

func TestInvoice(t *testing.T) {
	cases := []struct {
		name       string
		plan       Plan
		active     int
		period     string
		wantMember money.Kobo
		wantTotal  money.Kobo
	}{
		{"below free allowance", testPlan, 1000, "2026-11", 0, money.NGN(250000)},
		{"one above allowance", testPlan, 1001, "2026-11", money.NGN(120), money.NGN(250120)},
		{"many members", testPlan, 51000, "2026-11", money.NGN(6000000), money.NGN(6250000)},
		{"zero members", testPlan, 0, "2026-11", 0, money.NGN(250000)},
		{"no free allowance", Plan{Code: "X", BaseMonthly: money.NGN(1000), PerActiveMember: money.NGN(10)}, 5, "2026-11", money.NGN(50), money.NGN(1050)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv := tc.plan.Invoice(tc.active, tc.period)
			if inv.Period != tc.period || inv.Base != tc.plan.BaseMonthly {
				t.Fatalf("inv = %+v", inv)
			}
			if inv.MemberCharge != tc.wantMember {
				t.Fatalf("MemberCharge = %s, want %s", inv.MemberCharge.Naira(), tc.wantMember.Naira())
			}
			if inv.Total != tc.wantTotal {
				t.Fatalf("Total = %s, want %s", inv.Total.Naira(), tc.wantTotal.Naira())
			}
			if inv.Total != inv.Base+inv.MemberCharge {
				t.Fatal("total must equal base + member charge")
			}
		})
	}
}

func TestInvoiceLines(t *testing.T) {
	inv := testPlan.Invoice(2000, "2026-11")
	lines := inv.Lines()
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d", len(lines))
	}
	var sum money.Kobo
	for _, l := range lines {
		if l.Description == "" {
			t.Fatal("line description required")
		}
		sum += l.Amount
	}
	if sum != inv.Total {
		t.Fatalf("lines sum %s != total %s", sum.Naira(), inv.Total.Naira())
	}
}

func TestStatusNextBoundaries(t *testing.T) {
	cases := []struct {
		days int
		want Status
	}{
		{-5, Current},
		{0, Current},
		{14, Current},
		{15, Grace},
		{30, Grace},
		{31, Overdue},
		{60, Overdue},
		{61, Suspended},
		{365, Suspended},
	}
	for _, tc := range cases {
		if got := Current.Next(tc.days); got != tc.want {
			t.Fatalf("Next(%d) = %v, want %v", tc.days, got, tc.want)
		}
	}
}

func TestPlatformAccess(t *testing.T) {
	cases := []struct {
		status Status
		want   bool
	}{
		{Current, true},
		{Grace, true},
		{Overdue, true}, // gateway should enforce read-only; see doc comment
		{Suspended, false},
	}
	for _, tc := range cases {
		if got := PlatformAccess(tc.status); got != tc.want {
			t.Fatalf("PlatformAccess(%v) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestStatusString(t *testing.T) {
	if Current.String() != "CURRENT" || Suspended.String() != "SUSPENDED" {
		t.Fatal("unexpected status strings")
	}
}
