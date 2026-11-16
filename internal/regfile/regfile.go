// Package regfile implements the PenCom regulatory-filing calendar:
// recurring obligations (monthly contribution schedules, quarterly
// returns, annual audited statements) with due-date tracking,
// auto-generated submission payloads and a lateness penalty guard
// (2% per month on unremitted contributions per PRA s.11) so the PFA
// never misses a filing.
package regfile

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

// FilingKind classifies an obligation.
type FilingKind string

const (
	FilingMonthlySchedule FilingKind = "MONTHLY_SCHEDULE" // contribution schedule
	FilingQuarterlyReturn FilingKind = "QUARTERLY_RETURN"
	FilingAnnualStatement FilingKind = "ANNUAL_STATEMENT"
)

// Status is the filing lifecycle.
type Status string

const (
	StatusPending   Status = "PENDING"
	StatusGenerated Status = "GENERATED"
	StatusFiled     Status = "FILED"
	StatusLate      Status = "LATE"
)

var (
	ErrNoFiling     = errors.New("regfile: filing not found")
	ErrBadTransition = errors.New("regfile: invalid filing transition")
)

// Filing is one obligation instance.
type Filing struct {
	ID        string     `json:"id"`
	TenantID  string     `json:"tenant_id"`
	Kind      FilingKind `json:"kind"`
	Period    string     `json:"period"`   // YYYY-MM or YYYY-Qn
	DueDate   time.Time  `json:"due_date"`
	Status    Status     `json:"status"`
	Payload   string     `json:"payload"`  // generated return body
	FiledAt   *time.Time `json:"filed_at,omitempty"`
	Penalty   money.Kobo `json:"penalty_kobo"` // accrued lateness penalty
}

// PenaltyMonthlyBps is the PRA s.11 lateness penalty: 2% per month.
const PenaltyMonthlyBps = 200

// Engine manages the calendar.
type Engine struct {
	mu      sync.Mutex
	filings map[string]*Filing
	seq     int
}

func NewEngine() *Engine { return &Engine{filings: map[string]*Filing{}} }

// Schedule registers a filing obligation.
func (e *Engine) Schedule(tenantID string, kind FilingKind, period string, due time.Time) Filing {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	f := &Filing{ID: fmt.Sprintf("rf-%06d", e.seq), TenantID: tenantID, Kind: kind,
		Period: period, DueDate: due, Status: StatusPending}
	e.filings[tenantID+"|"+f.ID] = f
	return *f
}

// Generate produces the submission payload (PENDING/LATE → GENERATED).
func (e *Engine) Generate(tenantID, id, payload string, at time.Time) (Filing, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	f, ok := e.filings[tenantID+"|"+id]
	if !ok {
		return Filing{}, ErrNoFiling
	}
	if f.Status == StatusFiled {
		return Filing{}, ErrBadTransition
	}
	f.Payload = payload
	if at.After(f.DueDate) {
		f.Status = StatusLate
	} else {
		f.Status = StatusGenerated
	}
	return *f, nil
}

// File marks a filing submitted; late filings accrue the s.11 penalty on
// the outstanding amount (2% per started month late).
func (e *Engine) File(tenantID, id string, outstanding money.Kobo, at time.Time) (Filing, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	f, ok := e.filings[tenantID+"|"+id]
	if !ok {
		return Filing{}, ErrNoFiling
	}
	if f.Status == StatusPending || f.Status == StatusFiled {
		return Filing{}, ErrBadTransition
	}
	if at.After(f.DueDate) && outstanding > 0 {
		monthsLate := int64(at.Sub(f.DueDate).Hours()/(24*30)) + 1
		f.Penalty = outstanding * money.Kobo(PenaltyMonthlyBps*monthsLate) / 10000
	}
	f.Status = StatusFiled
	f.FiledAt = &at
	return *f, nil
}

// DueSoon lists non-filed filings due within the horizon.
func (e *Engine) DueSoon(tenantID string, at time.Time, horizon time.Duration) []Filing {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Filing
	for _, f := range e.filings {
		if f.TenantID == tenantID && f.Status != StatusFiled && f.DueDate.Sub(at) <= horizon {
			out = append(out, *f)
		}
	}
	return out
}

// List lists a tenant's filings.
func (e *Engine) List(tenantID string) []Filing {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Filing
	for _, f := range e.filings {
		if f.TenantID == tenantID {
			out = append(out, *f)
		}
	}
	return out
}
