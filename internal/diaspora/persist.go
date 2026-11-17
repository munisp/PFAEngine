package diaspora

import (
	"encoding/json"
	"strconv"

	"github.com/munisp/pfaengine/internal/persist"
)

// Snapshot kinds owned by this engine.
const (
	kindMember       = "diaspora.member"
	kindRate         = "diaspora.rate"
	kindContribution = "diaspora.contribution"
)

// globalTenant is the snapshot tenant for tenant-agnostic state (FX rates
// are set platform-wide, not per tenant).
const globalTenant = "_global"

// rateState is the persisted form of one FX rate (the in-memory map entry
// has no entity struct of its own; the natural key is the currency).
type rateState struct {
	Currency    string `json:"currency"`
	KoboPerUnit int64  `json:"kobo_per_unit"`
}

// AttachSink wires write-through snapshot persistence (nil disables it,
// pure in-memory mode).
func (e *Engine) AttachSink(s persist.Sink) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sink = s
}

// Restore rehydrates members, FX rates and contributions from snapshots,
// ignoring foreign kinds. It performs no side-effects and does not
// re-persist. The ID sequence is advanced past every restored ID so
// post-restore IDs never collide.
func (e *Engine) Restore(snaps []persist.Snapshot) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, sn := range persist.OfKind(snaps, kindMember) {
		var m Member
		if err := json.Unmarshal(sn.Payload, &m); err != nil {
			return err
		}
		bumpSeq(&e.seq, m.ID)
		e.members[m.TenantID+"|"+m.RSAID] = &m
	}
	for _, sn := range persist.OfKind(snaps, kindRate) {
		var rs rateState
		if err := json.Unmarshal(sn.Payload, &rs); err != nil {
			return err
		}
		e.rates[rs.Currency] = rs.KoboPerUnit
	}
	for _, sn := range persist.OfKind(snaps, kindContribution) {
		var c Contribution
		if err := json.Unmarshal(sn.Payload, &c); err != nil {
			return err
		}
		bumpSeq(&e.seq, c.ID)
		e.contribs = append(e.contribs, c)
	}
	return nil
}

// bumpSeq advances seq past the numeric suffix of id (e.g. "dia-000007" → 7)
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
