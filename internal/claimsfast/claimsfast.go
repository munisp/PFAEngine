// Package claimsfast implements the 48-hour straight-through benefit
// engine: it scores a retiree's claim pack for completeness (KYC,
// documents, employer certification, bank verification) and flags claims
// eligible for straight-through processing — the difference between a
// 48-hour payout and a 6-week queue.
package claimsfast

import (
	"fmt"
	"sync"
	"time"
)

// PackItem is one completeness check on a claim pack.
type PackItem struct {
	Key   string `json:"key"`   // KYC | DOCS | EMPLOYER_CERT | BANK_VERIFIED | MPG_SCREEN
	Label string `json:"label"`
	Done  bool   `json:"done"`
}

// Pack is a retiree benefit claim pack.
type Pack struct {
	ID        string     `json:"id"`
	TenantID  string     `json:"tenant_id"`
	RSAID     string     `json:"rsa_id"`
	Items     []PackItem `json:"items"`
	Score     int        `json:"score"` // 0–100
	STP       bool       `json:"stp"`   // straight-through eligible
	Missing   []string   `json:"missing"`
	CreatedAt time.Time  `json:"created_at"`
}

// STPThreshold is the score at/above which a claim goes straight-through.
const STPThreshold = 100

var defaultItems = []PackItem{
	{Key: "KYC", Label: "Identity verified (BVN/NIN)"},
	{Key: "DOCS", Label: "Retirement documents complete"},
	{Key: "EMPLOYER_CERT", Label: "Employer separation certificate"},
	{Key: "BANK_VERIFIED", Label: "Bank account verified"},
	{Key: "MPG_SCREEN", Label: "MPG eligibility screened"},
}

// Engine holds claim packs.
type Engine struct {
	mu    sync.Mutex
	packs map[string]*Pack // tenantID|rsaID
	seq   int
}

func NewEngine() *Engine { return &Engine{packs: map[string]*Pack{}} }

func score(items []PackItem) (int, []string) {
	done := 0
	var missing []string
	for _, it := range items {
		if it.Done {
			done++
		} else {
			missing = append(missing, it.Key)
		}
	}
	return done * 100 / len(items), missing
}

// Open creates a pack for a retiree with all items pending.
func (e *Engine) Open(tenantID, rsaID string, at time.Time) Pack {
	e.mu.Lock()
	defer e.mu.Unlock()
	key := tenantID + "|" + rsaID
	if p, ok := e.packs[key]; ok {
		return *p
	}
	e.seq++
	items := make([]PackItem, len(defaultItems))
	copy(items, defaultItems)
	s, missing := score(items)
	p := &Pack{ID: fmt.Sprintf("cf-%06d", e.seq), TenantID: tenantID, RSAID: rsaID,
		Items: items, Score: s, Missing: missing, CreatedAt: at}
	e.packs[key] = p
	return *p
}

// Complete marks one item done and re-scores.
func (e *Engine) Complete(tenantID, rsaID, key string) (Pack, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.packs[tenantID+"|"+rsaID]
	if !ok {
		return Pack{}, fmt.Errorf("claimsfast: no pack for RSA %s", rsaID)
	}
	found := false
	for i := range p.Items {
		if p.Items[i].Key == key {
			p.Items[i].Done = true
			found = true
		}
	}
	if !found {
		return Pack{}, fmt.Errorf("claimsfast: unknown pack item %s", key)
	}
	p.Score, p.Missing = score(p.Items)
	p.STP = p.Score >= STPThreshold
	return *p, nil
}

// Get fetches a pack.
func (e *Engine) Get(tenantID, rsaID string) (Pack, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.packs[tenantID+"|"+rsaID]
	if !ok {
		return Pack{}, false
	}
	return *p, true
}
