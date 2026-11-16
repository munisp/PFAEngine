// Package rules implements NFR-19 / SCM-15 / REG-08: an externalized,
// versioned business-rules engine. Every calculation is pinned to a rule
// URI + version (rules://jurisdiction/authority/name@version), effective
// dated, and testable offline. The §13.2 worked examples are golden
// tests: any DSL/runtime change must reproduce them exactly.
package rules

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/munisp/pfaengine/internal/money"
)

var ErrRuleNotFound = errors.New("rules: rule not found")

type Kind string

const (
	KindContribSplit     Kind = "contrib-split"
	KindMultiFundDefault Kind = "multifund-default"
	KindRetireProjection Kind = "retire-projection"
	KindBenefitSplit     Kind = "benefit-split"
	KindDBAccrual        Kind = "db-accrual"
	KindEarlyFactor      Kind = "early-factor"
	KindCAREReval        Kind = "care-reval"
	KindShariaPack       Kind = "sharia-pack"
	KindTaxRelief        Kind = "tax-relief"
)

// Pack is one versioned rule set (REG-08: traceable to the rule version
// that produced each filed report).
type Pack struct {
	URI           string          `json:"uri"`
	Jurisdiction  string          `json:"jurisdiction"`
	Kind          Kind            `json:"kind"`
	Version       string          `json:"version"`
	EffectiveFrom string          `json:"effective_from"`
	EffectiveTo   string          `json:"effective_to,omitempty"`
	Params        json.RawMessage `json:"params"`
}

// Engine is the runtime registry of rule packs.
type Engine struct {
	mu    sync.RWMutex
	packs map[string]Pack // uri -> pack
}

func NewEngine() *Engine { return &Engine{packs: map[string]Pack{}} }

func (e *Engine) Register(p Pack) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.packs[p.URI] = p
}

// LoadDir loads every *.json rule pack from a directory tree.
func (e *Engine) LoadDir(dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var packs []Pack
		if err := json.Unmarshal(b, &packs); err != nil {
			var p Pack
			if err2 := json.Unmarshal(b, &p); err2 != nil {
				return fmt.Errorf("rules: %s: %w", path, err)
			}
			packs = []Pack{p}
		}
		for _, p := range packs {
			e.Register(p)
		}
		return nil
	})
}

func (e *Engine) Get(uri string) (Pack, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	p, ok := e.packs[uri]
	if !ok {
		return Pack{}, fmt.Errorf("%w: %s", ErrRuleNotFound, uri)
	}
	return p, nil
}

// --- Parameter structs ---

type ContribSplitParams struct {
	EmployeePct int `json:"employee_pct"`
	EmployerPct int `json:"employer_pct"`
}

type RetireProjectionParams struct {
	GrowthPctPerYear int `json:"growth_pct_per_year"` // contribution growth g
	ReturnPctPerYear int `json:"return_pct_per_year"` // net nominal return
}

type BenefitSplitParams struct {
	TaxFreeCashPct int `json:"tax_free_cash_pct"` // 25% in Nigeria (PRA 2014 s7)
}

type DBAccrualParams struct {
	AccrualNumerator   int `json:"accrual_numerator"`   // 1
	AccrualDenominator int `json:"accrual_denominator"` // 60
}

type EarlyFactorParams struct {
	Factors map[string]int `json:"factors"` // years early -> factor permille (800 = 0.80)
}

type CARERevalParams struct {
	AccrualPctMille int `json:"accrual_pct_mille"`  // 2.5% => 25
	RevalPctPerYear int `json:"reval_pct_per_year"` // 6 (CPI+1.5)
}

type ShariaParams struct {
	ProfitSharePct    int `json:"profit_share_pct"`
	PurificationMille int `json:"purification_mille"` // 1.8% => 18
	ZakatPctMille     int `json:"zakat_pct_mille"`    // 2.5% => 25
}

// --- Evaluators (pure, deterministic) ---

// EvalContribSplit: monthly contribution from gross (§13.2.1 step 1).
func EvalContribSplit(p Pack, gross money.Kobo) (employee, employer money.Kobo, err error) {
	var pr ContribSplitParams
	if err = json.Unmarshal(p.Params, &pr); err != nil {
		return
	}
	return money.MulKobo(gross, int64(pr.EmployeePct), 100),
		money.MulKobo(gross, int64(pr.EmployerPct), 100), nil
}

// EvalRetireProjection: FV of growing annual contributions + FV of
// existing pot. Returns (contribFV, existingFV) in kobo.
// FV = C1 * ((1+r)^n - (1+g)^n)/(r-g) ; existing = P * (1+r)^n.
// Float math is confined to projections (modelling), never the ledger.
func EvalRetireProjection(p Pack, firstYearAnnual money.Kobo, existing money.Kobo, years int) (money.Kobo, money.Kobo, error) {
	var pr RetireProjectionParams
	if err := json.Unmarshal(p.Params, &pr); err != nil {
		return 0, 0, err
	}
	r := float64(pr.ReturnPctPerYear) / 100
	g := float64(pr.GrowthPctPerYear) / 100
	n := float64(years)
	fvC := float64(firstYearAnnual) * (math.Pow(1+r, n) - math.Pow(1+g, n)) / (r - g)
	fvE := float64(existing) * math.Pow(1+r, n)
	return money.Kobo(math.Round(fvC)), money.Kobo(math.Round(fvE)), nil
}

// EvalBenefitSplit: tax-free cash + remainder (§13.2.1 retirement).
func EvalBenefitSplit(p Pack, pot money.Kobo) (tfc, remainder money.Kobo, err error) {
	var pr BenefitSplitParams
	if err = json.Unmarshal(p.Params, &pr); err != nil {
		return
	}
	tfc = money.MulKobo(pot, int64(pr.TaxFreeCashPct), 100)
	return tfc, pot - tfc, nil
}

// EvalDBAccrual: accrual × service years × pensionable salary (§13.2.2).
func EvalDBAccrual(p Pack, serviceYears int, pensionableSalary money.Kobo) (money.Kobo, error) {
	var pr DBAccrualParams
	if err := json.Unmarshal(p.Params, &pr); err != nil {
		return 0, err
	}
	return money.MulKobo(pensionableSalary, int64(serviceYears*pr.AccrualNumerator), int64(pr.AccrualDenominator)), nil
}

// EvalEarlyFactor applies an early-retirement actuarial factor (§13.2.2).
func EvalEarlyFactor(p Pack, annualPension money.Kobo, yearsEarly int) (money.Kobo, error) {
	var pr EarlyFactorParams
	if err := json.Unmarshal(p.Params, &pr); err != nil {
		return 0, err
	}
	f, ok := pr.Factors[fmt.Sprintf("%d", yearsEarly)]
	if !ok {
		return 0, fmt.Errorf("rules: no early factor for %d years", yearsEarly)
	}
	return money.MulKobo(annualPension, int64(f), 1000), nil
}

// EvalCARE: career-average accrual revalued to retirement (§13.2.3).
// Returns the pot at retirement for one year's accrual.
func EvalCARE(p Pack, pensionableSalary money.Kobo, yearsToRetire int) (money.Kobo, error) {
	var pr CARERevalParams
	if err := json.Unmarshal(p.Params, &pr); err != nil {
		return 0, err
	}
	accrual := float64(money.MulKobo(pensionableSalary, int64(pr.AccrualPctMille), 1000))
	r := float64(pr.RevalPctPerYear) / 100
	return money.Kobo(math.Round(accrual * math.Pow(1+r, float64(yearsToRetire)))), nil
}

// ShariaResult holds the §13.2.4 outputs.
type ShariaResult struct {
	ProfitShare  money.Kobo `json:"profit_share_kobo"`
	Purification money.Kobo `json:"purification_kobo"`
	Zakat        money.Kobo `json:"zakat_kobo"`
	NetRetained  money.Kobo `json:"net_retained_kobo"`
}

func EvalSharia(p Pack, balance, zakatableBase money.Kobo) (ShariaResult, error) {
	var pr ShariaParams
	if err := json.Unmarshal(p.Params, &pr); err != nil {
		return ShariaResult{}, err
	}
	profit := money.MulKobo(balance, int64(pr.ProfitSharePct), 100)
	purif := money.MulKobo(profit, int64(pr.PurificationMille), 1000)
	zakat := money.MulKobo(zakatableBase, int64(pr.ZakatPctMille), 1000)
	return ShariaResult{
		ProfitShare: profit, Purification: purif, Zakat: zakat,
		NetRetained: balance + profit - purif - zakat,
	}, nil
}
