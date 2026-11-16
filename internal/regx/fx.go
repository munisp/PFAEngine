package regx

import (
	"fmt"
	"sync"

	"github.com/munisp/pfaengine/internal/money"
)

// FXRate: 1 unit of From = rate_milli minor-of-To per 1_000_000 (fixed point).
type FXRate struct {
	From, To   string
	RateMicros int64 // 1 From = RateMicros/1e6 To (in minor-unit ratio terms)
	AsOf       string
}

// FXTable implements REG-02 multi-currency accounting with
// realized/unrealized revaluation split.
type FXTable struct {
	mu    sync.RWMutex
	rates map[string]FXRate // "FROM:TO"
}

func NewFXTable() *FXTable { return &FXTable{rates: map[string]FXRate{}} }

func (t *FXTable) Set(r FXRate) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rates[r.From+":"+r.To] = r
}

// Convert converts minor units using the stored fixed-point rate.
func (t *FXTable) Convert(amount money.Kobo, from, to string) (money.Kobo, error) {
	if from == to {
		return amount, nil
	}
	t.mu.RLock()
	r, ok := t.rates[from+":"+to]
	t.mu.RUnlock()
	if !ok {
		return 0, fmt.Errorf("regx: no FX rate %s->%s", from, to)
	}
	return money.Kobo(int64(amount) * r.RateMicros / 1_000_000), nil
}

// Revalue splits FX movement into realized vs unrealized (REG-02).
type Revaluation struct {
	Realized   money.Kobo `json:"realized_kobo"`
	Unrealized money.Kobo `json:"unrealized_kobo"`
}

func (t *FXTable) Revalue(original money.Kobo, bookRate, currentRate FXRate, realizedPortion money.Kobo) Revaluation {
	bookVal := money.Kobo(int64(original) * bookRate.RateMicros / 1_000_000)
	curVal := money.Kobo(int64(original) * currentRate.RateMicros / 1_000_000)
	gain := curVal - bookVal
	var realized money.Kobo
	if original > 0 {
		realized = money.Kobo(int64(gain) * int64(realizedPortion) / int64(original))
	}
	return Revaluation{Realized: realized, Unrealized: gain - realized}
}
