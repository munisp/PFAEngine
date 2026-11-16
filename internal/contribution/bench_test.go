package contribution

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// BenchmarkParseCSV10K measures CSV ingestion of a 10,000-row employer
// schedule (FR-203): rsa_pin,gross_kobo,voluntary_kobo,is_ppp.
func BenchmarkParseCSV10K(b *testing.B) {
	const n = 10000
	var buf bytes.Buffer
	buf.WriteString("rsa_pin,gross_kobo,voluntary_kobo,is_ppp\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&buf, "PEN%011d,%d,%d,%t\n", i, 1_000_000+i%503*1000, i%7*1000, i%11 == 0)
	}
	data := buf.Bytes()
	valid := func(pin string) bool { return strings.HasPrefix(pin, "PEN") }
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, err := ParseCSV("t1", "emp-1", "2026-11", bytes.NewReader(data), valid)
		if err != nil {
			b.Fatalf("parse failed: %v", err)
		}
		if len(s.Lines) != n {
			b.Fatalf("parsed %d lines, want %d", len(s.Lines), n)
		}
	}
}
