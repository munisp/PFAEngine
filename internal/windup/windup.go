// Package windup implements SCM-14: scheme wind-up — the strict
// lifecycle state machine, the statutory distribution order, and the
// integer pro-rata allocation of the residual to contributors.
package windup

import (
	"errors"
	"fmt"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

// Status is a wind-up lifecycle state. Transitions are strictly forward.
type Status int

const (
	Initiated Status = iota
	AssetsLiquidated
	BenefitsComputed
	Distributed
	Closed
)

func (s Status) String() string {
	switch s {
	case Initiated:
		return "Initiated"
	case AssetsLiquidated:
		return "AssetsLiquidated"
	case BenefitsComputed:
		return "BenefitsComputed"
	case Distributed:
		return "Distributed"
	case Closed:
		return "Closed"
	}
	return "Unknown"
}

var ErrBadTransition = errors.New("windup: illegal status transition")

// WindUp tracks one scheme wind-up for a tenant.
type WindUp struct {
	TenantID  string    `json:"tenant_id"`
	StartedAt time.Time `json:"started_at"`
	Status    Status    `json:"status"`
}

// Advance moves the wind-up to the next state. Only strictly forward,
// one-state-at-a-time transitions are allowed
// (Initiated→AssetsLiquidated→BenefitsComputed→Distributed→Closed);
// anything else returns ErrBadTransition.
func (w *WindUp) Advance(to Status) error {
	if to != w.Status+1 {
		return fmt.Errorf("%w: %s → %s", ErrBadTransition, w.Status, to)
	}
	w.Status = to
	return nil
}

// DistributionOrder returns the statutory priority order for applying
// wind-up proceeds.
func DistributionOrder() []string {
	return []string{
		"pending benefits in payment",
		"transfer values to other PFAs",
		"administrative costs reserve",
		"residual to contributors pro-rata",
	}
}

// ProRata splits residual across contributor balances in integer kobo.
// Each member gets floor(residual × balance / total); the leftover
// remainder kobo go to the member with the largest balance (ties: the
// earliest). The result sums exactly to residual.
func ProRata(residual money.Kobo, balances []money.Kobo) []money.Kobo {
	out := make([]money.Kobo, len(balances))
	if residual == 0 || len(balances) == 0 {
		return out
	}
	var total int64
	for _, b := range balances {
		total += int64(b)
	}
	if total <= 0 {
		return out
	}
	var allocated int64
	largest := 0
	for i, b := range balances {
		share := int64(residual) * int64(b) / total
		out[i] = money.Kobo(share)
		allocated += share
		if balances[i] > balances[largest] {
			largest = i
		}
	}
	out[largest] += money.Kobo(int64(residual) - allocated)
	return out
}
