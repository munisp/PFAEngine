// Package readiness computes an employer's workforce retirement-readiness
// index: a 0–100 composite of remittance timeliness, workforce coverage
// and contribution adequacy, with a letter grade. It gives employers a
// dashboard KPI and gives the PFA a retention/intervention signal —
// employers trending down get proactive outreach before PenCom penalties.
package readiness

import (
	"fmt"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/persist"
)

// Inputs are the measured signals for one employer over a period.
type Inputs struct {
	RemittancesOnTime int   `json:"remittances_on_time"` // count
	RemittancesTotal  int   `json:"remittances_total"`   // count
	EmployeesCovered  int   `json:"employees_covered"`   // with active RSA
	EmployeesTotal    int   `json:"employees_total"`
	AvgContribBps     int64 `json:"avg_contrib_bps"` // avg total rate, statutory min 1800
}

// Report is the computed index.
type Report struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	EmployerID string    `json:"employer_id"`
	Period     string    `json:"period"`     // YYYY-MM
	Timeliness int       `json:"timeliness"` // 0–100
	Coverage   int       `json:"coverage"`   // 0–100
	Adequacy   int       `json:"adequacy"`   // 0–100 (1800bps = 60, 3000bps = 100)
	Index      int       `json:"index"`      // weighted composite
	Grade      string    `json:"grade"`      // A ≥80, B ≥65, C ≥50, D <50
	ComputedAt time.Time `json:"computed_at"`
}

func pct(num, den int) int {
	if den <= 0 {
		return 0
	}
	return den100(den, num)
}

func den100(den, num int) int { return num * 100 / den }

func grade(idx int) string {
	switch {
	case idx >= 80:
		return "A"
	case idx >= 65:
		return "B"
	case idx >= 50:
		return "C"
	default:
		return "D"
	}
}

// Engine computes and stores reports.
type Engine struct {
	mu      sync.Mutex
	reports []Report
	seq     int
	sink    persist.Sink
}

func NewEngine() *Engine { return &Engine{} }

// Compute scores an employer for a period. Weights: timeliness 40%,
// coverage 30%, adequacy 30%.
func (e *Engine) Compute(tenantID, employerID, period string, in Inputs, at time.Time) Report {
	timeliness := pct(in.RemittancesOnTime, in.RemittancesTotal)
	coverage := pct(in.EmployeesCovered, in.EmployeesTotal)
	// Adequacy: 1800bps statutory minimum scores 60; ≥3000bps scores 100.
	adequacy := int(in.AvgContribBps) * 100 / 3000
	if adequacy > 100 {
		adequacy = 100
	}
	idx := timeliness*40/100 + coverage*30/100 + adequacy*30/100
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	r := Report{ID: fmt.Sprintf("rdy-%06d", e.seq), TenantID: tenantID, EmployerID: employerID,
		Period: period, Timeliness: timeliness, Coverage: coverage, Adequacy: adequacy,
		Index: idx, Grade: grade(idx), ComputedAt: at}
	e.reports = append(e.reports, r)
	persist.Save(e.sink, kindReport, tenantID, r.ID, r)
	return r
}

// Reports lists an employer's history, newest last.
func (e *Engine) Reports(tenantID, employerID string) []Report {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Report
	for _, r := range e.reports {
		if r.TenantID == tenantID && r.EmployerID == employerID {
			out = append(out, r)
		}
	}
	return out
}
