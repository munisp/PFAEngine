package funds

import (
	"fmt"
	"sort"

	"github.com/munisp/pfaengine/internal/money"
)

// Tranche 21: fund performance publication — every licensed PFA publishes
// daily unit prices and trailing compounded returns on its website (e.g.
// the "36-month compounded rate of return" on PFA homepages). This file
// computes those figures from the NAV history the pricing engine already
// publishes. Returns are basis points (bps), computed on monthly
// observation points to smooth daily gaps.

// PricePoint is one dated unit price in a published series.
type PricePoint struct {
	Date  string     `json:"date"`
	Price money.Kobo `json:"price_kobo"`
}

// Series returns the fund's NAV series, ascending by date.
func (r *Registry) Series(fundID string) ([]PricePoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.funds[fundID]; !ok {
		return nil, fmt.Errorf("funds: unknown fund %s", fundID)
	}
	m := r.navs[fundID]
	out := make([]PricePoint, 0, len(m))
	for d, p := range m {
		out = append(out, PricePoint{Date: d, Price: p})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out, nil
}

// ReturnBps computes the compounded total return between two dates:
// (price_end / price_start − 1) × 10_000. Integer arithmetic only — no
// floats anywhere near money.
func ReturnBps(start, end money.Kobo) (int64, error) {
	if start <= 0 {
		return 0, fmt.Errorf("funds: start price must be positive")
	}
	return (int64(end) - int64(start)) * 10_000 / int64(start), nil
}

// CompoundedReturnBps computes the trailing compounded return over the
// last `months` months ending at (or before) asOf, using the nearest
// available prices at the window edges. Mirrors the "trailing N-month
// compounded rate of return" PFAs publish.
func (r *Registry) CompoundedReturnBps(fundID string, months int, asOf string) (int64, error) {
	if asOf == "" {
		asOf = Today()
	}
	series, err := r.Series(fundID)
	if err != nil {
		return 0, err
	}
	if len(series) < 2 {
		return 0, fmt.Errorf("funds: insufficient NAV history for %s", fundID)
	}
	// window end: latest price ≤ asOf; window start: latest price ≤
	// asOf minus `months` months.
	endIdx := -1
	for i, p := range series {
		if p.Date <= asOf {
			endIdx = i
		}
	}
	if endIdx <= 0 {
		return 0, fmt.Errorf("funds: no price at or before %s", asOf)
	}
	startTarget := shiftMonths(asOf, -months)
	startIdx := -1
	for i := 0; i <= endIdx; i++ {
		if series[i].Date <= startTarget {
			startIdx = i
		}
	}
	if startIdx < 0 || startIdx >= endIdx {
		return 0, fmt.Errorf("funds: insufficient history for %d-month window", months)
	}
	return ReturnBps(series[startIdx].Price, series[endIdx].Price)
}

// Performance is the published performance snapshot for a fund.
type Performance struct {
	FundID        string `json:"fund_id"`
	AsOf          string `json:"as_of"`
	UnitPriceKobo int64  `json:"unit_price_kobo"`
	Return3MBps   *int64 `json:"return_3m_bps,omitempty"`
	Return6MBps   *int64 `json:"return_6m_bps,omitempty"`
	Return12MBps  *int64 `json:"return_12m_bps,omitempty"`
	Return36MBps  *int64 `json:"return_36m_bps,omitempty"` // the headline PFA figure
	YtdReturnBps  *int64 `json:"ytd_bps,omitempty"`
}

// Performance assembles the publication snapshot; missing windows are
// omitted rather than guessed.
func (r *Registry) Performance(fundID, asOf string) (Performance, error) {
	if asOf == "" {
		asOf = Today()
	}
	series, err := r.Series(fundID)
	if err != nil {
		return Performance{}, err
	}
	latest := ""
	var price money.Kobo
	for _, p := range series {
		if p.Date <= asOf {
			latest, price = p.Date, p.Price
		}
	}
	if latest == "" {
		return Performance{}, fmt.Errorf("funds: no price at or before %s", asOf)
	}
	p := Performance{FundID: fundID, AsOf: latest, UnitPriceKobo: int64(price)}
	set := func(dst **int64, months int) {
		if v, err := r.CompoundedReturnBps(fundID, months, asOf); err == nil {
			*dst = &v
		}
	}
	set(&p.Return3MBps, 3)
	set(&p.Return6MBps, 6)
	set(&p.Return12MBps, 12)
	set(&p.Return36MBps, 36)
	if v, err := r.CompoundedReturnBpsToDate(fundID, asOf[:4]+"-01-01", asOf); err == nil {
		p.YtdReturnBps = &v
	}
	return p, nil
}

// CompoundedReturnBpsToDate computes the return from the latest price at
// or before `from` to the latest price at or before `asOf` (YTD helper).
func (r *Registry) CompoundedReturnBpsToDate(fundID, from, asOf string) (int64, error) {
	series, err := r.Series(fundID)
	if err != nil {
		return 0, err
	}
	var start, end money.Kobo
	for _, p := range series {
		if p.Date <= from {
			start = p.Price
		}
		if p.Date <= asOf {
			end = p.Price
		}
	}
	if start == 0 || end == 0 {
		return 0, fmt.Errorf("funds: no price at window edges")
	}
	return ReturnBps(start, end)
}

// shiftMonths shifts a YYYY-MM-DD date by n months (clamped day).
func shiftMonths(date string, n int) string {
	y := atoi(date[0:4])
	m := atoi(date[5:7])
	d := atoi(date[8:10])
	total := y*12 + (m - 1) + n
	y, m = total/12, total%12+1
	days := daysIn(y, m)
	if d > days {
		d = days
	}
	return fmt.Sprintf("%04d-%02d-%02d", y, m, d)
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func daysIn(y, m int) int {
	switch m {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	}
	if y%400 == 0 || (y%4 == 0 && y%100 != 0) {
		return 29
	}
	return 28
}
