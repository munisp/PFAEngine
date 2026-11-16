package contribution

import (
	"strings"
	"testing"

	"github.com/munisp/pfaengine/internal/money"
)

func TestSplit810(t *testing.T) {
	l := SplitLine("PEN100", money.NGN(800000), 0, false)
	if l.Employee != money.NGN(64000) || l.Employer != money.NGN(80000) {
		t.Fatalf("split wrong: %s / %s", l.Employee.Naira(), l.Employer.Naira())
	}
	if l.Total() != money.NGN(144000) {
		t.Fatalf("total %s", l.Total().Naira())
	}
}

func TestParseCSVWithPINValidation(t *testing.T) {
	valid := func(pin string) bool { return pin != "BAD-PIN" }
	csvBody := "rsa_pin,gross\nPEN100,80000000\nBAD-PIN,50000000\n"
	s, err := ParseCSV("t1", "emp1", "2026-11", strings.NewReader(csvBody), valid)
	if err == nil || s.Status != SchedFailed {
		t.Fatal("expected validation failure on bad PIN")
	}
	if !strings.Contains(s.ErrorSummary, "BAD-PIN") {
		t.Fatalf("summary should name bad pin: %s", s.ErrorSummary)
	}
}

func TestParseCSVClean(t *testing.T) {
	valid := func(pin string) bool { return true }
	s, err := ParseCSV("t1", "emp1", "2026-11", strings.NewReader("rsa_pin,gross,voluntary,is_ppp\nPEN100,80000000,500000,false\nPEN200,12000000,0,true\n"), valid)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Lines) != 2 || !s.Lines[1].IsPPP {
		t.Fatalf("bad parse: %+v", s.Lines)
	}
	if s.Lines[0].Employee != money.NGN(64000) {
		t.Fatalf("split wrong: %s", s.Lines[0].Employee.Naira())
	}
}
