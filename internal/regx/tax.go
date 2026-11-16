package regx

import (
	"sync"

	"github.com/munisp/pfaengine/internal/money"
)

// TaxModel is the contribution-relief model per jurisdiction (REG-07).
type TaxModel string

const (
	EET TaxModel = "EET" // exempt-exempt-taxed (Nigeria: contributions relieved, benefits taxed)
	TEE TaxModel = "TEE"
	ETT TaxModel = "ETT"
)

// TaxRule is effective-dated and testable offline (REG-07).
type TaxRule struct {
	Jurisdiction    string   `json:"jurisdiction"`
	Model           TaxModel `json:"model"`
	EffectiveFrom   string   `json:"effective_from"`
	EffectiveTo     string   `json:"effective_to,omitempty"`
	ReliefCapPct    int      `json:"relief_pct"`    // % of contribution eligible for relief
	TaxFreeCashPct  int      `json:"tax_free_cash"` // e.g. 25% Nigeria
	BenefitTaxBands []Band   `json:"benefit_bands"`
}

type Band struct {
	UpToKobo  money.Kobo `json:"up_to_kobo"` // 0 = unlimited
	RateMille int        `json:"rate_mille"` // permille
}

// TaxEngine applies effective-dated tax rules per jurisdiction (REG-07).
type TaxEngine struct {
	mu    sync.RWMutex
	rules []TaxRule
}

func NewTaxEngine(rules []TaxRule) *TaxEngine {
	return &TaxEngine{rules: rules}
}

// ruleAt resolves the effective rule for a jurisdiction at a date.
func (e *TaxEngine) ruleAt(jurisdiction, date string) (TaxRule, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, r := range e.rules {
		if r.Jurisdiction != jurisdiction {
			continue
		}
		if r.EffectiveFrom <= date && (r.EffectiveTo == "" || date <= r.EffectiveTo) {
			return r, true
		}
	}
	return TaxRule{}, false
}

// ContributionRelief computes the tax-relieved portion of a contribution.
func (e *TaxEngine) ContributionRelief(jurisdiction, date string, contribution money.Kobo) money.Kobo {
	r, ok := e.ruleAt(jurisdiction, date)
	if !ok || r.Model == TEE {
		return 0
	}
	return money.MulKobo(contribution, int64(r.ReliefCapPct), 100)
}

// BenefitTax computes tax on a benefit payment under the effective model.
func (e *TaxEngine) BenefitTax(jurisdiction, date string, benefit money.Kobo) money.Kobo {
	r, ok := e.ruleAt(jurisdiction, date)
	if !ok || r.Model == TEE {
		return 0
	}
	// Tax-free cash carve-out first.
	taxable := benefit - money.MulKobo(benefit, int64(r.TaxFreeCashPct), 100)
	if r.Model == ETT || r.Model == EET {
		return applyBands(taxable, r.BenefitTaxBands)
	}
	return 0
}

// applyBands applies progressive bands to an amount.
func applyBands(amount money.Kobo, bands []Band) money.Kobo {
	var tax money.Kobo
	var lower money.Kobo
	for _, b := range bands {
		upper := b.UpToKobo
		if upper == 0 || upper > amount {
			upper = amount
		}
		if upper > lower {
			tax += money.MulKobo(upper-lower, int64(b.RateMille), 1000)
		}
		if b.UpToKobo == 0 || amount <= b.UpToKobo {
			break
		}
		lower = b.UpToKobo
	}
	return tax
}
