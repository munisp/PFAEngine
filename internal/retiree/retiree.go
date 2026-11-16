// Package retiree implements FR-403 / FR-108 / BR-02: retiree register,
// periodic pension payroll, and the "I Am Alive" annual liveness campaign
// with 12-month + 3-month grace scheduling and suspension workflow.
package retiree

import (
	"fmt"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

type Retiree struct {
	RSAID           string     `json:"rsa_id"`
	TenantID        string     `json:"tenant_id"`
	MonthlyPension  money.Kobo `json:"monthly_pension_kobo"`
	BankRef         string     `json:"bank_ref"`
	Status          string     `json:"status"` // ACTIVE | GRACE | SUSPENDED
	LastLivenessAt  time.Time  `json:"last_liveness_at"`
	NextLivenessDue time.Time  `json:"next_liveness_due"`
}

type PayrollRun struct {
	ID        string     `json:"id"`
	TenantID  string     `json:"tenant_id"`
	Period    string     `json:"period"` // YYYY-MM
	Total     money.Kobo `json:"total_kobo"`
	Count     int        `json:"count"`
	CreatedAt time.Time  `json:"created_at"`
}

type Register struct {
	mu       sync.Mutex
	retirees map[string]*Retiree
	runs     []PayrollRun
	seq      int
}

func NewRegister() *Register { return &Register{retirees: map[string]*Retiree{}} }

func (r *Register) Enroll(tenantID, rsaID string, monthly money.Kobo, bankRef string, verifiedAt time.Time) *Retiree {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt := &Retiree{
		RSAID: rsaID, TenantID: tenantID, MonthlyPension: monthly, BankRef: bankRef,
		Status: "ACTIVE", LastLivenessAt: verifiedAt,
		NextLivenessDue: verifiedAt.AddDate(1, 0, 0), // 12-month cycle (FR-108)
	}
	r.retirees[rsaID] = rt
	return rt
}

// RecordLiveness resets the 12-month clock on a successful check.
func (r *Register) RecordLiveness(rsaID string, at time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.retirees[rsaID]
	if !ok {
		return false
	}
	rt.LastLivenessAt = at
	rt.NextLivenessDue = at.AddDate(1, 0, 0)
	rt.Status = "ACTIVE"
	return true
}

// Campaign advances statuses: past due+3mo grace → SUSPENDED (FR-108).
// Returns (dueSoon, suspended) RSA IDs for notification and payroll gating.
func (r *Register) Campaign(tenantID string, now time.Time) (dueSoon, suspended []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rt := range r.retirees {
		if rt.TenantID != tenantID {
			continue
		}
		graceEnd := rt.NextLivenessDue.AddDate(0, 3, 0)
		switch {
		case now.After(graceEnd):
			if rt.Status != "SUSPENDED" {
				rt.Status = "SUSPENDED"
				suspended = append(suspended, rt.RSAID)
			}
		case now.After(rt.NextLivenessDue):
			rt.Status = "GRACE"
			dueSoon = append(dueSoon, rt.RSAID)
		}
	}
	return dueSoon, suspended
}

// RunPayroll generates the periodic payment schedule, skipping suspended
// retirees (liveness-linked suspension, FR-403).
func (r *Register) RunPayroll(tenantID, period string, now time.Time) PayrollRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	run := PayrollRun{
		TenantID: tenantID, Period: period, CreatedAt: now,
	}
	r.seq++
	run.ID = fmt.Sprintf("pay-%d", r.seq)
	for _, rt := range r.retirees {
		if rt.TenantID == tenantID && rt.Status == "ACTIVE" {
			run.Total += rt.MonthlyPension
			run.Count++
		}
	}
	r.runs = append(r.runs, run)
	return run
}

func (r *Register) Get(rsaID string) (*Retiree, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.retirees[rsaID]
	return rt, ok
}
