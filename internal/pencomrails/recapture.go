// Package pencomrails implements Gap 4: the connector layer for the three
// industry rails a licensed Nigerian PFA must speak:
//
//   - PENCAP  — PenCom data-recapture / enrolment file exchange
//   - RTS     — RSA Transfer System (member transfers between PFAs)
//   - PSSP    — Pension Service Support Provider remittance-schedule channel
//
// IMPORTANT: the wire layouts implemented here are ASSUMED layouts. The
// PenCom/PSSP bilateral interface specifications are industry-private and
// must be validated against the official specs before production use.
// See INTEGRATION-pencomrails.md.
//
// All clients fail closed: missing configuration, non-2xx responses and
// malformed payloads are hard errors — never silently accepted.
package pencomrails

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Shared client plumbing
// ---------------------------------------------------------------------------

const defaultHTTPTimeout = 30 * time.Second

var errNotConfigured = errors.New("pencomrails: client not configured (missing base URL, PFA code or API key)")

func envOr(k string) string { return strings.TrimSpace(os.Getenv(k)) }

// configured reports whether the mandatory connector settings are present.
func configured(baseURL, pfaCode, apiKey string) bool {
	return baseURL != "" && pfaCode != "" && apiKey != ""
}

func httpOrDefault(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: defaultHTTPTimeout}
}

// setAuth applies the standard rail auth headers. Assumed scheme:
// Bearer token plus an X-PFA-Code header identifying the licensed PFA.
func setAuth(req *http.Request, pfaCode, apiKey string) {
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("X-PFA-Code", pfaCode)
}

// checkStatus fails closed on any non-2xx response.
func checkStatus(resp *http.Response) error {
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("pencomrails: rail returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

// doJSON sends req, enforces a 2xx status, and JSON-decodes into out
// (out may be nil when the body is irrelevant).
func doJSON(c *http.Client, req *http.Request, out any) error {
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("pencomrails: %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return err
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 4<<20))
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("pencomrails: malformed JSON response: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// PENCAP data recapture
// ---------------------------------------------------------------------------

var (
	ninRe   = regexp.MustCompile(`^\d{11}$`)
	pinRe   = regexp.MustCompile(`^PEN\d{12}$`)
	phoneRe = regexp.MustCompile(`^\+234\d{10}$`)
)

// RecaptureRecord is one member's data-recapture submission (biodata + NIN)
// for the PenCom 2024-2025 data recapture exercise.
type RecaptureRecord struct {
	PIN          string // RSA PIN, format PEN + 12 digits
	NIN          string // National Identification Number, 11 digits
	Surname      string
	FirstName    string
	MiddleName   string // optional
	DOB          string // ISO date YYYY-MM-DD
	Gender       string // M or F
	Phone        string // E.164, Nigerian +234...
	Email        string // optional
	EmployerCode string // employer code registered with PenCom
}

// Validate enforces the field-level rules of the (assumed) recapture layout.
func (r RecaptureRecord) Validate() error {
	if !pinRe.MatchString(r.PIN) {
		return fmt.Errorf("pencomrails: invalid RSA PIN %q (want PEN + 12 digits)", r.PIN)
	}
	if !ninRe.MatchString(r.NIN) {
		return fmt.Errorf("pencomrails: invalid NIN %q (want 11 digits)", r.NIN)
	}
	if strings.TrimSpace(r.Surname) == "" || strings.TrimSpace(r.FirstName) == "" {
		return errors.New("pencomrails: surname and first name are required")
	}
	if r.DOB == "" {
		return errors.New("pencomrails: date of birth is required")
	}
	if _, err := time.Parse("2006-01-02", r.DOB); err != nil {
		return fmt.Errorf("pencomrails: DOB %q not ISO YYYY-MM-DD", r.DOB)
	}
	switch strings.ToUpper(r.Gender) {
	case "M", "F":
	default:
		return fmt.Errorf("pencomrails: gender %q must be M or F", r.Gender)
	}
	if !phoneRe.MatchString(r.Phone) {
		return fmt.Errorf("pencomrails: phone %q must be E.164 +234XXXXXXXXXX", r.Phone)
	}
	if strings.TrimSpace(r.EmployerCode) == "" {
		return errors.New("pencomrails: employer code is required")
	}
	return nil
}

// Assumed pipe-delimited recapture file layout:
//
//	HDR|PENCAP-RECAPTURE|1.0|<record-count>
//	REC|<PIN>|<NIN>|<SURNAME>|<FIRSTNAME>|<MIDDLENAME>|<DOB>|<GENDER>|<PHONE>|<EMAIL>|<EMPLOYERCODE>
//	...
//	TRAILER|<record-count>|<sha256-hex of all REC lines joined with \n>
//
// The checksum covers only the REC payload lines so the trailer can be
// verified before trusting any record.
const (
	recapHDRPrefix  = "HDR|PENCAP-RECAPTURE|1.0|"
	recapRecordTag  = "REC"
	recapTrailerTag = "TRAILER"
)

func recapLine(r RecaptureRecord) string {
	return strings.Join([]string{
		recapRecordTag, r.PIN, r.NIN, r.Surname, r.FirstName, r.MiddleName,
		r.DOB, strings.ToUpper(r.Gender), r.Phone, r.Email, r.EmployerCode,
	}, "|")
}

func recapChecksum(lines []string) string {
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// BuildRecaptureFile writes a checksum-protected pipe-delimited recapture
// file. Every record is validated first — the file is fail-closed: one bad
// record aborts the whole batch.
func BuildRecaptureFile(w io.Writer, recs []RecaptureRecord) error {
	for i, r := range recs {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("pencomrails: record %d: %w", i, err)
		}
	}
	lines := make([]string, len(recs))
	for i, r := range recs {
		lines[i] = recapLine(r)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s%d\n", recapHDRPrefix, len(recs))
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "%s|%d|%s\n", recapTrailerTag, len(recs), recapChecksum(lines))
	_, err := io.WriteString(w, b.String())
	return err
}

// ParseRecaptureFile reads a recapture file back, enforcing HDR/TRAILER
// record counts and the SHA-256 checksum. Any tampering is a hard error.
func ParseRecaptureFile(r io.Reader) ([]RecaptureRecord, error) {
	data, err := io.ReadAll(io.LimitReader(r, 64<<20))
	if err != nil {
		return nil, err
	}
	rows := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(rows) < 2 {
		return nil, errors.New("pencomrails: recapture file too short (need HDR and TRAILER)")
	}
	hdr := rows[0]
	if !strings.HasPrefix(hdr, recapHDRPrefix) {
		return nil, errors.New("pencomrails: bad recapture HDR line")
	}
	var hdrCount int
	if _, err := fmt.Sscanf(strings.TrimPrefix(hdr, recapHDRPrefix), "%d", &hdrCount); err != nil {
		return nil, fmt.Errorf("pencomrails: bad HDR record count: %w", err)
	}
	trailer := strings.Split(rows[len(rows)-1], "|")
	if len(trailer) != 3 || trailer[0] != recapTrailerTag {
		return nil, errors.New("pencomrails: bad recapture TRAILER line")
	}
	var trailCount int
	if _, err := fmt.Sscanf(trailer[1], "%d", &trailCount); err != nil {
		return nil, fmt.Errorf("pencomrails: bad TRAILER record count: %w", err)
	}
	body := rows[1 : len(rows)-1]
	if hdrCount != trailCount || len(body) != hdrCount {
		return nil, fmt.Errorf("pencomrails: record count mismatch (hdr=%d trailer=%d body=%d)", hdrCount, trailCount, len(body))
	}
	if got := recapChecksum(body); !strings.EqualFold(got, trailer[2]) {
		return nil, errors.New("pencomrails: checksum mismatch — recapture file tampered or corrupt")
	}
	recs := make([]RecaptureRecord, 0, len(body))
	for i, line := range body {
		f := strings.Split(line, "|")
		if len(f) != 11 || f[0] != recapRecordTag {
			return nil, fmt.Errorf("pencomrails: record %d: malformed REC line", i)
		}
		rec := RecaptureRecord{
			PIN: f[1], NIN: f[2], Surname: f[3], FirstName: f[4], MiddleName: f[5],
			DOB: f[6], Gender: f[7], Phone: f[8], Email: f[9], EmployerCode: f[10],
		}
		if err := rec.Validate(); err != nil {
			return nil, fmt.Errorf("pencomrails: record %d: %w", i, err)
		}
		recs = append(recs, rec)
	}
	return recs, nil
}

// RecaptureResult is PenCom's per-record validation outcome.
type RecaptureResult struct {
	PIN    string
	Status string // ACCEPTED or REJECTED
	Reason string // populated when REJECTED
}

const (
	RecaptureAccepted = "ACCEPTED"
	RecaptureRejected = "REJECTED"
)

// ParseRecaptureResponse parses the (assumed) validation response layout:
// one pipe-delimited line per record, PIN|STATUS|REASON. Blank lines and
// lines starting with '#' are ignored.
func ParseRecaptureResponse(r io.Reader) ([]RecaptureResult, error) {
	data, err := io.ReadAll(io.LimitReader(r, 64<<20))
	if err != nil {
		return nil, err
	}
	var out []RecaptureResult
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.SplitN(line, "|", 3)
		if len(f) < 2 {
			return nil, fmt.Errorf("pencomrails: response line %d: want PIN|STATUS[|REASON]", i+1)
		}
		res := RecaptureResult{PIN: strings.TrimSpace(f[0]), Status: strings.ToUpper(strings.TrimSpace(f[1]))}
		if len(f) == 3 {
			res.Reason = strings.TrimSpace(f[2])
		}
		switch res.Status {
		case RecaptureAccepted, RecaptureRejected:
		default:
			return nil, fmt.Errorf("pencomrails: response line %d: unknown status %q", i+1, res.Status)
		}
		if res.PIN == "" {
			return nil, fmt.Errorf("pencomrails: response line %d: empty PIN", i+1)
		}
		out = append(out, res)
	}
	if out == nil {
		return nil, errors.New("pencomrails: empty recapture response")
	}
	return out, nil
}

// PENCAPClient submits recapture batches to PenCom's PENCAP rails.
type PENCAPClient struct {
	BaseURL string
	PFACode string
	APIKey  string
	HTTP    *http.Client
}

// NewPENCAPFromEnv builds a client from PENCAP_BASE_URL, PENCAP_PFA_CODE and
// PENCAP_API_KEY. It fails closed when any of them is missing.
func NewPENCAPFromEnv() (*PENCAPClient, error) {
	c := &PENCAPClient{
		BaseURL: strings.TrimRight(envOr("PENCAP_BASE_URL"), "/"),
		PFACode: envOr("PENCAP_PFA_CODE"),
		APIKey:  envOr("PENCAP_API_KEY"),
	}
	if !configured(c.BaseURL, c.PFACode, c.APIKey) {
		return nil, errNotConfigured
	}
	return c, nil
}

func (c *PENCAPClient) ready() error {
	if c == nil || !configured(c.BaseURL, c.PFACode, c.APIKey) {
		return errNotConfigured
	}
	return nil
}

// SubmitRecapture builds the recapture file and multipart-POSTs it to
// {BaseURL}/v1/recapture (assumed endpoint). Returns the batch ID assigned
// by PenCom.
func (c *PENCAPClient) SubmitRecapture(ctx context.Context, recs []RecaptureRecord) (string, error) {
	if err := c.ready(); err != nil {
		return "", err
	}
	if len(recs) == 0 {
		return "", errors.New("pencomrails: no recapture records to submit")
	}
	var file bytes.Buffer
	if err := BuildRecaptureFile(&file, recs); err != nil {
		return "", err
	}
	var body bytes.Buffer
	mp := multipart.NewWriter(&body)
	part, err := mp.CreateFormFile("file", "recapture.txt")
	if err != nil {
		return "", err
	}
	if _, err := part.Write(file.Bytes()); err != nil {
		return "", err
	}
	if err := mp.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/recapture", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mp.FormDataContentType())
	setAuth(req, c.PFACode, c.APIKey)
	var out struct {
		BatchID string `json:"batch_id"`
	}
	if err := doJSON(httpOrDefault(c.HTTP), req, &out); err != nil {
		return "", err
	}
	if out.BatchID == "" {
		return "", errors.New("pencomrails: PENCAP returned no batch_id")
	}
	return out.BatchID, nil
}

// FetchResults retrieves the per-record validation results for a submitted
// batch from {BaseURL}/v1/recapture/{batchID}/results (assumed endpoint).
func (c *PENCAPClient) FetchResults(ctx context.Context, batchID string) ([]RecaptureResult, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(batchID) == "" {
		return nil, errors.New("pencomrails: batch ID required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/recapture/"+batchID+"/results", nil)
	if err != nil {
		return nil, err
	}
	setAuth(req, c.PFACode, c.APIKey)
	resp, err := httpOrDefault(c.HTTP).Do(req)
	if err != nil {
		return nil, fmt.Errorf("pencomrails: fetch results: %w", err)
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return nil, err
	}
	return ParseRecaptureResponse(resp.Body)
}
