// Package payroll implements payroll-provider integration depth: first-class
// sync with ERPNext / Frappe HR over its REST API, plus Nigerian PSSP
// (Pension Service Support Provider) remittance-schedule file formats in the
// Pensphere and Interswitch Pension Central style CSV layouts.
//
// All money is integer kobo (internal/money); no floats anywhere. The
// statutory split follows the Pension Reform Act 2014 s.4(1): employee 8%,
// employer 10% of the pensionable base (basic + housing + transport).
package payroll

import (
	"errors"
	"fmt"

	"github.com/munisp/pfaengine/internal/money"
)

var (
	// ErrInvalidPeriod is returned when a period is not YYYY-MM.
	ErrInvalidPeriod = errors.New("payroll: period must be YYYY-MM")
	// ErrEmptySchedule is returned when a schedule has no contribution lines.
	ErrEmptySchedule = errors.New("payroll: schedule has no contribution lines")
	// ErrNonPositiveTotal is returned when a contribution total is zero or negative.
	ErrNonPositiveTotal = errors.New("payroll: contribution total must be positive")
)

// Employee is a staff record synchronised from a payroll provider.
type Employee struct {
	ID            string     `json:"id"`
	EmployerID    string     `json:"employer_id"`
	Name          string     `json:"name"`
	RSA_PIN       string     `json:"rsa_pin"`
	MonthlySalary money.Kobo `json:"monthly_salary"`
}

// Contribution is one employee's pension contribution for a period.
// Employee is the 8% employee share, Employer the 10% employer share.
type Contribution struct {
	EmployeeID string     `json:"employee_id"`
	RSA_PIN    string     `json:"rsa_pin"`
	Employee   money.Kobo `json:"employee"`
	Employer   money.Kobo `json:"employer"`
	Period     string     `json:"period"` // YYYY-MM
}

// Total returns the employee plus employer share.
func (c Contribution) Total() money.Kobo { return c.Employee + c.Employer }

// Schedule is a remittance schedule for one employer and one period.
type Schedule struct {
	ID         string         `json:"id"`
	TenantID   string         `json:"tenant_id"`
	EmployerID string         `json:"employer_id"`
	Period     string         `json:"period"` // YYYY-MM
	Lines      []Contribution `json:"lines"`
}

// Total sums every line (employee + employer shares).
func (s Schedule) Total() money.Kobo {
	var t money.Kobo
	for _, l := range s.Lines {
		t += l.Total()
	}
	return t
}

// Validate checks period shape, non-empty lines and a positive grand total.
func (s Schedule) Validate() error {
	if err := ValidatePeriod(s.Period); err != nil {
		return err
	}
	if len(s.Lines) == 0 {
		return ErrEmptySchedule
	}
	if s.Total() <= 0 {
		return ErrNonPositiveTotal
	}
	return nil
}

// ValidatePeriod checks the YYYY-MM period shape, including month range.
func ValidatePeriod(p string) error {
	if len(p) != 7 || p[4] != '-' {
		return fmt.Errorf("%w (got %q)", ErrInvalidPeriod, p)
	}
	for i := 0; i < len(p); i++ {
		if i == 4 {
			continue
		}
		if p[i] < '0' || p[i] > '9' {
			return fmt.Errorf("%w (got %q)", ErrInvalidPeriod, p)
		}
	}
	mo := int(p[5]-'0')*10 + int(p[6]-'0')
	if mo < 1 || mo > 12 {
		return fmt.Errorf("%w (month %02d out of range)", ErrInvalidPeriod, mo)
	}
	return nil
}

// PensionSplit computes the statutory 8% employee / 10% employer shares of
// the pensionable base (basic + housing + transport per PRA 2014), rounding
// half up to the nearest kobo. Integer math only — never floats.
func PensionSplit(base money.Kobo) (employee, employer money.Kobo) {
	employee = money.Kobo((int64(base)*8 + 50) / 100)
	employer = money.Kobo((int64(base)*10 + 50) / 100)
	return employee, employer
}

// derivePensionBase reverse-computes the pensionable base from an 8%
// employee share, rounding half up. It is exact whenever the employee share
// was itself produced by PensionSplit from a kobo-exact base whose product
// with 8 divides evenly by 100; otherwise it may differ from the true base
// by a kobo. Used by the PSSP writers, whose layouts carry salary/base
// columns that the Contribution type does not model.
func derivePensionBase(employee money.Kobo) money.Kobo {
	return money.Kobo((int64(employee)*100 + 4) / 8)
}
