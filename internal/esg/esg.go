// Package esg implements impact and ESG reporting for pension-fund
// investments — the disclosure layer for infrastructure mandates (see
// internal/invmandate). Each deployment can report impact metrics (jobs
// created, MW capacity, km of road, homes financed); the engine rolls
// them up per mandate with an ESG score so PFAs can show contributors
// and PenCom what their capital built.
package esg

import (
	"fmt"
	"sync"
	"time"
)

// Metrics is one impact report against a deployment.
type Metrics struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	MandateID    string    `json:"mandate_id"`
	DeploymentID string    `json:"deployment_id"`
	JobsCreated  int64     `json:"jobs_created"`
	Megawatts    int64     `json:"megawatts"`
	RoadKm       int64     `json:"road_km"`
	HomesFinanced int64    `json:"homes_financed"`
	// Governance/social sub-scores, 0–100 each.
	EnvironmentScore int `json:"environment_score"`
	SocialScore      int `json:"social_score"`
	GovernanceScore  int `json:"governance_score"`
	ReportedAt       time.Time `json:"reported_at"`
}

// Rollup aggregates metrics for a mandate.
type Rollup struct {
	MandateID      string `json:"mandate_id"`
	Reports        int    `json:"reports"`
	JobsCreated    int64  `json:"jobs_created"`
	Megawatts      int64  `json:"megawatts"`
	RoadKm         int64  `json:"road_km"`
	HomesFinanced  int64  `json:"homes_financed"`
	ESGScore       int    `json:"esg_score"` // mean of sub-scores across reports
	Grade          string `json:"grade"`
}

func grade(score int) string {
	switch {
	case score >= 80:
		return "A"
	case score >= 65:
		return "B"
	case score >= 50:
		return "C"
	default:
		return "D"
	}
}

// Engine stores reports and computes rollups.
type Engine struct {
	mu      sync.Mutex
	reports []Metrics
	seq     int
}

func NewEngine() *Engine { return &Engine{} }

// clamp bounds a sub-score to 0–100.
func clamp(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// Report records an impact report for a deployment.
func (e *Engine) Report(tenantID, mandateID, deploymentID string, jobs, mw, km, homes int64, env, soc, gov int, at time.Time) Metrics {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	m := Metrics{ID: fmt.Sprintf("esg-%06d", e.seq), TenantID: tenantID, MandateID: mandateID,
		DeploymentID: deploymentID, JobsCreated: jobs, Megawatts: mw, RoadKm: km,
		HomesFinanced: homes, EnvironmentScore: clamp(env), SocialScore: clamp(soc),
		GovernanceScore: clamp(gov), ReportedAt: at}
	e.reports = append(e.reports, m)
	return m
}

// Rollup aggregates all reports for a mandate.
func (e *Engine) Rollup(tenantID, mandateID string) Rollup {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := Rollup{MandateID: mandateID}
	var sum int
	for _, m := range e.reports {
		if m.TenantID == tenantID && m.MandateID == mandateID {
			r.Reports++
			r.JobsCreated += m.JobsCreated
			r.Megawatts += m.Megawatts
			r.RoadKm += m.RoadKm
			r.HomesFinanced += m.HomesFinanced
			sum += (m.EnvironmentScore + m.SocialScore + m.GovernanceScore) / 3
		}
	}
	if r.Reports > 0 {
		r.ESGScore = sum / r.Reports
	}
	r.Grade = grade(r.ESGScore)
	return r
}

// Reports lists raw reports for a mandate.
func (e *Engine) Reports(tenantID, mandateID string) []Metrics {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Metrics
	for _, m := range e.reports {
		if m.TenantID == tenantID && m.MandateID == mandateID {
			out = append(out, m)
		}
	}
	return out
}
