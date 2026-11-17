package pencomrails

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ---------------------------------------------------------------------------
// PSSP — Pension Service Support Provider channel
// ---------------------------------------------------------------------------

// PSSPClient submits remittance schedules to the PSSP and polls their
// processing status. It deliberately consumes schedule JSON bytes rather
// than importing internal/payroll, so the two packages stay decoupled and
// this connector compiles before/without the payroll package. Callers that
// have an internal/payroll Schedule should json.Marshal it and pass the
// bytes here.
type PSSPClient struct {
	BaseURL string
	PFACode string
	APIKey  string
	HTTP    *http.Client
}

// NewPSSPFromEnv builds a client from PSSP_BASE_URL, PSSP_PFA_CODE and
// PSSP_API_KEY. Fails closed when any is missing.
func NewPSSPFromEnv() (*PSSPClient, error) {
	c := &PSSPClient{
		BaseURL: strings.TrimRight(envOr("PSSP_BASE_URL"), "/"),
		PFACode: envOr("PSSP_PFA_CODE"),
		APIKey:  envOr("PSSP_API_KEY"),
	}
	if !configured(c.BaseURL, c.PFACode, c.APIKey) {
		return nil, errNotConfigured
	}
	return c, nil
}

func (c *PSSPClient) ready() error {
	if c == nil || !configured(c.BaseURL, c.PFACode, c.APIKey) {
		return errNotConfigured
	}
	return nil
}

// ScheduleState is the PSSP processing status of a submitted schedule.
type ScheduleState struct {
	Status string // QUEUED, PROCESSED or REJECTED
	Reason string // populated when REJECTED
}

const (
	PSSPQueued    = "QUEUED"
	PSSPProcessed = "PROCESSED"
	PSSPRejected  = "REJECTED"
)

// SubmitSchedule POSTs a payroll-remittance schedule (JSON) to
// {BaseURL}/v1/schedules (assumed endpoint) and returns the PSSP reference.
// The payload must already be valid JSON; invalid payloads fail closed.
func (c *PSSPClient) SubmitSchedule(ctx context.Context, scheduleJSON []byte) (string, error) {
	if err := c.ready(); err != nil {
		return "", err
	}
	if len(bytes.TrimSpace(scheduleJSON)) == 0 {
		return "", errors.New("pencomrails: empty schedule payload")
	}
	if !json.Valid(scheduleJSON) {
		return "", errors.New("pencomrails: schedule payload is not valid JSON")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/schedules", bytes.NewReader(scheduleJSON))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	setAuth(req, c.PFACode, c.APIKey)
	var out struct {
		Reference string `json:"reference"`
	}
	if err := doJSON(httpOrDefault(c.HTTP), req, &out); err != nil {
		return "", err
	}
	if out.Reference == "" {
		return "", errors.New("pencomrails: PSSP returned no schedule reference")
	}
	return out.Reference, nil
}

// ScheduleStatus polls {BaseURL}/v1/schedules/{ref} (assumed endpoint) and
// returns the processing state. Unknown statuses fail closed.
func (c *PSSPClient) ScheduleStatus(ctx context.Context, ref string) (ScheduleState, error) {
	var zero ScheduleState
	if err := c.ready(); err != nil {
		return zero, err
	}
	if strings.TrimSpace(ref) == "" {
		return zero, errors.New("pencomrails: schedule reference required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.BaseURL+"/v1/schedules/"+url.PathEscape(ref), nil)
	if err != nil {
		return zero, err
	}
	setAuth(req, c.PFACode, c.APIKey)
	var out struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := doJSON(httpOrDefault(c.HTTP), req, &out); err != nil {
		return zero, err
	}
	st := ScheduleState{Status: strings.ToUpper(out.Status), Reason: out.Reason}
	switch st.Status {
	case PSSPQueued, PSSPProcessed, PSSPRejected:
		return st, nil
	default:
		return zero, fmt.Errorf("pencomrails: unknown PSSP schedule status %q", out.Status)
	}
}
