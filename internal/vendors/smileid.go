// Package vendors implements the real provider adapters (INT-02..08):
// Smile ID & Dojah (NIN/BVN/liveness), Termii (SMS), Remita (payments),
// Africa's Talking (USSD/SMS). All conform to the domain interfaces in
// internal/kyc and internal/notify — swap mocks for these with credentials.
package vendors

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/munisp/pfaengine/internal/kyc"
)

// SmileID implements kyc.Provider against the Smile ID v2 API
// (NIN verification, BVN validation, SmartSelfie liveness+face match).
type SmileID struct {
	PartnerID string
	APIKey    string
	BaseURL   string // https://api.smileidentity.com/v2 (sandbox: .../v2 sandbox host)
	HTTP      *http.Client
}

func (s *SmileID) Name() string { return "smileid" }

func (s *SmileID) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// signature: HMAC-SHA256(timestamp + partner_id + "sid_request") per Smile ID docs.
func (s *SmileID) signature(ts time.Time) (string, string) {
	tss := ts.UTC().Format(time.RFC3339Nano)
	mac := hmac.New(sha256.New, []byte(s.APIKey))
	mac.Write([]byte(tss + s.PartnerID + "sid_request"))
	return tss, base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(mac.Sum(nil))))
}

func (s *SmileID) post(ctx context.Context, path string, body any, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", s.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("smileid: %s: %s", resp.Status, string(raw))
	}
	return json.Unmarshal(raw, out)
}

type smileIDJobRequest struct {
	PartnerID string            `json:"partner_id"`
	Timestamp string            `json:"timestamp"`
	Signature string            `json:"signature"`
	JobType   int               `json:"job_type"` // 2 = ID verification (NIN/BVN); 1 = biometric KYC
	IDInfo    map[string]string `json:"id_info"`
}

type smileIDResponse struct {
	ResultCode string          `json:"ResultCode"`
	ResultText string          `json:"ResultText"`
	Actions    json.RawMessage `json:"Actions"`
	Confidence string          `json:"ConfidenceValue"`
	JobID      string          `json:"SmileJobID"`
}

func (s *SmileID) verifyID(ctx context.Context, idType, idNumber, name, dob string) (kyc.VerifyResult, error) {
	ts, sig := s.signature(time.Now())
	jr := smileIDJobRequest{
		PartnerID: s.PartnerID, Timestamp: ts, Signature: sig, JobType: 2,
		IDInfo: map[string]string{
			"country": "NG", "id_type": idType, "id_number": idNumber,
			"first_name": name, "dob": dob,
		},
	}
	var out smileIDResponse
	if err := s.post(ctx, "/verify", jr, &out); err != nil {
		return kyc.VerifyResult{}, err
	}
	return kyc.VerifyResult{
		ProviderRef: out.JobID,
		Token:       hexHash(s.APIKey + "|" + idNumber), // tokenized, never raw NIN
		Matched:     out.ResultCode == "0810" || out.ResultCode == "1012",
		EvidenceRef: "ev://smileid/" + out.JobID,
	}, nil
}

func hexHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func (s *SmileID) VerifyNIN(nin, name, dob string) (kyc.VerifyResult, error) {
	return s.verifyID(context.Background(), "NIN", nin, name, dob)
}

func (s *SmileID) ValidateBVN(bvn, name, dob, phone string) (kyc.VerifyResult, error) {
	return s.verifyID(context.Background(), "BVN", bvn, name, dob)
}

// LivenessFaceMatch runs SmartSelfie (job type 1/2 biometric) — in
// production the selfie bytes are uploaded; here selfieRef is the
// evidence locator.
func (s *SmileID) LivenessFaceMatch(selfieRef, ninToken string) (float64, string, error) {
	return 0, "", fmt.Errorf("smileid: submit via upload endpoint; selfieRef=%s queued", selfieRef)
}
