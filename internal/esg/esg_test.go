package esg

import (
	"testing"
	"time"
)

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func TestRollup(t *testing.T) {
	e := NewEngine()
	e.Report("t1", "im-1", "idp-1", 500, 20, 35, 1200, 80, 90, 85, now)
	e.Report("t1", "im-1", "idp-2", 300, 10, 15, 0, 70, 80, 75, now)
	r := e.Rollup("t1", "im-1")
	if r.Reports != 2 || r.JobsCreated != 800 || r.Megawatts != 30 || r.RoadKm != 50 || r.HomesFinanced != 1200 {
		t.Fatalf("rollup: %+v", r)
	}
	// ESG: (85 + 75) / 2 = 80 → grade A.
	if r.ESGScore != 80 || r.Grade != "A" {
		t.Fatalf("esg: %+v", r)
	}
	// Tenant isolation.
	if r2 := e.Rollup("t2", "im-1"); r2.Reports != 0 {
		t.Fatalf("leak: %+v", r2)
	}
}

func TestClamp(t *testing.T) {
	e := NewEngine()
	m := e.Report("t1", "im-1", "idp-1", 0, 0, 0, 0, 150, -5, 60, now)
	if m.EnvironmentScore != 100 || m.SocialScore != 0 {
		t.Fatalf("clamp: %+v", m)
	}
}
