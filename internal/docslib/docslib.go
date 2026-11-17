// Package docslib implements the forms & downloads library every licensed
// PFA publishes on its website (Tranche 21): RSA registration forms,
// transfer forms, withdrawal/benefit forms, KYC document checklists,
// nomination forms, mortgage equity forms. Forms are versioned — a new
// upload of the same form code supersedes the old version, which stays
// retrievable for audit.
package docslib

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

// Category groups forms by member journey.
type Category string

const (
	CatRSARegistration Category = "RSA_REGISTRATION"
	CatTransfer        Category = "TRANSFER"
	CatWithdrawal      Category = "WITHDRAWAL"
	CatKYC             Category = "KYC"
	CatNomination      Category = "NOMINATION"
	CatMortgage        Category = "MORTGAGE"
	CatMicro           Category = "MICRO_PENSION"
	CatGuide           Category = "GUIDE"
)

// Form is one published downloadable document.
type Form struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Code        string    `json:"code"` // e.g. RSA-REG-01
	Title       string    `json:"title"`
	Category    Category  `json:"category"`
	Version     int       `json:"version"`
	Description string    `json:"description"`
	Ref         string    `json:"ref"` // object-storage URI of the PDF
	SHA256      string    `json:"sha256"`
	Active      bool      `json:"active"`
	PublishedAt time.Time `json:"published_at"`
}

// Library is the tenant-scoped form registry.
type Library struct {
	mu     sync.Mutex
	forms  map[string]*Form // id -> form
	seq    int
	byCode map[string][]string // tenantID|code -> version-ordered ids
	sink   persist.Sink        // nil in pure in-memory mode
}

// New returns an empty library seeded with the standard PenCom form set.
func New() *Library {
	return &Library{forms: map[string]*Form{}, byCode: map[string][]string{}}
}

// AttachSink wires write-through snapshot persistence (NFR-10).
func (l *Library) AttachSink(s persist.Sink) { l.sink = s }

// Restore rehydrates forms from boot snapshots; foreign kinds are ignored.
func (l *Library) Restore(snaps []persist.Snapshot) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range persist.OfKind(snaps, "docslib.form") {
		var f Form
		if err := json.Unmarshal(s.Payload, &f); err != nil {
			return err
		}
		fc := f
		l.forms[f.ID] = &fc
		k := f.TenantID + "|" + f.Code
		l.byCode[k] = append(l.byCode[k], f.ID)
		if n, err := strconv.Atoi(strings.TrimPrefix(f.ID, "frm-")); err == nil && n > l.seq {
			l.seq = n
		}
	}
	for k := range l.byCode {
		ids := l.byCode[k]
		sort.Slice(ids, func(i, j int) bool { return l.forms[ids[i]].Version < l.forms[ids[j]].Version })
	}
	return nil
}

// SeedStandard publishes the standard PenCom form catalogue for a tenant.
func (l *Library) SeedStandard(tenantID string) {
	std := []struct {
		code, title string
		cat         Category
		desc        string
	}{
		{"RSA-REG-01", "RSA Registration Form", CatRSARegistration, "Open a Retirement Savings Account (PRA 2014 s.11)"},
		{"RSA-TRF-01", "RSA Transfer Request Form", CatTransfer, "Move your RSA from another PFA (annual transfer window)"},
		{"WDR-RET-01", "Retirement Benefit Withdrawal Form", CatWithdrawal, "Programmed withdrawal / annuity application at retirement"},
		{"WDR-25P-01", "25% Temporary Withdrawal Form", CatWithdrawal, "25% lump-sum withdrawal on job loss (PRA 2014 s.89)"},
		{"WDR-MTG-01", "Mortgage Equity Contribution Form", CatMortgage, "25% RSA equity contribution for residential mortgage (PRA s.89(2))"},
		{"KYC-CHK-01", "KYC Documentation Checklist", CatKYC, "Required identity and address documents"},
		{"NOM-01", "Beneficiary Nomination Form", CatNomination, "Nominate or update next-of-kin beneficiaries"},
		{"MIC-REG-01", "Micro-Pension Enrolment Form", CatMicro, "Informal-sector enrolment under the micro-pension plan"},
		{"GDE-RET-01", "Retirement Planning Guide", CatGuide, "How to plan, invest and retire well"},
	}
	for _, f := range std {
		// Idempotent: boot seeding must not mint new versions after a
		// restore has already rehydrated the catalogue.
		if len(l.Versions(tenantID, f.code)) > 0 {
			continue
		}
		_, _ = l.Publish(tenantID, f.code, f.title, f.cat, f.desc, "seed://standard/"+f.code, "")
	}
}

// Publish registers a new version of a form; older versions deactivate.
func (l *Library) Publish(tenantID, code, title string, cat Category, desc, ref, sha string) (Form, error) {
	if tenantID == "" || code == "" || title == "" {
		return Form{}, errors.New("docslib: tenant, code and title required")
	}
	l.mu.Lock()
	k := tenantID + "|" + code
	ver := len(l.byCode[k]) + 1
	var changed []Form
	for _, id := range l.byCode[k] {
		l.forms[id].Active = false
		changed = append(changed, *l.forms[id])
	}
	l.seq++
	f := &Form{
		ID: fmt.Sprintf("frm-%05d", l.seq), TenantID: tenantID, Code: code,
		Title: title, Category: cat, Version: ver, Description: desc,
		Ref: ref, SHA256: sha, Active: true, PublishedAt: time.Now(),
	}
	l.forms[f.ID] = f
	l.byCode[k] = append(l.byCode[k], f.ID)
	changed = append(changed, *f)
	l.mu.Unlock()
	for _, c := range changed {
		persist.Save(l.sink, "docslib.form", tenantID, c.ID, c)
	}
	return *f, nil
}

// List returns active forms, optionally filtered by category.
func (l *Library) List(tenantID string, cat Category) []Form {
	l.mu.Lock()
	var out []Form
	for _, f := range l.forms {
		if f.TenantID != tenantID || !f.Active {
			continue
		}
		if cat != "" && f.Category != cat {
			continue
		}
		out = append(out, *f)
	}
	l.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// Get returns one form (any version, for audit).
func (l *Library) Get(tenantID, id string) (Form, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, ok := l.forms[id]
	if !ok || f.TenantID != tenantID {
		return Form{}, fmt.Errorf("docslib: form %s not found", id)
	}
	return *f, nil
}

// Versions returns all versions of a form code, oldest first.
func (l *Library) Versions(tenantID, code string) []Form {
	l.mu.Lock()
	ids := append([]string(nil), l.byCode[tenantID+"|"+code]...)
	var out []Form
	for _, id := range ids {
		out = append(out, *l.forms[id])
	}
	l.mu.Unlock()
	return out
}
