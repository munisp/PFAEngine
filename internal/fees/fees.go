// Package fees implements FR-502 / BR-13: the PenCom fee engine.
// Admin fees are capped at ₦100/month per active RSA holder (₦50 retiree),
// deducted pre-unitization; micro-pension admin fees are ₦80/month when the
// month's contributions total ≥ ₦4,000 else ₦20, and zero when nothing was
// contributed. No fees are charged after 3 consecutive months without
// contributions (fee holiday). Asset-based fees accrue daily on portfolio
// valuation at a per-fund annual basis-point rate.
// Rate parameters load from the rules pack (NFR-22) — never floats (DA-03).
package fees

import (
	"encoding/json"
	"fmt"

	"github.com/munisp/pfaengine/internal/money"
)

// MemberKind selects the admin-fee tier.
type MemberKind int

const (
	KindActive MemberKind = iota
	KindRetiree
	KindMicro
)

// RatePack holds the fee parameters loaded from the rules pack
// (rules://ng/pencom/ops-2025@v2, "fees" object).
type RatePack struct {
	FundRates          map[string]int64 // fund code -> asset-based fee bps/year
	AdminActiveKobo    money.Kobo       // ₦100 (10000 kobo)
	AdminRetireeKobo   money.Kobo       // ₦50
	MicroHighKobo      money.Kobo       // ₦80 when month total >= threshold
	MicroLowKobo       money.Kobo       // ₦20 otherwise
	MicroHighThreshold money.Kobo       // ₦4,000 (400000 kobo)
}

// RatePackFromParams decodes a RatePack from a rules pack's params blob.
func RatePackFromParams(params json.RawMessage) (RatePack, error) {
	var p struct {
		Fees struct {
			AdminActiveKobo        int64            `json:"admin_active_kobo"`
			AdminRetireeKobo       int64            `json:"admin_retiree_kobo"`
			MicroHighKobo          int64            `json:"micro_high_kobo"`
			MicroLowKobo           int64            `json:"micro_low_kobo"`
			MicroHighThresholdKobo int64            `json:"micro_high_threshold_kobo"`
			AssetBasedBps          map[string]int64 `json:"asset_based_bps"`
		} `json:"fees"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return RatePack{}, fmt.Errorf("fees: decode rate pack: %w", err)
	}
	return RatePack{
		FundRates:          p.Fees.AssetBasedBps,
		AdminActiveKobo:    money.Kobo(p.Fees.AdminActiveKobo),
		AdminRetireeKobo:   money.Kobo(p.Fees.AdminRetireeKobo),
		MicroHighKobo:      money.Kobo(p.Fees.MicroHighKobo),
		MicroLowKobo:       money.Kobo(p.Fees.MicroLowKobo),
		MicroHighThreshold: money.Kobo(p.Fees.MicroHighThresholdKobo),
	}, nil
}

// FeeHolidayMonths is the number of consecutive contribution-free months
// after which all admin fees stop (BR-13).
const FeeHolidayMonths = 3

// AdminFee returns the monthly admin fee for a member. Zero when no
// contribution was made in the month, and zero once the member has had
// FeeHolidayMonths consecutive months without contributions.
func AdminFee(rp RatePack, kind MemberKind, monthlyContrib money.Kobo, contributed bool, consecutiveInactiveMonths int) money.Kobo {
	if consecutiveInactiveMonths >= FeeHolidayMonths {
		return 0
	}
	if !contributed {
		return 0
	}
	switch kind {
	case KindActive:
		return rp.AdminActiveKobo
	case KindRetiree:
		return rp.AdminRetireeKobo
	case KindMicro:
		if monthlyContrib >= rp.MicroHighThreshold {
			return rp.MicroHighKobo
		}
		return rp.MicroLowKobo
	}
	return 0
}

// AccrueDaily computes one day of asset-based fee on a portfolio valued at
// nav: nav × annualBps/10000/365, via integer rational multiply.
func AccrueDaily(nav money.Kobo, annualBps int64) money.Kobo {
	return money.MulKobo(nav, annualBps, 10000*365)
}
