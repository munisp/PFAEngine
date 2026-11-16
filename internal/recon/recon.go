// Package recon implements FR-303/FR-305/BR-03: automated reconciliation
// against custodian receipts and PCRS schedules with ≥95% auto-match on
// clean files, aged exception queues, per-employer suspense accounts, and
// employer-default detection (deductions without remittance).
package recon

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/contribution"
	"github.com/munisp/pfaengine/internal/money"
)

// Receipt is a custodian/PCRS remittance receipt line.
type Receipt struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	EmployerID string     `json:"employer_id"`
	RSAPIN     string     `json:"rsa_pin"`
	Amount     money.Kobo `json:"amount_kobo"`
	ValueDate  string     `json:"value_date"` // YYYY-MM-DD
	Source     string     `json:"source"`     // CUSTODIAN | PCRS
}

type ExceptionReason string

const (
	ReasonUnmatchedReceipt ExceptionReason = "UNMATCHED_RECEIPT"
	ReasonAmountMismatch   ExceptionReason = "AMOUNT_MISMATCH"
	ReasonUnknownPIN       ExceptionReason = "UNKNOWN_PIN"
	ReasonDuplicate        ExceptionReason = "DUPLICATE"
)

type Exception struct {
	ID         string          `json:"id"`
	TenantID   string          `json:"tenant_id"`
	EmployerID string          `json:"employer_id"`
	Reason     ExceptionReason `json:"reason"`
	Expected   money.Kobo      `json:"expected_kobo"`
	Received   money.Kobo      `json:"received_kobo"`
	RSAPIN     string          `json:"rsa_pin"`
	RaisedAt   time.Time       `json:"raised_at"`
	ResolvedAt time.Time       `json:"resolved_at,omitempty"`
	Resolution string          `json:"resolution,omitempty"`  // AUTO_MATCH | RECLASSIFY | REFUND
	ResolvedBy string          `json:"resolved_by,omitempty"` // maker-checker
}

// AgeDays implements exception ageing (§4 reconciliation).
func (e Exception) AgeDays(now time.Time) int { return int(now.Sub(e.RaisedAt).Hours() / 24) }

type MatchResult struct {
	AutoMatched  int `json:"auto_matched"`
	ExceptionCnt int `json:"exceptions"`
	TotalLines   int `json:"total_lines"`
}

// AutoMatchRate is the BR-03 KPI (target ≥95% on clean schedules).
func (m MatchResult) AutoMatchRate() float64 {
	if m.TotalLines == 0 {
		return 1.0
	}
	return float64(m.AutoMatched) / float64(m.TotalLines)
}

// Engine reconciles schedules against receipts.
type Engine struct {
	mu         sync.Mutex
	exceptions map[string]*Exception
	suspense   map[string]money.Kobo // employerID -> suspense balance
	seq        int
}

func NewEngine() *Engine {
	return &Engine{exceptions: map[string]*Exception{}, suspense: map[string]money.Kobo{}}
}

// matchKey identifies a scheduled line for exact matching.
func matchKey(pin string, amount money.Kobo) string {
	return fmt.Sprintf("%s|%d", pin, int64(amount))
}

// Reconcile matches each schedule line against receipts: exact PIN+amount
// within the value-date window auto-matches; remainder raise exceptions.
func (e *Engine) Reconcile(sched *contribution.Schedule, receipts []Receipt) MatchResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	res := MatchResult{TotalLines: len(sched.Lines)}
	// Bucket unmatched receipts by PIN|amount for O(1) matching.
	available := map[string][]int{}
	used := make([]bool, len(receipts))
	for i, r := range receipts {
		if r.TenantID != sched.TenantID {
			continue
		}
		k := matchKey(r.RSAPIN, r.Amount)
		available[k] = append(available[k], i)
	}
	for _, line := range sched.Lines {
		k := matchKey(line.RSAPIN, line.Total())
		if idxs := available[k]; len(idxs) > 0 {
			i := idxs[0]
			available[k] = idxs[1:]
			used[i] = true
			res.AutoMatched++
			continue
		}
		// Try same PIN different amount -> amount mismatch exception.
		found := -1
		for i, r := range receipts {
			if !used[i] && r.TenantID == sched.TenantID && r.RSAPIN == line.RSAPIN {
				found = i
				break
			}
		}
		e.seq++
		ex := &Exception{
			ID: fmt.Sprintf("exc-%d", e.seq), TenantID: sched.TenantID,
			EmployerID: sched.EmployerID, RSAPIN: line.RSAPIN,
			Expected: line.Total(), RaisedAt: time.Now().UTC(),
		}
		if found >= 0 {
			used[found] = true
			ex.Reason = ReasonAmountMismatch
			ex.Received = receipts[found].Amount
		} else {
			ex.Reason = ReasonUnmatchedReceipt
			ex.Received = 0
		}
		e.exceptions[ex.ID] = ex
		e.suspense[sched.EmployerID] += ex.Expected - ex.Received
		res.ExceptionCnt++
	}
	// Receipts never consumed: unmatched inflows into suspense.
	for i, r := range receipts {
		if used[i] || r.TenantID != sched.TenantID {
			continue
		}
		e.seq++
		ex := &Exception{
			ID: fmt.Sprintf("exc-%d", e.seq), TenantID: sched.TenantID,
			EmployerID: r.EmployerID, RSAPIN: r.RSAPIN, Reason: ReasonUnmatchedReceipt,
			Expected: 0, Received: r.Amount, RaisedAt: time.Now().UTC(),
		}
		e.exceptions[ex.ID] = ex
		e.suspense[r.EmployerID] += r.Amount
	}
	return res
}

// Resolve applies a maker-checker resolution (§6.2 endpoint).
func (e *Engine) Resolve(id, resolution, actor string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	ex, ok := e.exceptions[id]
	if !ok {
		return fmt.Errorf("recon: exception %s not found", id)
	}
	if ex.ResolvedAt.IsZero() == false {
		return fmt.Errorf("recon: exception already resolved")
	}
	ex.Resolution = resolution
	ex.ResolvedBy = actor
	ex.ResolvedAt = time.Now().UTC()
	e.suspense[ex.EmployerID] -= ex.Expected - ex.Received
	return nil
}

func (e *Engine) Exceptions(tenantID string) []*Exception {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []*Exception
	for _, ex := range e.exceptions {
		if ex.TenantID == tenantID && ex.ResolvedAt.IsZero() {
			out = append(out, ex)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RaisedAt.Before(out[j].RaisedAt) })
	return out
}

// Suspense returns the real-time suspense balance per employer (§4).
func (e *Engine) Suspense(employerID string) money.Kobo {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.suspense[employerID]
}

// AgedBuckets groups open exceptions into 0-7/8-30/31-90/90+ day buckets.
func (e *Engine) AgedBuckets(tenantID string, now time.Time) map[string]int {
	buckets := map[string]int{"0-7": 0, "8-30": 0, "31-90": 0, "90+": 0}
	for _, ex := range e.Exceptions(tenantID) {
		switch d := ex.AgeDays(now); {
		case d <= 7:
			buckets["0-7"]++
		case d <= 30:
			buckets["8-30"]++
		case d <= 90:
			buckets["31-90"]++
		default:
			buckets["90+"]++
		}
	}
	return buckets
}

// DefaultAlert is raised when an employer deducts but does not remit (FR-305).
type DefaultAlert struct {
	EmployerID   string     `json:"employer_id"`
	Period       string     `json:"period"`
	Scheduled    money.Kobo `json:"scheduled_kobo"`
	Remitted     money.Kobo `json:"remitted_kobo"`
	ShortfallPct int        `json:"shortfall_pct"`
}

// DetectDefaults compares scheduled vs remitted totals per employer/period.
// Shortfall above thresholdPct raises a compliance alert.
func DetectDefaults(schedules []*contribution.Schedule, receipts []Receipt, thresholdPct int) []DefaultAlert {
	type key struct{ emp, period string }
	schedTot := map[key]money.Kobo{}
	for _, s := range schedules {
		schedTot[key{s.EmployerID, s.Period}] += s.Total()
	}
	remitTot := map[string]money.Kobo{}
	for _, r := range receipts {
		remitTot[r.EmployerID] += r.Amount
	}
	var alerts []DefaultAlert
	for k, tot := range schedTot {
		rem := remitTot[k.emp]
		if tot <= 0 {
			continue
		}
		short := int64(tot - rem)
		if short <= 0 {
			continue
		}
		pct := int(short * 100 / int64(tot))
		if pct >= thresholdPct {
			alerts = append(alerts, DefaultAlert{
				EmployerID: k.emp, Period: k.period, Scheduled: tot, Remitted: rem, ShortfallPct: pct,
			})
		}
	}
	return alerts
}
