package claimsfast

import (
	"encoding/json"
	"strconv"

	"github.com/munisp/pfaengine/internal/persist"
)

// kindPack is the snapshot kind owned by this engine.
const kindPack = "claimsfast.pack"

// AttachSink wires write-through snapshot persistence (nil disables it,
// pure in-memory mode).
func (e *Engine) AttachSink(s persist.Sink) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sink = s
}

// Restore rehydrates claim packs from snapshots, ignoring foreign kinds.
// It performs no side-effects and does not re-persist. The ID sequence is
// advanced past every restored ID so post-restore IDs never collide.
func (e *Engine) Restore(snaps []persist.Snapshot) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, sn := range persist.OfKind(snaps, kindPack) {
		var p Pack
		if err := json.Unmarshal(sn.Payload, &p); err != nil {
			return err
		}
		bumpSeq(&e.seq, p.ID)
		e.packs[p.TenantID+"|"+p.RSAID] = &p
	}
	return nil
}

// bumpSeq advances seq past the numeric suffix of id (e.g. "cf-000007" → 7)
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
