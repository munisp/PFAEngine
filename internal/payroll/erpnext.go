package payroll

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

// ERPNextClient is a Frappe REST API client using token auth
// (Authorization: token <api_key>:<api_secret>).
type ERPNextClient struct {
	BaseURL   string
	APIKey    string
	APISecret string
	HTTP      *http.Client
}

// NewERPNextFromEnv builds a client from ERPNEXT_BASE_URL, ERPNEXT_API_KEY
// and ERPNEXT_API_SECRET. ok is false when any of them is missing, so
// callers can disable the integration cleanly instead of failing at runtime.
func NewERPNextFromEnv() (c ERPNextClient, ok bool) {
	c = ERPNextClient{
		BaseURL:   strings.TrimRight(os.Getenv("ERPNEXT_BASE_URL"), "/"),
		APIKey:    os.Getenv("ERPNEXT_API_KEY"),
		APISecret: os.Getenv("ERPNEXT_API_SECRET"),
	}
	if c.BaseURL == "" || c.APIKey == "" || c.APISecret == "" {
		return ERPNextClient{}, false
	}
	return c, true
}

func (c ERPNextClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c ERPNextClient) authorize(req *http.Request) {
	req.Header.Set("Authorization", "token "+c.APIKey+":"+c.APISecret)
	req.Header.Set("Accept", "application/json")
}

// scheduleDoctype is the Frappe doctype used by PushContributionSchedule,
// configurable via ERPNEXT_SCHEDULE_DOCTYPE (default "Contribution Schedule").
func scheduleDoctype() string {
	if d := os.Getenv("ERPNEXT_SCHEDULE_DOCTYPE"); d != "" {
		return d
	}
	return "Contribution Schedule"
}

// get performs an authenticated GET against a Frappe endpoint and decodes
// the standard {"data": ...} envelope into out. Fail-closed: any HTTP
// status >= 300 or any decode failure is an error.
func (c ERPNextClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return fmt.Errorf("payroll: ERPNext GET %s: %w", path, err)
	}
	c.authorize(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("payroll: ERPNext GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		snip, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("payroll: ERPNext GET %s: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(snip)))
	}
	var wrap struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrap); err != nil {
		return fmt.Errorf("payroll: ERPNext GET %s: decode envelope: %w", path, err)
	}
	if len(wrap.Data) == 0 {
		return fmt.Errorf("payroll: ERPNext GET %s: response missing data", path)
	}
	if err := json.Unmarshal(wrap.Data, out); err != nil {
		return fmt.Errorf("payroll: ERPNext GET %s: decode data: %w", path, err)
	}
	return nil
}

// Frappe document shapes. Money fields are json.Number so JSON floats from
// ERPNext are converted via money.ParseKobo (integer kobo), never via
// float64 arithmetic.
type payrollEntryDoc struct {
	Name        string `json:"name"`
	Company     string `json:"company"`
	PostingDate string `json:"posting_date"`
	StartDate   string `json:"start_date"`
}

type salarySlipRef struct {
	Name string `json:"name"`
}

type earningLine struct {
	Component string      `json:"salary_component"`
	Amount    json.Number `json:"amount"`
}

type salarySlipDoc struct {
	Name         string        `json:"name"`
	Employee     string        `json:"employee"`
	EmployeeName string        `json:"employee_name"`
	RSAPIN       string        `json:"custom_rsa_pin"` // site customisation: RSA PIN on Salary Slip or Employee
	GrossPay     json.Number   `json:"gross_pay"`
	Earnings     []earningLine `json:"earnings"`
}

// parseNumber converts a Frappe JSON number (naira) to kobo. An absent
// number unmarshals to an empty json.Number and is treated as zero.
func parseNumber(n json.Number) (money.Kobo, error) {
	if n == "" {
		return 0, nil
	}
	return money.ParseKobo(n.String())
}

// pensionableBase sums the basic/housing/transport earning components of a
// salary slip. When no such components are present it falls back to
// gross_pay, so slips from sites without a structured earnings table still
// produce a schedule.
func pensionableBase(slip salarySlipDoc) (money.Kobo, error) {
	var base money.Kobo
	for _, e := range slip.Earnings {
		comp := strings.ToLower(e.Component)
		if strings.Contains(comp, "basic") || strings.Contains(comp, "housing") || strings.Contains(comp, "transport") {
			k, err := parseNumber(e.Amount)
			if err != nil {
				return 0, fmt.Errorf("earning %q: %w", e.Component, err)
			}
			base += k
		}
	}
	if base > 0 {
		return base, nil
	}
	gross, err := parseNumber(slip.GrossPay)
	if err != nil {
		return 0, fmt.Errorf("gross_pay: %w", err)
	}
	return gross, nil
}

// FetchPayrollEntry loads a Frappe Payroll Entry and its Salary Slip
// children, mapping each slip to a Contribution with the statutory 8%/10%
// split of the pensionable base (basic + housing + transport, falling back
// to gross_pay). The schedule period is derived from the entry's posting
// date (or start date). Fail-closed: any HTTP >= 300 or decode error
// aborts the whole fetch — no partial schedules are returned.
func (c ERPNextClient) FetchPayrollEntry(ctx context.Context, payrollEntryID string) (Schedule, error) {
	var entry payrollEntryDoc
	if err := c.get(ctx, "/api/resource/Payroll Entry/"+url.PathEscape(payrollEntryID), &entry); err != nil {
		return Schedule{}, fmt.Errorf("payroll: fetch payroll entry %q: %w", payrollEntryID, err)
	}

	period := entry.PostingDate
	if len(period) < 7 {
		period = entry.StartDate
	}
	if len(period) >= 7 {
		period = period[:7]
	}
	if err := ValidatePeriod(period); err != nil {
		return Schedule{}, fmt.Errorf("payroll: payroll entry %q: %w", payrollEntryID, err)
	}

	filters, _ := json.Marshal([][]string{{"payroll_entry", "=", payrollEntryID}})
	q := url.Values{}
	q.Set("filters", string(filters))
	q.Set("fields", `["name"]`)
	q.Set("limit_page_length", "1000")
	var refs []salarySlipRef
	if err := c.get(ctx, "/api/resource/Salary Slip?"+q.Encode(), &refs); err != nil {
		return Schedule{}, fmt.Errorf("payroll: list salary slips for %q: %w", payrollEntryID, err)
	}

	s := Schedule{
		ID:         entry.Name,
		EmployerID: entry.Company,
		Period:     period,
	}
	for _, ref := range refs {
		var slip salarySlipDoc
		if err := c.get(ctx, "/api/resource/Salary Slip/"+url.PathEscape(ref.Name), &slip); err != nil {
			return Schedule{}, fmt.Errorf("payroll: fetch salary slip %q: %w", ref.Name, err)
		}
		base, err := pensionableBase(slip)
		if err != nil {
			return Schedule{}, fmt.Errorf("payroll: salary slip %q: %w", ref.Name, err)
		}
		ee, er := PensionSplit(base)
		s.Lines = append(s.Lines, Contribution{
			EmployeeID: slip.Employee,
			RSA_PIN:    slip.RSAPIN,
			Employee:   ee,
			Employer:   er,
			Period:     period,
		})
	}
	if len(s.Lines) == 0 {
		return Schedule{}, fmt.Errorf("payroll: payroll entry %q: %w", payrollEntryID, ErrEmptySchedule)
	}
	return s, nil
}

// PushContributionSchedule creates a draft schedule document in ERPNext and
// returns the created document name. The target doctype is configurable via
// ERPNEXT_SCHEDULE_DOCTYPE (default "Contribution Schedule"); amounts are
// sent both as naira strings (2dp) and integer kobo so the receiving
// doctype can pick either. Fail-closed: validation, HTTP >= 300 and decode
// failures all return an error and empty name.
func (c ERPNextClient) PushContributionSchedule(ctx context.Context, s Schedule) (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	type pushLine struct {
		EmployeeID string `json:"employee_id"`
		RSAPIN     string `json:"rsa_pin"`
		Employee   string `json:"employee_contrib"` // naira, 2dp
		Employer   string `json:"employer_contrib"` // naira, 2dp
		Total      string `json:"total"`            // naira, 2dp
		TotalKobo  int64  `json:"total_kobo"`
	}
	lines := make([]pushLine, 0, len(s.Lines))
	for _, l := range s.Lines {
		lines = append(lines, pushLine{
			EmployeeID: l.EmployeeID,
			RSAPIN:     l.RSA_PIN,
			Employee:   l.Employee.Naira(),
			Employer:   l.Employer.Naira(),
			Total:      l.Total().Naira(),
			TotalKobo:  int64(l.Total()),
		})
	}
	payload := map[string]any{
		"schedule_id": s.ID,
		"tenant_id":   s.TenantID,
		"employer_id": s.EmployerID,
		"period":      s.Period,
		"total":       s.Total().Naira(),
		"total_kobo":  int64(s.Total()),
		"lines":       lines,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("payroll: encode schedule: %w", err)
	}

	dt := scheduleDoctype()
	path := "/api/resource/" + url.PathEscape(dt)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("payroll: ERPNext POST %s: %w", path, err)
	}
	c.authorize(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("payroll: ERPNext POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		snip, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("payroll: ERPNext POST %s: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(snip)))
	}
	var wrap struct {
		Data struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrap); err != nil {
		return "", fmt.Errorf("payroll: ERPNext POST %s: decode: %w", path, err)
	}
	if wrap.Data.Name == "" {
		return "", fmt.Errorf("payroll: ERPNext POST %s: response missing document name", path)
	}
	return wrap.Data.Name, nil
}
