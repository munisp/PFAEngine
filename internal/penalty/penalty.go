// Package penalty implements FR-501 / BR-11 / BR-12: late-remittance
// penalties under PRA 2014 s.11(3),(6),(7). Employers must remit pension
// contributions within 7 working days of salary payment; unpaid
// contributions attract a penalty of at least 2% per month or part
// thereof, recoverable as debt and credited to the employee's RSA.
// BR-12 (PRA s.69(d)): employers whose remittance remains due more than
// 14 calendar days past the deadline must be reported to PenCom.
// All amounts are integer kobo (DA-03); rates are integer basis points.
package penalty

import (
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

// Defaults mirroring rules://ng/pencom/ops-2025@v2 (NFR-22: the rules
// pack is the source of truth; these are the compiled-in fallbacks).
const (
	DefaultRemittanceWorkingDays = 7   // PRA 2014 s.11(1)
	DefaultPenaltyBpsPerMonth    = 200 // >=2% per month or part thereof
	DefaultPenComReportDays      = 14  // PRA 2014 s.69(d)
)

// WorkingDayDeadline returns the date n working days (Mon–Fri) after
// salaryPaid, skipping Saturdays and Sundays.
func WorkingDayDeadline(salaryPaid time.Time, n int) time.Time {
	d := salaryPaid
	for added := 0; added < n; {
		d = d.AddDate(0, 0, 1)
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			added++
		}
	}
	return d
}

// Arrears is one employer's overdue remittance position.
type Arrears struct {
	EmployerID string     `json:"employer_id"`
	Principal  money.Kobo `json:"principal_kobo"`
	SalaryPaid time.Time  `json:"salary_paid"`
	AsOf       time.Time  `json:"as_of"`
}

// Deadline is the remittance due date: 7 working days after salary payment.
func (a Arrears) Deadline() time.Time {
	return WorkingDayDeadline(a.SalaryPaid, DefaultRemittanceWorkingDays)
}

// MonthsOverdue returns the number of months or part thereof that the
// remittance is past its 7-working-day deadline (0 if not overdue).
func (a Arrears) MonthsOverdue() int {
	dl := a.Deadline()
	if !a.AsOf.After(dl) {
		return 0
	}
	months := 0
	d := dl
	for !d.AddDate(0, 1, 0).After(a.AsOf) {
		d = d.AddDate(0, 1, 0)
		months++
	}
	if d.Before(a.AsOf) {
		months++ // part thereof counts as a full month (PRA s.11(6))
	}
	return months
}

// Penalty computes the late-remittance penalty: principal × rateBps/10000
// per month or part thereof overdue (2% = 200 bps).
func (a Arrears) Penalty(rateBps int64) money.Kobo {
	m := a.MonthsOverdue()
	if m == 0 {
		return 0
	}
	return money.MulKobo(a.Principal, rateBps*int64(m), 10000)
}

// PenComReportable reports whether the arrears must be reported to PenCom:
// the remittance remains due more than 14 calendar days past the deadline.
func PenComReportable(a Arrears) bool {
	return a.AsOf.After(a.Deadline().AddDate(0, 0, DefaultPenComReportDays))
}
