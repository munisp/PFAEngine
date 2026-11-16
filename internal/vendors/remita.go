package vendors

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

// Remita implements employer-remittance payment instructions (INT-06).
// Payment file generation per Remita RRR convention; webhooks land in the
// contribution pipeline upstream.
type Remita struct {
	MerchantID string
	APIKey     string
	BaseURL    string // https://remitademo.net/remita/exapp/api/v1/send/api (demo)
	HTTP       *http.Client
}

// Instruction is a payment instruction to the custodian/bank rail.
type Instruction struct {
	RRR         string    `json:"rrr"`
	AmountKobo  int64     `json:"amount_kobo"`
	Narration   string    `json:"narration"`
	EmployerID  string    `json:"employer_id"`
	GeneratedAt time.Time `json:"generated_at"`
}

// hashKey: sha512(merchantId + serviceTypeId + orderId + amount + apiKey).
func (r *Remita) hashKey(serviceTypeID, orderID string, amount int64) string {
	h := sha512.Sum512([]byte(fmt.Sprintf("%s%s%s%d%s", r.MerchantID, serviceTypeID, orderID, amount/100, r.APIKey)))
	return hex.EncodeToString(h[:])
}

// CheckStatus queries the Remita eComm status endpoint for an order.
// Auth per the Remita convention: the SHA-512 hash-key
// (merchantId + serviceTypeId + orderId + amount + apiKey, in that exact
// order) is sent as the remitaConsumerToken credential alongside the
// merchantId as remitaConsumerKey.
func (r *Remita) CheckStatus(ctx context.Context, serviceTypeID, orderID string, amount money.Kobo) (string, error) {
	hc := r.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	hash := r.hashKey(serviceTypeID, orderID, int64(amount))
	url := fmt.Sprintf("%s/ecomm/%s/%s/%s/status.reg", r.BaseURL, r.MerchantID, orderID, hash)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization",
		fmt.Sprintf("remitaConsumerKey=%s, remitaConsumerToken=%s", r.MerchantID, hash))
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("remita: %s: %s", resp.Status, string(raw))
	}
	return string(raw), nil
}

// GenerateRRR creates the payment reference for an employer remittance.
func (r *Remita) GenerateRRR(employerID, scheduleID string, total money.Kobo) Instruction {
	ref := fmt.Sprintf("PFA-%s-%s", employerID, scheduleID)
	return Instruction{
		RRR: ref, AmountKobo: int64(total), Narration: "Pension contribution remittance " + scheduleID,
		EmployerID: employerID, GeneratedAt: time.Now().UTC(),
	}
}
