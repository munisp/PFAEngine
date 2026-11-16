package tbgate

import (
	"errors"
	"testing"

	"github.com/munisp/pfaengine/internal/money"
)

func allPassed() []Capability {
	reqs := RequiredCapabilities()
	caps := make([]Capability, len(reqs))
	for i, r := range reqs {
		caps[i] = Capability{Name: r, Passed: true, Evidence: "drill-report-2026-11"}
	}
	return caps
}

func TestRequiredCapabilities(t *testing.T) {
	want := []string{
		"replication-3node", "failover-drill", "backup-restore-drill",
		"soak-test-72h", "dual-control-export", "ledger-replay-parity",
	}
	got := RequiredCapabilities()
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestAssess(t *testing.T) {
	cases := []struct {
		name      string
		caps      []Capability
		wantAdopt bool
		wantMiss  int
	}{
		{"all pass", allPassed(), true, 0},
		{"none", nil, false, 6},
		{"one missing", allPassed()[:5], false, 1},
		{"one failed", func() []Capability {
			c := allPassed()
			c[3].Passed = false
			return c
		}(), false, 1},
		{"one without evidence", func() []Capability {
			c := allPassed()
			c[0].Evidence = ""
			return c
		}(), false, 1},
	}
	for _, c := range cases {
		adopt, missing := Assess(c.caps)
		if adopt != c.wantAdopt || len(missing) != c.wantMiss {
			t.Errorf("%s: adopt=%v missing=%v", c.name, adopt, missing)
		}
	}
}

func TestLedgerParity(t *testing.T) {
	base := []Entry{
		{AccountID: "a1", Debit: money.NGN(100)},
		{AccountID: "a2", Credit: money.NGN(100)},
	}
	if err := LedgerParity(base, append([]Entry{}, base...)); err != nil {
		t.Fatal(err)
	}
	if err := LedgerParity(nil, nil); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		tb   []Entry
	}{
		{"diverged amount", []Entry{
			{AccountID: "a1", Debit: money.NGN(99)},
			{AccountID: "a2", Credit: money.NGN(100)},
		}},
		{"missing entry", base[:1]},
		{"extra entry", append(append([]Entry{}, base...), Entry{AccountID: "a3", Debit: 1})},
	}
	for _, c := range cases {
		if err := LedgerParity(base, c.tb); !errors.Is(err, ErrParity) {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
}
