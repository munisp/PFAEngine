package vendors

import (
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

// Remita implements employer-remittance payment instructions (INT-06).
// Payment file generation per Remita RRR convention; webhooks land in the
// contribution pipeline upstream.
type Remita struct {
	MerchantID string
	APIKey     string
	BaseURL    string
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

// GenerateRRR creates the payment reference for an employer remittance.
func (r *Remita) GenerateRRR(employerID, scheduleID string, total money.Kobo) Instruction {
	ref := fmt.Sprintf("PFA-%s-%s", employerID, scheduleID)
	return Instruction{
		RRR: ref, AmountKobo: int64(total), Narration: "Pension contribution remittance " + scheduleID,
		EmployerID: employerID, GeneratedAt: time.Now().UTC(),
	}
}
