package windup

import (
	"errors"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

func TestAdvanceHappyPath(t *testing.T) {
	w := &WindUp{TenantID: "t1", StartedAt: time.Now().UTC(), Status: Initiated}
	for _, s := range []Status{AssetsLiquidated, BenefitsComputed, Distributed, Closed} {
		if err := w.Advance(s); err != nil {
			t.Fatalf("advance to %s: %v", s, err)
		}
		if w.Status != s {
			t.Fatalf("status = %s, want %s", w.Status, s)
		}
	}
}

func TestAdvanceViolations(t *testing.T) {
	cases := []struct {
		name string
		from Status
		to   Status
	}{
		{"skip a state", Initiated, BenefitsComputed},
		{"backwards", Distributed, Initiated},
		{"stay", Closed, Closed},
		{"skip to end", Initiated, Closed},
	}
	for _, c := range cases {
		w := &WindUp{Status: c.from}
		if err := w.Advance(c.to); !errors.Is(err, ErrBadTransition) {
			t.Errorf("%s: err = %v", c.name, err)
		}
		if w.Status != c.from {
			t.Errorf("%s: status mutated to %s", c.name, w.Status)
		}
	}
}

func TestDistributionOrder(t *testing.T) {
	got := DistributionOrder()
	want := []string{
		"pending benefits in payment",
		"transfer values to other PFAs",
		"administrative costs reserve",
		"residual to contributors pro-rata",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestProRataExactSumInvariant(t *testing.T) {
	cases := []struct {
		name     string
		residual money.Kobo
		balances []money.Kobo
	}{
		{"even split", money.NGN(90_000), []money.Kobo{money.NGN(10_000), money.NGN(20_000), money.NGN(60_000)}},
		{"rounding remainder", money.NGN(100_001), []money.Kobo{money.NGN(1), money.NGN(1), money.NGN(1)}},
		{"single member", money.NGN(50_000), []money.Kobo{money.NGN(123)}},
		{"zero residual", 0, []money.Kobo{money.NGN(1_000)}},
		{"no members", money.NGN(50_000), nil},
		{"odd kobo", money.Kobo(7), []money.Kobo{money.Kobo(3), money.Kobo(5), money.Kobo(11), money.Kobo(2)}},
	}
	for _, c := range cases {
		got := ProRata(c.residual, c.balances)
		if len(got) != len(c.balances) {
			t.Errorf("%s: len = %d", c.name, len(got))
			continue
		}
		var sum money.Kobo
		for _, s := range got {
			sum += s
		}
		wantSum := c.residual
		if len(c.balances) == 0 {
			wantSum = 0
		}
		if sum != wantSum {
			t.Errorf("%s: sum = %d, want %d", c.name, sum, wantSum)
		}
	}
}

func TestProRataRemainderToLargest(t *testing.T) {
	// 100 kobo over balances 1:1:1 → floor gives 33+33+33=99, the extra
	// kobo goes to the largest balance (all equal → the earliest).
	got := ProRata(100, []money.Kobo{1, 1, 1})
	want := []money.Kobo{34, 33, 33}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	// Distinct balances: remainder goes to the largest holder.
	got = ProRata(money.Kobo(101), []money.Kobo{money.Kobo(10), money.Kobo(90)})
	if got[1] <= got[0] || got[0]+got[1] != 101 {
		t.Fatalf("got %v", got)
	}
	// floor(101*10/100)=10, floor(101*90/100)=90 → remainder 1 to largest.
	if got[0] != 10 || got[1] != 91 {
		t.Fatalf("got %v, want [10 91]", got)
	}
}
