package roundup

import (
	"encoding/json"
	"strconv"

	"github.com/munisp/pfaengine/internal/persist"
)

// Snapshot kinds owned by this engine.
const (
	kindWallet = "roundup.wallet"
	kindSweep  = "roundup.sweep"
)

// AttachSink wires write-through snapshot persistence (nil disables it,
// pure in-memory mode).
func (e *Engine) AttachSink(s persist.Sink) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sink = s
}

// Restore rehydrates wallets and sweeps from snapshots, ignoring foreign
// kinds. It performs no side-effects and does not re-persist. The ID
// sequence is advanced past every restored ID so post-restore IDs never
// collide.
func (e *Engine) Restore(snaps []persist.Snapshot) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, sn := range persist.OfKind(snaps, kindWallet) {
		var w Wallet
		if err := json.Unmarshal(sn.Payload, &w); err != nil {
			return err
		}
		bumpSeq(&e.seq, w.ID)
		e.wallets[w.TenantID+"|"+w.MemberID] = &w
	}
	for _, sn := range persist.OfKind(snaps, kindSweep) {
		var s Sweep
		if err := json.Unmarshal(sn.Payload, &s); err != nil {
			return err
		}
		bumpSeq(&e.seq, s.ID)
		e.sweeps = append(e.sweeps, s)
	}
	return nil
}

// bumpSeq advances seq past the numeric suffix of id (e.g. "ru-000007" → 7)
// so freshly generated IDs never collide with restored ones.
func bumpSeq(seq *int, id string) {
	i := len(id)
	for i > 0 && id[i-1] >= '0' && id[i-1] <= '9' {
		i--
	}
	if i == len(id) {
		return
	}
	if n, err := strconv.Atoi(id[i:]); err == nil && n > *seq {
		*seq = n
	}
}
