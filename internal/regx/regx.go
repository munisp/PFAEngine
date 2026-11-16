// Package regx implements REG-01..09: the regulator adapter framework.
// All jurisdiction-specific behavior lives in adapter packs, never in the
// domain core. A new market = a new adapter pack, not a fork.
package regx

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Adapter is the pluggable module per jurisdiction (REG-01).
type Adapter interface {
	Jurisdiction() string
	// Supervisory reporting formats (a).
	ReportFormats() []ReportFormat
	// Member-registry/transfer protocol (b).
	TransferProtocol() TransferProtocol
	// Contribution-remittance protocol (c).
	RemittanceProtocol() RemittanceProtocol
	// Enrollment identity chain (d).
	IdentityChain() []string
}

type ReportFormat struct {
	Code      string `json:"code"`      // e.g. PENCOM-QTR-RETURNS
	Frequency string `json:"frequency"` // QUARTERLY | ANNUAL
	Format    string `json:"format"`    // JSON | XBRL | CSV (REG-09 SupTech)
}

type TransferProtocol struct {
	Name           string `json:"name"`             // RTS
	Cycle          string `json:"cycle"`            // QUARTERLY
	MinDaysBetween int    `json:"min_days_between"` // 365-day rule
	SLADays        int    `json:"sla_days"`
}

type RemittanceProtocol struct {
	Name             string `json:"name"` // PCRS
	RequiresPINMatch bool   `json:"requires_pin_match"`
	AutoPenalties    bool   `json:"auto_penalties"`
}

// Nigeria (PenCom) — the primary pack (REG-01 M).
type NigeriaAdapter struct{}

func (NigeriaAdapter) Jurisdiction() string { return "NG" }
func (NigeriaAdapter) ReportFormats() []ReportFormat {
	return []ReportFormat{
		{Code: "PENCOM-QTR-RETURNS", Frequency: "QUARTERLY", Format: "JSON"},
		{Code: "PENCOM-ANN-RETURNS", Frequency: "ANNUAL", Format: "JSON"},
		{Code: "PENCOM-INSPECTION-PACK", Frequency: "ON_DEMAND", Format: "JSON"},
	}
}
func (NigeriaAdapter) TransferProtocol() TransferProtocol {
	return TransferProtocol{Name: "RTS", Cycle: "QUARTERLY", MinDaysBetween: 365, SLADays: 30}
}
func (NigeriaAdapter) RemittanceProtocol() RemittanceProtocol {
	return RemittanceProtocol{Name: "PCRS", RequiresPINMatch: true, AutoPenalties: true}
}
func (NigeriaAdapter) IdentityChain() []string {
	return []string{"NIMC:NIN", "NIBSS:BVN", "PAD_LIVENESS"}
}

// Ghana (NPRA), Kenya (RBA), South Africa (FSCA), Egypt (PRA) — REG-01 S.
type GhanaAdapter struct{}

func (GhanaAdapter) Jurisdiction() string { return "GH" }
func (GhanaAdapter) ReportFormats() []ReportFormat {
	return []ReportFormat{{Code: "NPRA-QTR", Frequency: "QUARTERLY", Format: "JSON"}}
}
func (GhanaAdapter) TransferProtocol() TransferProtocol {
	return TransferProtocol{Name: "NPRA-PORTABILITY", Cycle: "ON_REQUEST", MinDaysBetween: 365, SLADays: 45}
}
func (GhanaAdapter) RemittanceProtocol() RemittanceProtocol {
	return RemittanceProtocol{Name: "NPRA-REM", RequiresPINMatch: true, AutoPenalties: false}
}
func (GhanaAdapter) IdentityChain() []string { return []string{"NIA:GHANA_CARD", "PAD_LIVENESS"} }

type KenyaAdapter struct{}

func (KenyaAdapter) Jurisdiction() string { return "KE" }
func (KenyaAdapter) ReportFormats() []ReportFormat {
	return []ReportFormat{{Code: "RBA-QTR", Frequency: "QUARTERLY", Format: "JSON"}}
}
func (KenyaAdapter) TransferProtocol() TransferProtocol {
	return TransferProtocol{Name: "RBA-TRANSFER", Cycle: "ON_REQUEST", MinDaysBetween: 365, SLADays: 60}
}
func (KenyaAdapter) RemittanceProtocol() RemittanceProtocol {
	return RemittanceProtocol{Name: "RBA-REM", RequiresPINMatch: false, AutoPenalties: false}
}
func (KenyaAdapter) IdentityChain() []string { return []string{"IPRS:NATIONAL_ID", "PAD_LIVENESS"} }

type SouthAfricaAdapter struct{}

func (SouthAfricaAdapter) Jurisdiction() string { return "ZA" }
func (SouthAfricaAdapter) ReportFormats() []ReportFormat {
	return []ReportFormat{{Code: "FSCA-QTR", Frequency: "QUARTERLY", Format: "XBRL"}}
}
func (SouthAfricaAdapter) TransferProtocol() TransferProtocol {
	return TransferProtocol{Name: "FSCA-S14", Cycle: "ON_REQUEST", MinDaysBetween: 0, SLADays: 60}
}
func (SouthAfricaAdapter) RemittanceProtocol() RemittanceProtocol {
	return RemittanceProtocol{Name: "FSCA-REM", RequiresPINMatch: false, AutoPenalties: false}
}
func (SouthAfricaAdapter) IdentityChain() []string { return []string{"DHA:SA_ID", "PAD_LIVENESS"} }

type EgyptAdapter struct{}

func (EgyptAdapter) Jurisdiction() string { return "EG" }
func (EgyptAdapter) ReportFormats() []ReportFormat {
	return []ReportFormat{{Code: "EG-PRA-ANN", Frequency: "ANNUAL", Format: "JSON"}}
}
func (EgyptAdapter) TransferProtocol() TransferProtocol {
	return TransferProtocol{Name: "EG-TRANSFER", Cycle: "ON_REQUEST", MinDaysBetween: 365, SLADays: 90}
}
func (EgyptAdapter) RemittanceProtocol() RemittanceProtocol {
	return RemittanceProtocol{Name: "EG-REM", RequiresPINMatch: false, AutoPenalties: false}
}
func (EgyptAdapter) IdentityChain() []string { return []string{"NATIONAL_ID", "PAD_LIVENESS"} }

// GenericAdapter: passport + liveness fallback (REG-03).
type GenericAdapter struct{ J string }

func (g GenericAdapter) Jurisdiction() string { return g.J }
func (g GenericAdapter) ReportFormats() []ReportFormat {
	return []ReportFormat{{Code: "GENERIC-QTR", Frequency: "QUARTERLY", Format: "JSON"}}
}
func (g GenericAdapter) TransferProtocol() TransferProtocol {
	return TransferProtocol{Name: "GENERIC", Cycle: "ON_REQUEST", MinDaysBetween: 365, SLADays: 90}
}
func (g GenericAdapter) RemittanceProtocol() RemittanceProtocol {
	return RemittanceProtocol{Name: "GENERIC-REM"}
}
func (g GenericAdapter) IdentityChain() []string { return []string{"PASSPORT", "PAD_LIVENESS"} }

// Registry resolves adapters by jurisdiction (REG-01).
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

func NewRegistry() *Registry {
	r := &Registry{adapters: map[string]Adapter{}}
	for _, a := range []Adapter{NigeriaAdapter{}, GhanaAdapter{}, KenyaAdapter{}, SouthAfricaAdapter{}, EgyptAdapter{}} {
		r.adapters[a.Jurisdiction()] = a
	}
	return r
}

func (r *Registry) For(jurisdiction string) (Adapter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	j := strings.ToUpper(jurisdiction)
	if a, ok := r.adapters[j]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("regx: no adapter pack for %s", j)
}

func (r *Registry) Register(a Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[a.Jurisdiction()] = a
}

// Supported lists the markets covered by adapter packs.
func (r *Registry) Supported() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.adapters))
	for j := range r.adapters {
		out = append(out, j)
	}
	return out
}

var ErrQuarterClosed = errors.New("regx: RTS submission window closed for this quarter")

// RTSQuarterWindow returns the quarterly cycle window for a date (INT-01).
// PenCom RTS runs quarterly: submission in the last month of each quarter.
func RTSQuarterWindow(month int) (quarter string, submissionOpen bool) {
	q := (month-1)/3 + 1
	quarter = fmt.Sprintf("Q%d", q)
	// Submission window: final month of each quarter.
	submissionOpen = month%3 == 0
	return quarter, submissionOpen
}
