package payroll

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"github.com/munisp/pfaengine/internal/money"
)

// PSSP remittance-schedule CSV layouts used in the Nigerian pensions
// industry. Amounts are NAIRA with 2 decimal places in the file; they are
// converted to integer kobo on parse and back to naira on write, so a
// write -> parse round-trip preserves kobo totals exactly.
//
// Pensphere layout (one row per employee):
//
//	EMPLOYER_CODE,PERIOD,PIN,SURNAME,FIRST_NAME,MONTHLY_SALARY,EMPLOYEE_CONTRIB,EMPLOYER_CONTRIB,TOTAL
//
// Interswitch Pension Central layout (one row per employee):
//
//	SCHEDULE_ID,EMPLOYER,PERIOD,STAFF_ID,RSA_PIN,NAME,BASIC,HOUSING,TRANSPORT,PENSION_EE,PENSION_ER
//
// The Contribution type does not carry names, monthly salary or the
// basic/housing/transport split, so the writers reconstruct what they can:
// MONTHLY_SALARY (Pensphere) and BASIC (Pension Central, with HOUSING and
// TRANSPORT written as 0.00) are derived from the 8% employee share via
// derivePensionBase, and the SURNAME/FIRST_NAME columns are written blank.
// Pension columns are always recomputed with PensionSplit on write, never
// copied blindly.

var pensphereHeader = []string{
	"EMPLOYER_CODE", "PERIOD", "PIN", "SURNAME", "FIRST_NAME",
	"MONTHLY_SALARY", "EMPLOYEE_CONTRIB", "EMPLOYER_CONTRIB", "TOTAL",
}

var pensionCentralHeader = []string{
	"SCHEDULE_ID", "EMPLOYER", "PERIOD", "STAFF_ID", "RSA_PIN", "NAME",
	"BASIC", "HOUSING", "TRANSPORT", "PENSION_EE", "PENSION_ER",
}

func checkHeader(format string, got, want []string) error {
	if len(got) != len(want) {
		return fmt.Errorf("payroll: %s: header has %d columns, want %d", format, len(got), len(want))
	}
	for i := range want {
		if !strings.EqualFold(strings.TrimSpace(got[i]), want[i]) {
			return fmt.Errorf("payroll: %s: header column %d is %q, want %q", format, i+1, got[i], want[i])
		}
	}
	return nil
}

// parseNaira parses a naira amount cell into kobo with a line-numbered error.
func parseNaira(format string, line int, col, val string) (money.Kobo, error) {
	k, err := money.ParseKobo(val)
	if err != nil {
		return 0, fmt.Errorf("payroll: %s: line %d: %s: invalid amount %q", format, line, col, val)
	}
	return k, nil
}

// FormatPensphere writes s in the Pensphere CSV layout. Amounts are
// written in naira with 2dp; TOTAL is the employee + employer share.
func FormatPensphere(w io.Writer, s Schedule) error {
	if err := s.Validate(); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(pensphereHeader); err != nil {
		return fmt.Errorf("payroll: pensphere: write header: %w", err)
	}
	for i, l := range s.Lines {
		if l.Total() <= 0 {
			return fmt.Errorf("payroll: pensphere: line %d (PIN %q): %w", i+2, l.RSA_PIN, ErrNonPositiveTotal)
		}
		row := []string{
			s.EmployerID,
			s.Period,
			l.RSA_PIN,
			"",                                    // SURNAME: not modelled on Contribution
			"",                                    // FIRST_NAME: not modelled on Contribution
			derivePensionBase(l.Employee).Naira(), // MONTHLY_SALARY (derived)
			l.Employee.Naira(),
			l.Employer.Naira(),
			l.Total().Naira(),
		}
		if err := cw.Write(row); err != nil {
			return fmt.Errorf("payroll: pensphere: write line %d: %w", i+2, err)
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("payroll: pensphere: flush: %w", err)
	}
	return nil
}

// ParsePensphere reads a Pensphere CSV file into a Schedule. The schedule
// ID is synthesised as "<EMPLOYER_CODE>/<PERIOD>". Malformed rows produce
// line-numbered errors (header is line 1); negative amounts, a TOTAL that
// does not equal EMPLOYEE_CONTRIB+EMPLOYER_CONTRIB, and zero totals are all
// rejected.
func ParsePensphere(r io.Reader) (Schedule, error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = len(pensphereHeader)
	header, err := cr.Read()
	if err != nil {
		return Schedule{}, fmt.Errorf("payroll: pensphere: line 1: header: %w", err)
	}
	if err := checkHeader("pensphere", header, pensphereHeader); err != nil {
		return Schedule{}, err
	}

	var s Schedule
	line := 1
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			return Schedule{}, fmt.Errorf("payroll: pensphere: line %d: %w", line, err)
		}
		employer := strings.TrimSpace(rec[0])
		period := strings.TrimSpace(rec[1])
		pin := strings.TrimSpace(rec[2])
		if pin == "" {
			return Schedule{}, fmt.Errorf("payroll: pensphere: line %d: PIN is empty", line)
		}
		if err := ValidatePeriod(period); err != nil {
			return Schedule{}, fmt.Errorf("payroll: pensphere: line %d: %w", line, err)
		}
		salary, err := parseNaira("pensphere", line, "MONTHLY_SALARY", rec[5])
		if err != nil {
			return Schedule{}, err
		}
		if salary < 0 {
			return Schedule{}, fmt.Errorf("payroll: pensphere: line %d: MONTHLY_SALARY must not be negative", line)
		}
		ee, err := parseNaira("pensphere", line, "EMPLOYEE_CONTRIB", rec[6])
		if err != nil {
			return Schedule{}, err
		}
		er, err := parseNaira("pensphere", line, "EMPLOYER_CONTRIB", rec[7])
		if err != nil {
			return Schedule{}, err
		}
		tot, err := parseNaira("pensphere", line, "TOTAL", rec[8])
		if err != nil {
			return Schedule{}, err
		}
		if ee < 0 || er < 0 {
			return Schedule{}, fmt.Errorf("payroll: pensphere: line %d: contribution amounts must not be negative", line)
		}
		if tot != ee+er {
			return Schedule{}, fmt.Errorf("payroll: pensphere: line %d: TOTAL %s != EMPLOYEE_CONTRIB+EMPLOYER_CONTRIB %s", line, tot.Naira(), (ee + er).Naira())
		}
		if tot <= 0 {
			return Schedule{}, fmt.Errorf("payroll: pensphere: line %d: %w", line, ErrNonPositiveTotal)
		}
		if s.EmployerID == "" {
			s.EmployerID = employer
			s.Period = period
		}
		s.Lines = append(s.Lines, Contribution{
			EmployeeID: pin,
			RSA_PIN:    pin,
			Employee:   ee,
			Employer:   er,
			Period:     period,
		})
	}
	if len(s.Lines) == 0 {
		return Schedule{}, fmt.Errorf("payroll: pensphere: %w", ErrEmptySchedule)
	}
	s.ID = s.EmployerID + "/" + s.Period
	return s, nil
}

// FormatPensionCentral writes s in the Interswitch Pension Central CSV
// layout. BASIC is derived from the 8% employee share (HOUSING and
// TRANSPORT written as 0.00), and PENSION_EE / PENSION_ER are computed as
// 8% / 10% of BASIC+HOUSING+TRANSPORT via PensionSplit.
func FormatPensionCentral(w io.Writer, s Schedule) error {
	if err := s.Validate(); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(pensionCentralHeader); err != nil {
		return fmt.Errorf("payroll: pension-central: write header: %w", err)
	}
	for i, l := range s.Lines {
		if l.Total() <= 0 {
			return fmt.Errorf("payroll: pension-central: line %d (RSA_PIN %q): %w", i+2, l.RSA_PIN, ErrNonPositiveTotal)
		}
		basic := derivePensionBase(l.Employee)
		ee, er := PensionSplit(basic)
		row := []string{
			s.ID,
			s.EmployerID,
			s.Period,
			l.EmployeeID,
			l.RSA_PIN,
			"", // NAME: not modelled on Contribution
			basic.Naira(),
			money.Kobo(0).Naira(), // HOUSING
			money.Kobo(0).Naira(), // TRANSPORT
			ee.Naira(),
			er.Naira(),
		}
		if err := cw.Write(row); err != nil {
			return fmt.Errorf("payroll: pension-central: write line %d: %w", i+2, err)
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("payroll: pension-central: flush: %w", err)
	}
	return nil
}

// ParsePensionCentral reads a Pension Central CSV file into a Schedule.
// Unlike the writer, the parser trusts the PENSION_EE / PENSION_ER columns
// as given (the BASIC/HOUSING/TRANSPORT columns are validated but not
// re-derived). Malformed rows produce line-numbered errors (header is
// line 1); negative amounts and zero totals are rejected.
func ParsePensionCentral(r io.Reader) (Schedule, error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = len(pensionCentralHeader)
	header, err := cr.Read()
	if err != nil {
		return Schedule{}, fmt.Errorf("payroll: pension-central: line 1: header: %w", err)
	}
	if err := checkHeader("pension-central", header, pensionCentralHeader); err != nil {
		return Schedule{}, err
	}

	var s Schedule
	line := 1
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			return Schedule{}, fmt.Errorf("payroll: pension-central: line %d: %w", line, err)
		}
		period := strings.TrimSpace(rec[2])
		staffID := strings.TrimSpace(rec[3])
		pin := strings.TrimSpace(rec[4])
		if pin == "" {
			return Schedule{}, fmt.Errorf("payroll: pension-central: line %d: RSA_PIN is empty", line)
		}
		if err := ValidatePeriod(period); err != nil {
			return Schedule{}, fmt.Errorf("payroll: pension-central: line %d: %w", line, err)
		}
		// Validate the pensionable component columns even though the
		// parser trusts the pension columns.
		for i, col := range []string{"BASIC", "HOUSING", "TRANSPORT"} {
			v, err := parseNaira("pension-central", line, col, rec[6+i])
			if err != nil {
				return Schedule{}, err
			}
			if v < 0 {
				return Schedule{}, fmt.Errorf("payroll: pension-central: line %d: %s must not be negative", line, col)
			}
		}
		ee, err := parseNaira("pension-central", line, "PENSION_EE", rec[9])
		if err != nil {
			return Schedule{}, err
		}
		er, err := parseNaira("pension-central", line, "PENSION_ER", rec[10])
		if err != nil {
			return Schedule{}, err
		}
		if ee < 0 || er < 0 {
			return Schedule{}, fmt.Errorf("payroll: pension-central: line %d: contribution amounts must not be negative", line)
		}
		if ee+er <= 0 {
			return Schedule{}, fmt.Errorf("payroll: pension-central: line %d: %w", line, ErrNonPositiveTotal)
		}
		if s.ID == "" {
			s.ID = strings.TrimSpace(rec[0])
			s.EmployerID = strings.TrimSpace(rec[1])
			s.Period = period
		}
		s.Lines = append(s.Lines, Contribution{
			EmployeeID: staffID,
			RSA_PIN:    pin,
			Employee:   ee,
			Employer:   er,
			Period:     period,
		})
	}
	if len(s.Lines) == 0 {
		return Schedule{}, fmt.Errorf("payroll: pension-central: %w", ErrEmptySchedule)
	}
	return s, nil
}
