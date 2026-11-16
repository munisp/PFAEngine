// Golden tests: the four §13.2 worked examples, reproduced bit-for-bit.
// Any rules-engine change must keep these green before release (NFR-19/21).
package rules

import (
	"testing"

	"github.com/munisp/pfaengine/internal/money"
)

func load(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine()
	if err := e.LoadDir("../../rules/ng"); err != nil {
		t.Fatal(err)
	}
	return e
}

// §13.2.1 DC — Amara, 34, ₦800,000/month, Nigeria Fund II.
func TestGolden_DC_Nigeria_FundII(t *testing.T) {
	e := load(t)
	split, _ := e.Get("rules://ng/pencom/contrib-8-10@PRA2014-s4")
	emp, emr, err := EvalContribSplit(split, money.NGN(800000))
	if err != nil {
		t.Fatal(err)
	}
	if emp+emr != money.NGN(144000) {
		t.Fatalf("monthly contribution %s", (emp + emr).Naira())
	}
	if (emp+emr)*12 != money.NGN(1728000) {
		t.Fatal("annual mismatch")
	}
	proj, _ := e.Get("assump/retire-proj@2026-11")
	fvC, fvE, err := EvalRetireProjection(proj, money.NGN(1728000), money.NGN(6500000), 26)
	if err != nil {
		t.Fatal(err)
	}
	// Doc: ₦252.4M + ₦61.1M ≈ ₦313.5M
	if got := fvC.Naira(); got != "252438562.02" {
		t.Fatalf("contrib FV changed: %s", got)
	}
	if got := fvE.Naira(); got != "61094526.48" {
		t.Fatalf("existing FV changed: %s", got)
	}
	pot := fvC + fvE
	bs, _ := e.Get("rules://ng/pencom/benefit-splits@PRA2014-s7")
	tfc, rem, err := EvalBenefitSplit(bs, pot)
	if err != nil {
		t.Fatal(err)
	}
	if tfc != money.MulKobo(pot, 25, 100) || tfc+rem != pot {
		t.Fatal("benefit split inconsistent")
	}
}

// §13.2.2 DB final-salary — Kwame, 22 years, salary 60,000.
func TestGolden_DB_FinalSalary(t *testing.T) {
	e := load(t)
	acc, _ := e.Get("rules://generic/db-fs/1-60@2026-11")
	annual, err := EvalDBAccrual(acc, 22, money.NGN(60000))
	if err != nil {
		t.Fatal(err)
	}
	if annual != money.NGN(22000) {
		t.Fatalf("annual pension %s", annual.Naira())
	}
	ef, _ := e.Get("rules://generic/db-fs/early-factors@2026-11")
	early, err := EvalEarlyFactor(ef, annual, 4)
	if err != nil {
		t.Fatal(err)
	}
	if early != money.NGN(17600) {
		t.Fatalf("early pension %s", early.Naira())
	}
}

// §13.2.3 Hybrid/CARE — Zubaida, 2.5% accrual, 6% revaluation.
func TestGolden_CARE(t *testing.T) {
	e := load(t)
	care, _ := e.Get("rules://generic/db-care/2.5@2026-11")
	y1, _ := EvalCARE(care, money.NGN(9600000), 20)
	y2, _ := EvalCARE(care, money.NGN(10080000), 19)
	y3, _ := EvalCARE(care, money.NGN(10600000), 18)
	if y1 != money.Kobo(76971251) { // ₦769,712.51 → doc rounds to 769,713
		t.Fatalf("Y1 %s", y1.Naira())
	}
	if y2 != money.Kobo(76245107) {
		t.Fatalf("Y2 %s", y2.Naira())
	}
	if y3 != money.Kobo(75639988) {
		t.Fatalf("Y3 %s", y3.Naira())
	}
	total := y1 + y2 + y3
	// ≈ ₦2,288,564 (doc sums the rounded figures)
	if total < money.NGN(2288563) || total > money.NGN(2288565) {
		t.Fatalf("3yr total %s", total.Naira())
	}
}

// §13.2.4 Sharia — Aisha, ₦5.0M, 6% profit participation.
func TestGolden_Sharia(t *testing.T) {
	e := load(t)
	sh, _ := e.Get("rules://sharia/profit-participation@board-2026-09")
	res, err := EvalSharia(sh, money.NGN(5000000), money.NGN(5300000))
	if err != nil {
		t.Fatal(err)
	}
	if res.ProfitShare != money.NGN(300000) {
		t.Fatalf("profit %s", res.ProfitShare.Naira())
	}
	if res.Purification != money.NGN(5400) {
		t.Fatalf("purification %s", res.Purification.Naira())
	}
	if res.Zakat != money.NGN(132500) {
		t.Fatalf("zakat %s", res.Zakat.Naira())
	}
	if res.NetRetained != money.NGN(5162100) {
		t.Fatalf("net %s", res.NetRetained.Naira())
	}
}
