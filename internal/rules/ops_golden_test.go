// Golden tests for the Tranche 4 PenCom ops pack (rules://ng/pencom/ops-2025@v2):
// 2025 fund table, fee rates, penalty rate, SRBC/en-bloc thresholds, VC/FCY
// reporting limits. Any change to these values must be a deliberate rules
// pack version bump (NFR-22).
package rules

import (
	"encoding/json"
	"testing"
)

// PenComOpsParams mirrors rules/ng/pencom.json ops-2025@v2 params.
type PenComOpsParams struct {
	Funds map[string]struct {
		VariableIncomeCapBps int64 `json:"variable_income_cap_bps"`
	} `json:"funds"`
	Fees struct {
		AdminActiveKobo        int64            `json:"admin_active_kobo"`
		AdminRetireeKobo       int64            `json:"admin_retiree_kobo"`
		MicroHighKobo          int64            `json:"micro_high_kobo"`
		MicroLowKobo           int64            `json:"micro_low_kobo"`
		MicroHighThresholdKobo int64            `json:"micro_high_threshold_kobo"`
		AssetBasedBps          map[string]int64 `json:"asset_based_bps"`
	} `json:"fees"`
	PenaltyBpsPerMonth    int64 `json:"penalty_bps_per_month"`
	RemittanceWorkingDays int   `json:"remittance_working_days"`
	PenComReportDays      int   `json:"pencom_report_days"`
	EnBlocMinWageKobo     int64 `json:"en_bloc_min_wage_kobo"`
	JobLossWaitMonths     int   `json:"job_loss_wait_months"`
	VcEFCCKobo            int64 `json:"vc_efcc_kobo"`
	FcyNFIUCents          int64 `json:"fcy_nfiu_cents"`
}

func loadOps(t *testing.T) PenComOpsParams {
	t.Helper()
	e := load(t)
	p, err := e.Get("rules://ng/pencom/ops-2025@v2")
	if err != nil {
		t.Fatal(err)
	}
	var ops PenComOpsParams
	if err := json.Unmarshal(p.Params, &ops); err != nil {
		t.Fatal(err)
	}
	return ops
}

func TestGolden_PenComOps_FundCaps(t *testing.T) {
	ops := loadOps(t)
	want := map[string]int64{
		"I": 9500, "II": 7500, "III": 2000, "IV": 1000,
		"V_CONSERVATIVE": 500, "V_GROWTH": 3000,
		"VI_ACTIVE": 5500, "VI_RETIREE": 1000,
	}
	if len(ops.Funds) != len(want) {
		t.Fatalf("fund count %d, want %d", len(ops.Funds), len(want))
	}
	for code, cap := range want {
		if got := ops.Funds[code].VariableIncomeCapBps; got != cap {
			t.Errorf("fund %s cap %d, want %d", code, got, cap)
		}
	}
}

func TestGolden_PenComOps_Fees(t *testing.T) {
	ops := loadOps(t)
	if ops.Fees.AdminActiveKobo != 10000 || ops.Fees.AdminRetireeKobo != 5000 {
		t.Errorf("admin fees: %+v", ops.Fees)
	}
	if ops.Fees.MicroHighKobo != 8000 || ops.Fees.MicroLowKobo != 2000 || ops.Fees.MicroHighThresholdKobo != 400000 {
		t.Errorf("micro tiers: %+v", ops.Fees)
	}
	if ops.Fees.AssetBasedBps["II"] != 155 {
		t.Errorf("asset-based bps II: %d", ops.Fees.AssetBasedBps["II"])
	}
}

func TestGolden_PenComOps_Thresholds(t *testing.T) {
	ops := loadOps(t)
	if ops.PenaltyBpsPerMonth != 200 {
		t.Errorf("penalty bps %d", ops.PenaltyBpsPerMonth)
	}
	if ops.RemittanceWorkingDays != 7 || ops.PenComReportDays != 14 {
		t.Errorf("deadlines: %d/%d", ops.RemittanceWorkingDays, ops.PenComReportDays)
	}
	if ops.EnBlocMinWageKobo != 7000000 {
		t.Errorf("en-bloc min wage %d", ops.EnBlocMinWageKobo)
	}
	if ops.JobLossWaitMonths != 4 {
		t.Errorf("job loss wait %d", ops.JobLossWaitMonths)
	}
	if ops.VcEFCCKobo != 500000000 {
		t.Errorf("VC EFCC %d", ops.VcEFCCKobo)
	}
	if ops.FcyNFIUCents != 1000000 {
		t.Errorf("FCY NFIU %d", ops.FcyNFIUCents)
	}
}
