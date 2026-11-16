// Package contribution implements FR-301/FR-203: employer schedule
// ingestion (CSV/JSON), 8%/10% employee/employer split, voluntary and PPP
// micro-contributions, and posting into the ledger.
package contribution

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var ErrValidation = errors.New("contribution: schedule validation failed")

// Split rates per PRA 2014 s4 (rules://ng/pencom/contrib-8-10).
const (
	EmployeeRatePct = 8
	EmployerRatePct = 10
)

type Line struct {
	RSAPIN    string     `json:"rsa_pin"`
	MemberRef string     `json:"member_ref,omitempty"`
	Gross     money.Kobo `json:"gross_kobo"` // monthly gross used for split
	Employee  money.Kobo `json:"employee_kobo"`
	Employer  money.Kobo `json:"employer_kobo"`
	Voluntary money.Kobo `json:"voluntary_kobo"`
	IsPPP     bool       `json:"is_ppp"` // Personal Pension Plan micro-contribution (BR-06)
}

func (l Line) Total() money.Kobo { return l.Employee + l.Employer + l.Voluntary }

// SplitLine computes the 8%/10% split from gross salary (FR-301).
func SplitLine(pin string, gross money.Kobo, voluntary money.Kobo, isPPP bool) Line {
	return Line{
		RSAPIN: pin, Gross: gross,
		Employee:  money.MulKobo(gross, EmployeeRatePct, 100),
		Employer:  money.MulKobo(gross, EmployerRatePct, 100),
		Voluntary: voluntary, IsPPP: isPPP,
	}
}

type ScheduleStatus string

const (
	SchedValidated ScheduleStatus = "VALIDATED"
	SchedPosted    ScheduleStatus = "POSTED"
	SchedFailed    ScheduleStatus = "FAILED"
)

type Schedule struct {
	ID           string         `json:"id"`
	TenantID     string         `json:"tenant_id"`
	EmployerID   string         `json:"employer_id"`
	Period       string         `json:"period"` // YYYY-MM
	Lines        []Line         `json:"lines"`
	Status       ScheduleStatus `json:"status"`
	UploadedAt   time.Time      `json:"uploaded_at"`
	PostedAt     time.Time      `json:"posted_at,omitempty"`
	ErrorSummary string         `json:"error_summary,omitempty"`
}

func (s *Schedule) Total() money.Kobo {
	var t money.Kobo
	for _, l := range s.Lines {
		t += l.Total()
	}
	return t
}

// ParseCSV ingests an employer schedule file (FR-203):
// rsa_pin,gross_kobo[,voluntary_kobo[,is_ppp]]
func ParseCSV(tenantID, employerID, period string, r io.Reader, validatePIN func(string) bool) (*Schedule, error) {
	rd := csv.NewReader(r)
	rd.TrimLeadingSpace = true
	rows, err := rd.ReadAll()
	if err != nil {
		return nil, err
	}
	s := &Schedule{TenantID: tenantID, EmployerID: employerID, Period: period, Status: SchedValidated, UploadedAt: time.Now().UTC()}
	var errs []string
	for i, row := range rows {
		if i == 0 && strings.EqualFold(strings.TrimSpace(row[0]), "rsa_pin") {
			continue // header
		}
		if len(row) < 2 {
			errs = append(errs, fmt.Sprintf("row %d: too few columns", i+1))
			continue
		}
		pin := strings.TrimSpace(row[0])
		if !validatePIN(pin) {
			errs = append(errs, fmt.Sprintf("row %d: RSA PIN %s failed PenCom validation", i+1, pin))
			continue
		}
		// Gross column is integer kobo; decimal strings are parsed as naira.
		var gross money.Kobo
		if g, perr := strconv.ParseInt(strings.TrimSpace(row[1]), 10, 64); perr == nil {
			gross = money.Kobo(g)
		} else if gk, kerr := money.ParseKobo(row[1]); kerr == nil {
			gross = gk
		} else {
			errs = append(errs, fmt.Sprintf("row %d: bad gross amount", i+1))
			continue
		}
		var vol money.Kobo
		isPPP := false
		if len(row) > 2 && strings.TrimSpace(row[2]) != "" {
			if v, perr := strconv.ParseInt(strings.TrimSpace(row[2]), 10, 64); perr == nil {
				vol = money.Kobo(v)
			}
		}
		if len(row) > 3 {
			isPPP = strings.EqualFold(strings.TrimSpace(row[3]), "true")
		}
		s.Lines = append(s.Lines, SplitLine(pin, gross, vol, isPPP))
	}
	if len(errs) > 0 {
		s.Status = SchedFailed
		s.ErrorSummary = strings.Join(errs, "; ")
		return s, fmt.Errorf("%w: %s", ErrValidation, s.ErrorSummary)
	}
	return s, nil
}

// ParseJSON ingests a JSON schedule.
func ParseJSON(tenantID, employerID, period string, body []byte, validatePIN func(string) bool) (*Schedule, error) {
	var lines []Line
	if err := json.Unmarshal(body, &lines); err != nil {
		return nil, err
	}
	s := &Schedule{TenantID: tenantID, EmployerID: employerID, Period: period, Status: SchedValidated, UploadedAt: time.Now().UTC()}
	var errs []string
	for i, l := range lines {
		if !validatePIN(l.RSAPIN) {
			errs = append(errs, fmt.Sprintf("line %d: PIN %s failed validation", i+1, l.RSAPIN))
			continue
		}
		if l.Employee == 0 && l.Employer == 0 && l.Gross > 0 {
			lines[i] = SplitLine(l.RSAPIN, l.Gross, l.Voluntary, l.IsPPP)
		}
	}
	s.Lines = lines
	if len(errs) > 0 {
		s.Status = SchedFailed
		s.ErrorSummary = strings.Join(errs, "; ")
		return s, fmt.Errorf("%w: %s", ErrValidation, s.ErrorSummary)
	}
	return s, nil
}
