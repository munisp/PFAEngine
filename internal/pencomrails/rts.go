package pencomrails

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// RTS — RSA Transfer System
// ---------------------------------------------------------------------------

// TransferStatus is the RTS transfer lifecycle.
type TransferStatus string

const (
	StatusPending   TransferStatus = "PENDING"
	StatusValidated TransferStatus = "VALIDATED"
	StatusConfirmed TransferStatus = "CONFIRMED"
	StatusRejected  TransferStatus = "REJECTED"
	StatusCancelled TransferStatus = "CANCELLED"
)

// legalTransitions enforces PENDING → VALIDATED → CONFIRMED|REJECTED|CANCELLED.
// PENDING may also be cancelled before validation. Terminal states have no
// outgoing edges.
var legalTransitions = map[TransferStatus]map[TransferStatus]bool{
	StatusPending:   {StatusValidated: true, StatusCancelled: true},
	StatusValidated: {StatusConfirmed: true, StatusRejected: true, StatusCancelled: true},
}

// TransferInstruction is one RSA transfer between PFAs on the RTS.
type TransferInstruction struct {
	ID          string
	PIN         string
	FromPFA     string
	ToPFA       string
	InitiatedAt time.Time
	Status      TransferStatus
}

// Transition moves the instruction to a new status, enforcing the legal
// state-machine edges. Illegal transitions are hard errors.
func (t *TransferInstruction) Transition(to TransferStatus) error {
	if !legalTransitions[t.Status][to] {
		return fmt.Errorf("pencomrails: illegal transfer transition %s -> %s", t.Status, to)
	}
	t.Status = to
	return nil
}

// TransferWindowDays is the PenCom cooling-off rule: a member may transfer
// RSA at most once per 12 months (365 days).
const TransferWindowDays = 365

// CanTransfer reports whether a member whose last completed transfer was at
// lastTransferAt may transfer again at now. A zero lastTransferAt means the
// member has never transferred and is always eligible.
func CanTransfer(lastTransferAt, now time.Time) bool {
	if lastTransferAt.IsZero() {
		return true
	}
	earliest := lastTransferAt.Add(TransferWindowDays * 24 * time.Hour)
	return !now.Before(earliest)
}

var transferCSVHeader = []string{"TRANSFER_ID", "PIN", "FROM_PFA", "TO_PFA", "INITIATED_AT", "STATUS"}

// BuildTransferFile serialises instructions to CSV with header row:
// TRANSFER_ID,PIN,FROM_PFA,TO_PFA,INITIATED_AT,STATUS (INITIATED_AT RFC3339).
func BuildTransferFile(w io.Writer, ts []TransferInstruction) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(transferCSVHeader); err != nil {
		return err
	}
	for _, t := range ts {
		rec := []string{t.ID, t.PIN, t.FromPFA, t.ToPFA, t.InitiatedAt.UTC().Format(time.RFC3339), string(t.Status)}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// ParseTransferFile reads a transfer CSV produced by BuildTransferFile.
// The header row must match exactly; malformed rows are hard errors.
func ParseTransferFile(r io.Reader) ([]TransferInstruction, error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	hdr, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("pencomrails: transfer file: %w", err)
	}
	if strings.Join(hdr, ",") != strings.Join(transferCSVHeader, ",") {
		return nil, fmt.Errorf("pencomrails: unexpected transfer CSV header %q", strings.Join(hdr, ","))
	}
	var out []TransferInstruction
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("pencomrails: transfer file row: %w", err)
		}
		ts, err := time.Parse(time.RFC3339, rec[4])
		if err != nil {
			return nil, fmt.Errorf("pencomrails: transfer %q: bad INITIATED_AT %q", rec[0], rec[4])
		}
		st := TransferStatus(rec[5])
		switch st {
		case StatusPending, StatusValidated, StatusConfirmed, StatusRejected, StatusCancelled:
		default:
			return nil, fmt.Errorf("pencomrails: transfer %q: unknown status %q", rec[0], rec[5])
		}
		if rec[0] == "" || rec[1] == "" || rec[2] == "" || rec[3] == "" {
			return nil, fmt.Errorf("pencomrails: transfer row %q: ID, PIN, FROM_PFA and TO_PFA are required", rec[0])
		}
		out = append(out, TransferInstruction{
			ID: rec[0], PIN: rec[1], FromPFA: rec[2], ToPFA: rec[3],
			InitiatedAt: ts, Status: st,
		})
	}
	return out, nil
}

// RTSHub is the connector to the RSA Transfer System.
type RTSHub struct {
	BaseURL string
	PFACode string
	APIKey  string
	HTTP    *http.Client
}

// NewRTSFromEnv builds a hub from RTS_BASE_URL, RTS_PFA_CODE and RTS_API_KEY.
// Fails closed when any is missing.
func NewRTSFromEnv() (*RTSHub, error) {
	h := &RTSHub{
		BaseURL: strings.TrimRight(envOr("RTS_BASE_URL"), "/"),
		PFACode: envOr("RTS_PFA_CODE"),
		APIKey:  envOr("RTS_API_KEY"),
	}
	if !configured(h.BaseURL, h.PFACode, h.APIKey) {
		return nil, errNotConfigured
	}
	return h, nil
}

func (h *RTSHub) ready() error {
	if h == nil || !configured(h.BaseURL, h.PFACode, h.APIKey) {
		return errNotConfigured
	}
	return nil
}

// InitiateTransfer opens a PENDING transfer for pin to toPFA via
// POST {BaseURL}/v1/transfers (assumed endpoint).
func (h *RTSHub) InitiateTransfer(ctx context.Context, pin, toPFA string) (TransferInstruction, error) {
	var zero TransferInstruction
	if err := h.ready(); err != nil {
		return zero, err
	}
	if !pinRe.MatchString(pin) {
		return zero, fmt.Errorf("pencomrails: invalid RSA PIN %q", pin)
	}
	if strings.TrimSpace(toPFA) == "" {
		return zero, errors.New("pencomrails: destination PFA code required")
	}
	payload, _ := json.Marshal(map[string]string{
		"pin":      pin,
		"from_pfa": h.PFACode,
		"to_pfa":   toPFA,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.BaseURL+"/v1/transfers", bytes.NewReader(payload))
	if err != nil {
		return zero, err
	}
	req.Header.Set("Content-Type", "application/json")
	setAuth(req, h.PFACode, h.APIKey)
	var out struct {
		ID          string `json:"id"`
		PIN         string `json:"pin"`
		FromPFA     string `json:"from_pfa"`
		ToPFA       string `json:"to_pfa"`
		InitiatedAt string `json:"initiated_at"`
		Status      string `json:"status"`
	}
	if err := doJSON(httpOrDefault(h.HTTP), req, &out); err != nil {
		return zero, err
	}
	if out.ID == "" {
		return zero, errors.New("pencomrails: RTS returned no transfer ID")
	}
	ts, _ := time.Parse(time.RFC3339, out.InitiatedAt)
	return TransferInstruction{
		ID: out.ID, PIN: out.PIN, FromPFA: out.FromPFA, ToPFA: out.ToPFA,
		InitiatedAt: ts, Status: TransferStatus(out.Status),
	}, nil
}

// PollInbound fetches transfers initiated against this PFA since the given
// time via GET {BaseURL}/v1/transfers/inbound?since=<RFC3339>
// (assumed endpoint).
func (h *RTSHub) PollInbound(ctx context.Context, since time.Time) ([]TransferInstruction, error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	u := h.BaseURL + "/v1/transfers/inbound?since=" + url.QueryEscape(since.UTC().Format(time.RFC3339))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	setAuth(req, h.PFACode, h.APIKey)
	var rows []struct {
		ID          string `json:"id"`
		PIN         string `json:"pin"`
		FromPFA     string `json:"from_pfa"`
		ToPFA       string `json:"to_pfa"`
		InitiatedAt string `json:"initiated_at"`
		Status      string `json:"status"`
	}
	if err := doJSON(httpOrDefault(h.HTTP), req, &rows); err != nil {
		return nil, err
	}
	out := make([]TransferInstruction, 0, len(rows))
	for i, r := range rows {
		ts, err := time.Parse(time.RFC3339, r.InitiatedAt)
		if err != nil {
			return nil, fmt.Errorf("pencomrails: inbound transfer %d: bad initiated_at %q", i, r.InitiatedAt)
		}
		out = append(out, TransferInstruction{
			ID: r.ID, PIN: r.PIN, FromPFA: r.FromPFA, ToPFA: r.ToPFA,
			InitiatedAt: ts, Status: TransferStatus(r.Status),
		})
	}
	return out, nil
}

// Confirm acknowledges receipt/completion of a transfer via
// POST {BaseURL}/v1/transfers/{id}/confirm (assumed endpoint).
func (h *RTSHub) Confirm(ctx context.Context, transferID string) error {
	if err := h.ready(); err != nil {
		return err
	}
	if strings.TrimSpace(transferID) == "" {
		return errors.New("pencomrails: transfer ID required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.BaseURL+"/v1/transfers/"+url.PathEscape(transferID)+"/confirm", nil)
	if err != nil {
		return err
	}
	setAuth(req, h.PFACode, h.APIKey)
	return doJSON(httpOrDefault(h.HTTP), req, nil)
}
