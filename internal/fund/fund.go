// Package fund implements FR-503 / BR-14 / BR-15 / BR-16: the 2025
// seven-fund multi-fund structure. Fund assignment is keyed to age at last
// birthday (Fund II default ≤49, Fund III ≥50, Fund IV retirees,
// Fund V-Conservative for micro/PPP members). Movement rules: one free
// move per 12 months (additional moves charged), III→I prohibited,
// II→III prohibited except the return path, members aged ≥50 or retired
// cannot enter Fund I, and micro funds are only for micro members.
// Variable-income caps (BR-16) live in the rules pack (NFR-22).
package fund

import (
	"errors"
	"fmt"
	"time"
)

// Fund codes under the Revised Investment Regulation 2025 s.8.
const (
	I             = "I"   // opt-in, ≤49
	II            = "II"  // default ≤49
	III           = "III" // default ≥50
	IV            = "IV"  // retirees
	VConservative = "V_CONSERVATIVE"
	VGrowth       = "V_GROWTH"
	VIActive      = "VI_ACTIVE"  // non-interest, active
	VIRetiree     = "VI_RETIREE" // non-interest, retiree
	VIIUSD        = "VII_USD"    // USD, diaspora/FX earners
)

var (
	ErrUnknownFund    = errors.New("fund: unknown fund code")
	ErrProhibitedMove = errors.New("fund: prohibited fund movement")
	ErrAgeRestricted  = errors.New("fund: members aged 50+ or retired cannot enter Fund I")
	ErrMicroFundOnly  = errors.New("fund: micro funds are only for micro-pension members")
)

// FreeMoveInterval is the one-free-move window (BR-15).
const FreeMoveInterval = 365 * 24 * time.Hour

func valid(code string) bool {
	switch code {
	case I, II, III, IV, VConservative, VGrowth, VIActive, VIRetiree, VIIUSD:
		return true
	}
	return false
}

func isMicroFund(code string) bool { return code == VConservative || code == VGrowth }

// DefaultFund returns the fund a member is assigned to by default: retirees
// to Fund IV, micro-pension members to Fund V-Conservative, members aged
// ≤49 (at last birthday) to Fund II, and ≥50 to Fund III.
func DefaultFund(ageAtLastBirthday int, retired bool, micro bool) string {
	switch {
	case retired:
		return IV
	case micro:
		return VConservative
	case ageAtLastBirthday <= 49:
		return II
	default:
		return III
	}
}

// ValidateMove checks a requested fund switch and reports whether a fee
// applies. returnPath must be true when the member previously moved III→II
// and is returning to Fund III (the only permitted II→III path). lastMove
// is the time of the member's previous move (zero if never); moves within
// 365 days of the last one are chargeable (fee=true), otherwise free.
func ValidateMove(from, to string, ageAtLastBirthday int, retired, micro, returnPath bool, lastMove, now time.Time) (fee bool, err error) {
	if !valid(from) || !valid(to) {
		return false, fmt.Errorf("%w: %q -> %q", ErrUnknownFund, from, to)
	}
	if to == from {
		return false, fmt.Errorf("%w: already in %s", ErrProhibitedMove, to)
	}
	if isMicroFund(to) && !micro {
		return false, ErrMicroFundOnly
	}
	if from == III && to == I {
		return false, fmt.Errorf("%w: III -> I", ErrProhibitedMove)
	}
	if from == II && to == III && !returnPath {
		return false, fmt.Errorf("%w: II -> III (return path only)", ErrProhibitedMove)
	}
	if to == I && (ageAtLastBirthday >= 50 || retired) {
		return false, ErrAgeRestricted
	}
	if !lastMove.IsZero() && now.Sub(lastMove) < FreeMoveInterval {
		return true, nil // additional move within 12 months: charged
	}
	return false, nil
}
