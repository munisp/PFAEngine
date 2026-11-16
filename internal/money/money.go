// Package money implements DA-03: integer minor units (kobo) and
// 8-decimal unit quantities. No floating point anywhere in money paths.
package money

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Kobo is an integer minor-unit amount (1 NGN = 100 kobo).
type Kobo int64

// Unit is a fund-unit quantity with 8 decimals: raw value 100_000_000 = 1 unit.
type Unit int64

const UnitScale = int64(100_000_000)

var ErrParse = errors.New("money: parse error")

// NGN builds Kobo from whole naira.
func NGN(n int64) Kobo { return Kobo(n * 100) }

// ParseKobo parses "1234.56" (naira) into kobo without floats.
func ParseKobo(s string) (Kobo, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	parts := strings.SplitN(s, ".", 2)
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, ErrParse
	}
	frac := int64(0)
	if len(parts) == 2 {
		f := parts[1]
		if len(f) > 2 {
			f = f[:2]
		}
		for len(f) < 2 {
			f += "0"
		}
		frac, err = strconv.ParseInt(f, 10, 64)
		if err != nil {
			return 0, ErrParse
		}
	}
	k := Kobo(whole*100 + frac)
	if neg {
		k = -k
	}
	return k, nil
}

// Naira formats kobo as naira with 2 decimals.
func (k Kobo) Naira() string {
	neg := k < 0
	v := int64(k)
	if neg {
		v = -v
	}
	s := fmt.Sprintf("%d.%02d", v/100, v%100)
	if neg {
		return "-" + s
	}
	return s
}

// ParseUnit parses "1.23456789" into a Unit (max 8 decimals).
func ParseUnit(s string) (Unit, error) {
	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	parts := strings.SplitN(s, ".", 2)
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, ErrParse
	}
	frac := int64(0)
	if len(parts) == 2 {
		f := parts[1]
		if len(f) > 8 {
			f = f[:8]
		}
		for len(f) < 8 {
			f += "0"
		}
		frac, err = strconv.ParseInt(f, 10, 64)
		if err != nil {
			return 0, ErrParse
		}
	}
	u := Unit(whole*UnitScale + frac)
	if neg {
		u = -u
	}
	return u, nil
}

func (u Unit) String() string {
	neg := u < 0
	v := int64(u)
	if neg {
		v = -v
	}
	s := fmt.Sprintf("%d.%08d", v/UnitScale, v%UnitScale)
	if neg {
		return "-" + s
	}
	return s
}

// MulKobo multiplies kobo by a rational num/den (round half away from zero).
func MulKobo(k Kobo, num, den int64) Kobo {
	n := int64(k) * num
	q := n / den
	r := n % den
	if 2*abs64(r) >= abs64(den) {
		if (n > 0) == (den > 0) {
			q++
		} else {
			q--
		}
	}
	return Kobo(q)
}

// AllocateUnits converts kobo to fund units at price (kobo per whole unit).
func AllocateUnits(amount Kobo, pricePerUnit Kobo) (Unit, error) {
	if pricePerUnit <= 0 {
		return 0, errors.New("money: price must be positive")
	}
	return Unit(int64(amount) * UnitScale / int64(pricePerUnit)), nil
}

// ValueUnits converts units back to kobo at a price (truncated).
func ValueUnits(u Unit, pricePerUnit Kobo) Kobo {
	return Kobo(int64(u) * int64(pricePerUnit) / UnitScale)
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
