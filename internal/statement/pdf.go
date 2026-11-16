// Package statement implements FR-501: on-demand statements as
// QR/HMAC-verifiable PDFs and API payloads, <5s generation.
package statement

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/munisp/pfaengine/internal/funds"
	"github.com/munisp/pfaengine/internal/money"
)

// Txn is one statement line.
type Txn struct {
	Date        string     `json:"date"`
	Description string     `json:"description"`
	Amount      money.Kobo `json:"amount_kobo"`
	Balance     money.Kobo `json:"balance_kobo"`
}

// Payload is the API form of a statement (FR-501).
type Payload struct {
	ID             string          `json:"id"`
	TenantID       string          `json:"tenant_id"`
	RSAID          string          `json:"rsa_id"`
	MemberName     string          `json:"member_name"`
	RSAPIN         string          `json:"rsa_pin"`
	Period         string          `json:"period"`
	OpeningBalance money.Kobo      `json:"opening_kobo"`
	ClosingBalance money.Kobo      `json:"closing_kobo"`
	Holdings       []funds.Holding `json:"holdings"`
	Transactions   []Txn           `json:"transactions"`
	VerifyCode     string          `json:"verify_code"` // HMAC; rendered as QR client-side
	GeneratedAt    time.Time       `json:"generated_at"`
}

// Sign computes the tamper-evident verification code (QR payload).
func Sign(secret []byte, tenantID, rsaID, period string, closing money.Kobo) string {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(fmt.Sprintf("%s|%s|%s|%d", tenantID, rsaID, period, int64(closing))))
	return hex.EncodeToString(h.Sum(nil))[:24]
}

// Verify checks a presented code (QR-verifiable, FR-501).
func Verify(secret []byte, tenantID, rsaID, period string, closing money.Kobo, code string) bool {
	return hmac.Equal([]byte(Sign(secret, tenantID, rsaID, period, closing)), []byte(code))
}

// RenderPDF produces a minimal, valid single-page PDF statement.
// (Pure Go, no external renderer — deterministic and testable.)
func RenderPDF(p Payload) []byte {
	var lines []string
	lines = append(lines,
		"PFA-as-a-Service - Retirement Savings Account Statement",
		fmt.Sprintf("Member: %s    RSA PIN: %s", p.MemberName, p.RSAPIN),
		fmt.Sprintf("Period: %s    Generated: %s", p.Period, p.GeneratedAt.Format("2006-01-02")),
		fmt.Sprintf("Opening balance: NGN %s", p.OpeningBalance.Naira()),
	)
	for _, t := range p.Transactions {
		lines = append(lines, fmt.Sprintf("%s  %-40s %12s %12s", t.Date, t.Description, t.Amount.Naira(), t.Balance.Naira()))
	}
	lines = append(lines,
		fmt.Sprintf("Closing balance: NGN %s", p.ClosingBalance.Naira()),
		fmt.Sprintf("Verification code: %s", p.VerifyCode),
		"Verify this statement at https://verify.pfaengine.example with the code above.",
	)

	var content bytes.Buffer
	y := 760
	content.WriteString("BT /F1 16 Tf 50 780 Td (Statement) Tj ET\n")
	for _, ln := range lines {
		content.WriteString(fmt.Sprintf("BT /F1 9 Tf 50 %d Td (%s) Tj ET\n", y, pdfEscape(ln)))
		y -= 14
	}

	var doc bytes.Buffer
	offsets := []int{}
	write := func(s string) {
		offsets = append(offsets, doc.Len())
		doc.WriteString(s)
	}
	doc.WriteString("%PDF-1.4\n")
	write(fmt.Sprintf("1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n"))
	write(fmt.Sprintf("2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n"))
	write(fmt.Sprintf("3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 595 842]/Resources<</Font<</F1 4 0 R>>>>/Contents 5 0 R>>endobj\n"))
	write(fmt.Sprintf("4 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj\n"))
	write(fmt.Sprintf("5 0 obj<</Length %d>>stream\n%sendstream endobj\n", content.Len(), content.String()))
	xrefPos := doc.Len()
	doc.WriteString(fmt.Sprintf("xref\n0 6\n0000000000 65535 f \n"))
	for _, off := range offsets[1:] {
		doc.WriteString(fmt.Sprintf("%010d 00000 n \n", off))
	}
	doc.WriteString(fmt.Sprintf("trailer<</Size 6/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF", xrefPos))
	return doc.Bytes()
}

func pdfEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '(' || c == ')' || c == '\\' {
			out = append(out, '\\')
		}
		if c < 32 || c > 126 {
			c = '?'
		}
		out = append(out, c)
	}
	return string(out)
}
