package funds

import (
	"testing"

	"github.com/munisp/pfaengine/internal/money"
)

func seeded(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	r.Register(Fund{ID: FundII, TenantID: "t1", Name: "Fund II", Currency: "NGN"})
	// 46 months of monthly NAV (2023-01-28 .. 2026-10-28), ~0.8%/month growth
	price := int64(100_00) // ₦100.00 per unit in kobo
	for m := 0; m <= 45; m++ {
		y := 2023 + m/12
		mm := m % 12 + 1
		date := []string{"", "01", "02", "03", "04", "05", "06", "07", "08", "09", "10", "11", "12"}[mm]
		d := itoa(y) + "-" + date + "-28"
		if err := r.PublishNAV(FundII, d, money.Kobo(price)); err != nil {
			t.Fatal(err)
		}
		price = price * 1008 / 1000
	}
	return r
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestCompoundedReturns(t *testing.T) {
	r := seeded(t)
	asOf := "2026-11-17"
	r12, err := r.CompoundedReturnBps(FundII, 12, asOf)
	if err != nil {
		t.Fatal(err)
	}
	// 12 months at 0.8%/mo ≈ 10.03% → ~1003 bps
	if r12 < 950 || r12 > 1060 {
		t.Fatalf("12m return: %d bps", r12)
	}
	r36, err := r.CompoundedReturnBps(FundII, 36, asOf)
	if err != nil {
		t.Fatal(err)
	}
	// 36 months ≈ 33.2% → ~3300 bps
	if r36 < 3200 || r36 > 3450 {
		t.Fatalf("36m return: %d bps", r36)
	}
	p, err := r.Performance(FundII, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if p.Return36MBps == nil || *p.Return36MBps != r36 {
		t.Fatalf("perf: %+v", p)
	}
	if p.UnitPriceKobo <= 0 || p.YtdReturnBps == nil {
		t.Fatalf("perf fields: %+v", p)
	}
	series, err := r.Series(FundII)
	if err != nil || len(series) != 46 || series[0].Date > series[45].Date {
		t.Fatalf("series: %d", len(series))
	}
}

func TestPerformanceUnknownFund(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Performance(FundII, "2026-11-17"); err == nil {
		t.Fatal("unknown fund must fail")
	}
}
