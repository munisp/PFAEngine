package escalation

import (
	"testing"
	"time"
)

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func TestTickStepsAndCaps(t *testing.T) {
	e := NewEngine()
	if _, err := e.Enrol("t1", "m1", 1800, 100, 2200, now); err != nil {
		t.Fatal(err)
	}
	evs := e.Tick("t1", now)
	if len(evs) != 1 || evs[0].ToBps != 1900 {
		t.Fatalf("tick1: %+v", evs)
	}
	e.Tick("t1", now)
	e.Tick("t1", now)
	e.Tick("t1", now) // 2200 reached
	evs = e.Tick("t1", now)
	r, _ := e.Rule("t1", "m1")
	if r.CurrentBps != 2200 || len(evs) != 0 {
		t.Fatalf("cap: %+v events=%v", r, evs)
	}
}

func TestOptOutFreezesAndAudits(t *testing.T) {
	e := NewEngine()
	e.Enrol("t1", "m1", 1800, 100, 2500, now)
	if err := e.OptOut("t1", "m1", "cannot afford", now); err != nil {
		t.Fatal(err)
	}
	if evs := e.Tick("t1", now); len(evs) != 0 {
		t.Fatalf("opted-out member stepped: %v", evs)
	}
	if err := e.ReEnrol("t1", "m1", now); err != nil {
		t.Fatal(err)
	}
	if evs := e.Tick("t1", now); len(evs) != 1 {
		t.Fatalf("re-enrol tick: %v", evs)
	}
	hist := e.Events("t1", "m1")
	kinds := map[string]bool{}
	for _, ev := range hist {
		kinds[ev.Kind] = true
	}
	if !kinds["STEP_UP"] || !kinds["OPT_OUT"] || !kinds["RE_ENROL"] {
		t.Fatalf("audit trail incomplete: %v", hist)
	}
}

func TestValidation(t *testing.T) {
	e := NewEngine()
	if _, err := e.Enrol("t1", "m1", 1800, 0, 2500, now); err != ErrBadStep {
		t.Fatalf("step: %v", err)
	}
	if _, err := e.Enrol("t1", "m2", 2500, 100, 1800, now); err != ErrBadStep {
		t.Fatalf("cap<base: %v", err)
	}
	if err := e.OptOut("t1", "ghost", "x", now); err != ErrNoRule {
		t.Fatalf("no rule: %v", err)
	}
}
