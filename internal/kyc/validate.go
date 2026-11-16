package kyc

// FR-514 (PenCom circular 9 Jan 2025): BVN is mandatory for registration
// and recapture from 1 Feb 2025; BVN and NIN must pass 11-digit format
// validation before provider verification.

import "errors"

var (
	ErrBVNFormat = errors.New("kyc: BVN must be exactly 11 digits")
	ErrNINFormat = errors.New("kyc: NIN must be exactly 11 digits")
)

// allDigits reports whether s is non-empty and contains only 0-9.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ValidateBVNFormat checks the BVN format: exactly 11 numeric digits.
func ValidateBVNFormat(bvn string) error {
	if len(bvn) != 11 || !allDigits(bvn) {
		return ErrBVNFormat
	}
	return nil
}

// ValidateNINFormat checks the NIN format: exactly 11 numeric digits.
func ValidateNINFormat(nin string) error {
	if len(nin) != 11 || !allDigits(nin) {
		return ErrNINFormat
	}
	return nil
}
