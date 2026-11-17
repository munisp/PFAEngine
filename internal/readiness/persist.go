package readiness

import (
	"encoding/json"
	"strconv"

	"github.com/munisp/pfaengine/internal/persist"
)

// kindReport is the snapshot kind owned by this engine.
const kindReport = "readiness.report"

// AttachSink wires write-through snapshot persistence (nil disables it,
// pure in-memory mode).
func (e *Engine) AttachSink(s persist.Sink) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sink = s
}

// Restore rehydrates reports from snapshots, ignoring foreign kinds. It
// performs no side-effects and does not re-persist. The ID sequence is
// advanced past every restored ID so post-restore IDs never collide.
func (e *Engine) Restore(snaps []persist.Snapshot) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, sn := range persist.OfKind(snaps, kindReport) {
		var r Report
		if err := json.Unmarshal(sn.Payload, &r); err != nil {
			return err
		}
		bumpSeq(&e.seq, r.ID)
		e.reports = append(e.reports, r)
	}
	return nil
}

// bumpSeq advances seq past the numeric suffix of id (e.g. "rdy-000007" → 7)
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
