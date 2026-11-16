package fraud

import (
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var t0 = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func baseTx(member string, n int64, at time.Time) Tx {
	return Tx{TenantID: "t1", MemberID: member, Kind: "CONTRIBUTION",
		Amount: money.NGN(n), Channel: "WEB", StateCode: "LA", At: at}
}

func TestCleanTxNoCase(t *testing.T) {
	e := NewEngine()
	score, sig, c := e.Score(baseTx("m1", 10000, t0))
	if score != 0 || len(sig) != 0 || c != nil {
		t.Fatalf("clean: %d %v %+v", score, sig, c)
	}
}

func TestVelocityAndChannelFlipOpenCase(t *testing.T) {
	e := NewEngine()
	e.Score(baseTx("m1", 10000, t0))
	e.Score(baseTx("m1", 11000, t0.Add(time.Minute)))
	e.Score(baseTx("m1", 9000, t0.Add(2*time.Minute)))
	// 4th tx in window + channel flip => 30 + 15 = 45 < 60, no case yet.
	tx := baseTx("m1", 10000, t0.Add(3*time.Minute))
	tx.Channel = "USSD"
	score, sig, c := e.Score(tx)
	if c != nil {
		t.Fatalf("premature case: %d %+v", score, c)
	}
	if score != 45 {
		t.Fatalf("score: %d %v", score, sig)
	}
	// 5th: velocity 30 + outlier 35 (5x avg) => case.
	tx = baseTx("m1", 300000, t0.Add(4*time.Minute))
	score, sig, c = e.Score(tx)
	if c == nil || c.Status != CaseOpen || score < 60 {
		t.Fatalf("case: %d %v %+v", score, sig, c)
	}
	if got := len(e.Cases("t1", true)); got != 1 {
		t.Fatalf("open cases: %d", got)
	}
}

func TestDormantThenLarge(t *testing.T) {
	e := NewEngine()
	e.Score(baseTx("m2", 10000, t0))
	tx := baseTx("m2", 600000, t0.Add(200*24*time.Hour))
	score, sig, c := e.Score(tx)
	if c == nil || score < 40 {
		t.Fatalf("dormant: %d %v", score, sig)
	}
	found := false
	for _, s := range sig {
		if s == "DORMANT_THEN_LARGE" {
			found = true
		}
	}
	if !found {
		t.Fatalf("signals: %v", sig)
	}
}

func TestResolve(t *testing.T) {
	e := NewEngine()
	e.Score(baseTx("m3", 10000, t0))
	tx := baseTx("m3", 600000, t0.Add(200*24*time.Hour))
	_, _, c := e.Score(tx)
	got, err := e.Resolve("t1", c.ID, CaseCleared)
	if err != nil || got.Status != CaseCleared {
		t.Fatalf("resolve: %v %+v", err, got)
	}
	if n := len(e.Cases("t1", true)); n != 0 {
		t.Fatalf("still open: %d", n)
	}
	if _, err := e.Resolve("t1", c.ID, "BOGUS"); err == nil {
		t.Fatal("bad resolution")
	}
}
