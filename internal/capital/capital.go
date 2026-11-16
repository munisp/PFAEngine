// Package capital implements FR-517 / BR-25: PFA recapitalisation
// monitoring (PenCom/INSP/Surv/2025/1255). Minimum paid-up capital is
// ₦20bn; PFAs with AUM of ₦500bn or more must hold an additional 1% of
// AUM above ₦500bn; the special NPF licence requires ₦30bn. Adequacy is
// assessed against audited shareholders' funds with a 90-day shortfall
// cure window. Integer kobo throughout (DA-03).
package capital

import (
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var (
	// BaseCapital is the ₦20bn minimum paid-up capital.
	BaseCapital = money.NGN(20_000_000_000)
	// NPFCapital is the ₦30bn minimum for the special NPF licence.
	NPFCapital = money.NGN(30_000_000_000)
	// AUMThreshold is the ₦500bn AUM level triggering the surcharge.
	AUMThreshold = money.NGN(500_000_000_000)
)

// CureWindowDays is the 90-day shortfall cure window (BR-25).
const CureWindowDays = 90

// SpecialNPF selects the ₦30bn NPF licence base.
const SpecialNPF = "NPF"

// RequiredCapital returns the minimum shareholders' funds for a PFA:
// ₦20bn base (₦30bn for the special NPF licence), plus — when AUM is at
// or above ₦500bn — 1% of the AUM above ₦500bn.
func RequiredCapital(aumKobo money.Kobo, special string) money.Kobo {
	base := BaseCapital
	if special == SpecialNPF {
		base = NPFCapital
	}
	if aumKobo >= AUMThreshold {
		base += money.MulKobo(aumKobo-AUMThreshold, 1, 100)
	}
	return base
}

// Status is the capital-adequacy assessment for the tenant dashboard
// (FR-517).
type Status struct {
	Compliant    bool       `json:"compliant"`
	Shortfall    money.Kobo `json:"shortfall_kobo"`
	CureDeadline time.Time  `json:"cure_deadline"` // zero when compliant
}

// Assess compares audited shareholders' funds against the required
// capital. On shortfall the 90-day cure deadline runs from asOf.
func Assess(shareholdersFunds, aum money.Kobo, asOf time.Time) Status {
	req := RequiredCapital(aum, "")
	if shareholdersFunds >= req {
		return Status{Compliant: true}
	}
	return Status{
		Compliant:    false,
		Shortfall:    req - shareholdersFunds,
		CureDeadline: asOf.AddDate(0, 0, CureWindowDays),
	}
}
