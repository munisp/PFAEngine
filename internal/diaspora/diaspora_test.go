package diaspora

import (
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func TestEnrolAndContribute(t *testing.T) {
	e := NewEngine()
	e.SetRate("USD", 153000) // ₦1,530.00 per $
	if _, err := e.Enrol("t1", "RSA-D1", "GB", "P-GB-123", now); err != nil {
		t.Fatal(err)
	}
	// $500 = 50,000 cents -> 50000 * 153000 / 100 = 76,500,000 kobo = ₦765,000.
	c, err := e.Contribute("t1", "RSA-D1", "USD", 50000, now)
	if err != nil || c.Credited != money.NGN(765_000) {
		t.Fatalf("contrib: %v %+v", err, c)
	}
	cs, total := e.Contributions("t1", "RSA-D1")
	if len(cs) != 1 || total != money.NGN(765_000) {
		t.Fatalf("total: %v %v", cs, total)
	}
}

func TestValidation(t *testing.T) {
	e := NewEngine()
	e.SetRate("USD", 153000)
	if _, err := e.Contribute("t1", "ghost", "USD", 100, now); err != ErrNoMember {
		t.Fatalf("member: %v", err)
	}
	e.Enrol("t1", "RSA-D1", "US", "P-1", now)
	if _, err := e.Contribute("t1", "RSA-D1", "CAD", 100, now); err != ErrBadCurrency {
		t.Fatalf("currency: %v", err)
	}
	if _, err := e.Contribute("t1", "RSA-D1", "USD", -1, now); err != ErrBadAmount {
		t.Fatalf("amount: %v", err)
	}
	if _, err := e.Contribute("t1", "RSA-D1", "GBP", 100, now); err != ErrNoFX {
		t.Fatalf("fx: %v", err)
	}
}
