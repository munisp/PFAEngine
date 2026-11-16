// Package lakehouse implements BR-07/BR-09: the analytics event staging
// sink and the management-information (MI) dashboard aggregations.
// Events are staged as JSONL lines (the parquet-export staging format;
// a batch job converts staged lines to Parquet for the lakehouse) and MI
// aggregations are pure functions over event slices — no external deps.
package lakehouse

import (
	"encoding/json"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

// Event kinds recognised by the MI aggregations.
const (
	KindContribution   = "CONTRIBUTION_POSTED"
	KindPenaltyApplied = "PENALTY_APPLIED"
	KindMemberActive   = "MEMBER_ACTIVE"
	KindUnitAllocation = "UNIT_ALLOCATION" // Meta["fund_code"] set
	KindBenefitPaid    = "BENEFIT_PAID"
	KindFeeCharged     = "FEE_CHARGED"
)

// Event is a single analytics fact emitted by a domain service for the
// lakehouse staging sink.
type Event struct {
	TenantID   string            `json:"tenant_id"`
	Kind       string            `json:"kind"`
	At         time.Time         `json:"at"`
	AmountKobo money.Kobo        `json:"amount_kobo"`
	Meta       map[string]string `json:"meta,omitempty"`
}

// Sink is the write side of the lakehouse staging pipeline.
type Sink interface {
	Write(e Event) error
}

// ParquetLine renders an event as one JSON object per line. This is the
// documented staging format for parquet export: the lakehouse batch job
// reads these JSONL files and columnarises them into Parquet.
func ParquetLine(e Event) []byte {
	b, err := json.Marshal(e)
	if err != nil { // impossible for this shape; defensive
		return []byte(`{"error":"marshal"}`)
	}
	return append(b, '\n')
}
