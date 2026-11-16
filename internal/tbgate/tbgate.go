// Package tbgate implements AC-05: the TigerBeetle ops-maturity gate.
// TigerBeetle may replace the Postgres ledger only after every required
// operational capability has been demonstrated with evidence; until then
// Postgres remains the default ledger (see docs/TRACEABILITY.md AC-05).
package tbgate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/munisp/pfaengine/internal/money"
)

// Capability is one demonstrated operational capability with evidence
// (drill report, soak-test logs, parity run output, ...).
type Capability struct {
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	Evidence string `json:"evidence"`
}

// RequiredCapabilities lists every capability that must pass before
// TigerBeetle is adopted as the ledger hot path.
func RequiredCapabilities() []string {
	return []string{
		"replication-3node",
		"failover-drill",
		"backup-restore-drill",
		"soak-test-72h",
		"dual-control-export",
		"ledger-replay-parity",
	}
}

// Assess reports whether TigerBeetle may be adopted: true only when
// every required capability is present and passed. missing lists the
// required capabilities that are absent, failed, or unevidenced
// (sorted).
func Assess(caps []Capability) (adopt bool, missing []string) {
	byName := map[string]Capability{}
	for _, c := range caps {
		byName[c.Name] = c
	}
	for _, req := range RequiredCapabilities() {
		c, ok := byName[req]
		if !ok || !c.Passed || c.Evidence == "" {
			missing = append(missing, req)
		}
	}
	sort.Strings(missing)
	return len(missing) == 0, missing
}

// Entry is a minimal ledger posting line used for replay comparison
// between the Go ledger and a TigerBeetle cluster.
type Entry struct {
	AccountID string     `json:"account_id"`
	Debit     money.Kobo `json:"debit"`
	Credit    money.Kobo `json:"credit"`
}

// ErrParity is returned when the two replays diverge; the message lists
// the first differing positions.
var ErrParity = fmt.Errorf("tbgate: ledger replay parity mismatch")

// LedgerParity replays both journals and requires identical ordered
// entries. Any length or per-position difference is a parity failure —
// the TB cluster must reproduce the Go ledger byte-for-byte in effect
// before adoption.
func LedgerParity(goLedger, tbLedger []Entry) error {
	var problems []string
	n := len(goLedger)
	if len(tbLedger) < n {
		n = len(tbLedger)
	}
	for i := 0; i < n; i++ {
		if goLedger[i] != tbLedger[i] {
			problems = append(problems, fmt.Sprintf("entry %d: go=%+v tb=%+v", i, goLedger[i], tbLedger[i]))
		}
	}
	if len(goLedger) != len(tbLedger) {
		problems = append(problems, fmt.Sprintf("length: go=%d tb=%d", len(goLedger), len(tbLedger)))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrParity, strings.Join(problems, "; "))
	}
	return nil
}
