// Package disclosure implements the corporate disclosure publishing every
// licensed PFA maintains on its website (Tranche 21): audited RSA fund
// accounts, audited company accounts, annual reports and quarterly
// returns. Publications are immutable once published (WORM — a disclosure
// is a regulatory artefact and must never change after release).
package disclosure

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/persist"
)

// Kind enumerates disclosure types.
type Kind string

const (
	AuditedRSAFund   Kind = "AUDITED_RSA_FUND"
	AuditedCompany   Kind = "AUDITED_COMPANY"
	AnnualReport     Kind = "ANNUAL_REPORT"
	QuarterlyReturns Kind = "QUARTERLY_RETURNS"
)

// Publication is one published disclosure document.
type Publication struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Kind        Kind      `json:"kind"`
	Title       string    `json:"title"`
	Period      string    `json:"period"` // e.g. "FY2025" or "2026-Q3"
	Ref         string    `json:"ref"`    // object-storage URI
	SHA256      string    `json:"sha256"`
	PublishedBy string    `json:"published_by"`
	PublishedAt time.Time `json:"published_at"`
}

// Registry stores disclosures (append-only).
type Registry struct {
	mu   sync.Mutex
	pubs map[string]*Publication
	seq  int
	sink persist.Sink // nil in pure in-memory mode
}

// New returns an empty registry.
func New() *Registry { return &Registry{pubs: map[string]*Publication{}} }

// AttachSink wires write-through snapshot persistence (NFR-10).
func (r *Registry) AttachSink(s persist.Sink) { r.sink = s }

// Restore rehydrates publications from boot snapshots; foreign kinds ignored.
func (r *Registry) Restore(snaps []persist.Snapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range persist.OfKind(snaps, "disclosure.pub") {
		var p Publication
		if err := json.Unmarshal(s.Payload, &p); err != nil {
			return err
		}
		pc := p
		r.pubs[p.ID] = &pc
		if n, err := strconv.Atoi(strings.TrimPrefix(p.ID, "dis-")); err == nil && n > r.seq {
			r.seq = n
		}
	}
	return nil
}

// Publish releases a disclosure. Duplicate kind+period is rejected — a
// correction is a new period label, never an overwrite (WORM).
func (r *Registry) Publish(tenantID string, kind Kind, title, period, ref, sha, by string) (Publication, error) {
	if tenantID == "" || title == "" || period == "" || ref == "" {
		return Publication{}, errors.New("disclosure: tenant, title, period and ref required")
	}
	switch kind {
	case AuditedRSAFund, AuditedCompany, AnnualReport, QuarterlyReturns:
	default:
		return Publication{}, fmt.Errorf("disclosure: unsupported kind %s", kind)
	}
	r.mu.Lock()
	for _, p := range r.pubs {
		if p.TenantID == tenantID && p.Kind == kind && p.Period == period {
			r.mu.Unlock()
			return Publication{}, fmt.Errorf("disclosure: %s for %s already published (WORM — corrections need a new period label)", kind, period)
		}
	}
	r.seq++
	p := &Publication{
		ID: fmt.Sprintf("dis-%05d", r.seq), TenantID: tenantID, Kind: kind,
		Title: title, Period: period, Ref: ref, SHA256: sha,
		PublishedBy: by, PublishedAt: time.Now(),
	}
	r.pubs[p.ID] = p
	out := *p
	r.mu.Unlock()
	persist.Save(r.sink, "disclosure.pub", tenantID, out.ID, out)
	return out, nil
}

// List returns disclosures, optionally filtered by kind, newest period first.
func (r *Registry) List(tenantID string, kind Kind) []Publication {
	r.mu.Lock()
	var out []Publication
	for _, p := range r.pubs {
		if p.TenantID == tenantID && (kind == "" || p.Kind == kind) {
			out = append(out, *p)
		}
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Period > out[j].Period })
	return out
}
