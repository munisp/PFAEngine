package ledger

import (
	"fmt"
	"testing"

	"github.com/munisp/pfaengine/internal/money"
)

// benchLedger returns a ledger preloaded with n balanced entries across
// two accounts (asset cash / liability rsa) — the hot-path fixture.
func benchLedger(b *testing.B, n int) *Ledger {
	b.Helper()
	l := New()
	if err := l.OpenAccount(Account{ID: "cash", TenantID: "t1", Name: "Custodian Cash", Type: Asset, Currency: "NGN"}); err != nil {
		b.Fatal(err)
	}
	if err := l.OpenAccount(Account{ID: "rsa", TenantID: "t1", Name: "RSA Liability", Type: Liability, Currency: "NGN"}); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < n; i++ {
		amt := money.Kobo(100000 + int64(i%997))
		if _, err := l.Post("t1", "contribution", fmt.Sprintf("REF-%d", i), []Posting{
			{AccountID: "cash", Debit: amt},
			{AccountID: "rsa", Credit: amt},
		}); err != nil {
			b.Fatalf("preload post %d: %v", i, err)
		}
	}
	return l
}

// BenchmarkPost measures the append+validate hot path on a ledger already
// holding 1000 entries (FR-302).
func BenchmarkPost(b *testing.B) {
	l := benchLedger(b, 1000)
	amt := money.Kobo(14400000) // NGN 144,000.00 monthly contribution
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := l.Post("t1", "contribution", fmt.Sprintf("BENCH-%d", i), []Posting{
			{AccountID: "cash", Debit: amt},
			{AccountID: "rsa", Credit: amt},
		}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkBalance measures the balance projection over 1000 entries.
func BenchmarkBalance(b *testing.B) {
	l := benchLedger(b, 1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if bal := l.Balance("cash"); bal <= 0 {
			b.Fatalf("unexpected balance %d", bal)
		}
	}
}

// BenchmarkTrialBalance measures the full trial-balance projection.
func BenchmarkTrialBalance(b *testing.B) {
	l := benchLedger(b, 1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tb := l.TrialBalance()
		if len(tb) != 2 {
			b.Fatalf("unexpected trial balance size %d", len(tb))
		}
	}
}

// BenchmarkReplay measures deterministic replay (FR-306) over 1000 entries.
func BenchmarkReplay(b *testing.B) {
	l := benchLedger(b, 1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bal := l.Replay()
		if bal["cash"] <= 0 {
			b.Fatalf("unexpected replay balance %d", bal["cash"])
		}
	}
}
