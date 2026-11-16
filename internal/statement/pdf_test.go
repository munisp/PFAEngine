package statement

import (
	"bytes"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

func TestPDFValidAndVerifiable(t *testing.T) {
	secret := []byte("tenant-signing-secret")
	p := Payload{
		ID: "stmt-1", TenantID: "t1", RSAID: "rsa-1", MemberName: "Amara Okafor",
		RSAPIN: "PEN100", Period: "2026-Q3",
		OpeningBalance: money.NGN(6500000), ClosingBalance: money.NGN(6932000),
		Transactions: []Txn{{Date: "2026-08-05", Description: "Contribution Aug 2026", Amount: money.NGN(144000), Balance: money.NGN(6932000)}},
		GeneratedAt:  time.Now().UTC(),
	}
	p.VerifyCode = Sign(secret, p.TenantID, p.RSAID, p.Period, p.ClosingBalance)
	pdf := RenderPDF(p)
	if !bytes.HasPrefix(pdf, []byte("%PDF-1.4")) || !bytes.Contains(pdf, []byte("%%EOF")) {
		t.Fatal("invalid PDF output")
	}
	if !Verify(secret, "t1", "rsa-1", "2026-Q3", money.NGN(6932000), p.VerifyCode) {
		t.Fatal("verification code should validate")
	}
	if Verify(secret, "t1", "rsa-1", "2026-Q3", money.NGN(1), p.VerifyCode) {
		t.Fatal("tampered amount must fail verification")
	}
}
