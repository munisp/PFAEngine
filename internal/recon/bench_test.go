package recon

import (
	"fmt"
	"testing"

	"github.com/munisp/pfaengine/internal/contribution"
	"github.com/munisp/pfaengine/internal/money"
)

// BenchmarkReconcile10K measures the matching engine on a 10,000-line
// employer schedule against 10,000 custodian receipts (BR-03: >=95%
// auto-match on clean files — the fixture delivers 97% exact matches).
func BenchmarkReconcile10K(b *testing.B) {
	const n = 10000
	lines := make([]contribution.Line, 0, n)
	receipts := make([]Receipt, 0, n)
	for i := 0; i < n; i++ {
		pin := fmt.Sprintf("PEN%011d", i)
		gross := money.Kobo(1_000_000 + int64(i%503)*1000)
		line := contribution.SplitLine(pin, gross, 0, false)
		lines = append(lines, line)
		amt := line.Total()
		if i%100 >= 97 {
			amt += 500 // 3% amount mismatches -> 97% auto-match
		}
		receipts = append(receipts, Receipt{
			ID: fmt.Sprintf("rcpt-%d", i), TenantID: "t1", EmployerID: "emp-1",
			RSAPIN: pin, Amount: amt, ValueDate: "2026-11-05", Source: "CUSTODIAN",
		})
	}
	sched := &contribution.Schedule{
		ID: "sched-10k", TenantID: "t1", EmployerID: "emp-1",
		Period: "2026-11", Lines: lines,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		eng := NewEngine()
		res := eng.Reconcile(sched, receipts)
		if res.TotalLines != n {
			b.Fatalf("total lines = %d, want %d", res.TotalLines, n)
		}
		if rate := res.AutoMatchRate(); rate < 0.95 {
			b.Fatalf("auto-match rate %.3f below BR-03 floor 0.95", rate)
		}
	}
}
