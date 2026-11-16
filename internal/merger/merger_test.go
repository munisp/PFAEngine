package merger

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

func TestMergeRosterReTenants(t *testing.T) {
	in := []MemberRecord{
		{ID: "m1", PIN: "PEN100", FundCode: "II", Balance: money.NGN(50_000)},
		{ID: "m2", PIN: "PEN200", FundCode: "III", Balance: money.NGN(75_000)},
	}
	out, err := MergeRoster("acq", "tgt", in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d records", len(out))
	}
	for i, m := range out {
		if !strings.HasPrefix(m.ID, "acq/") {
			t.Errorf("record %d not re-tenanted: %s", i, m.ID)
		}
		if m.PIN != in[i].PIN || m.Balance != in[i].Balance || m.FundCode != in[i].FundCode {
			t.Errorf("record %d mutated: %+v", i, m)
		}
	}
}

func TestMergeRosterPINConflict(t *testing.T) {
	in := []MemberRecord{
		{ID: "m1", PIN: "PEN100", Balance: money.NGN(1)},
		{ID: "m2", PIN: "PEN100", Balance: money.NGN(2)},
		{ID: "m3", PIN: "PEN300", Balance: money.NGN(3)},
	}
	_, err := MergeRoster("acq", "tgt", in)
	if !errors.Is(err, ErrPINConflict) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "PEN100") {
		t.Fatalf("conflict not listed: %v", err)
	}
}

func TestPlanMerge(t *testing.T) {
	cut := time.Date(2027, 3, 31, 0, 0, 0, 0, time.UTC)
	p := PlanMerge("acq", "tgt", cut)
	want := []string{"data-recapture", "trial-migration", "balance-proof", "pencom-approval", "cutover", "decommission"}
	if p.Acquirer != "acq" || p.Target != "tgt" || !p.CutoverDate.Equal(cut) {
		t.Fatalf("plan %+v", p)
	}
	if len(p.Steps) != len(want) {
		t.Fatalf("steps %v", p.Steps)
	}
	for i, s := range want {
		if p.Steps[i] != s {
			t.Fatalf("step %d = %q, want %q (order matters)", i, p.Steps[i], s)
		}
	}
}

func TestBalanceProofOK(t *testing.T) {
	before := []MemberRecord{
		{PIN: "PEN1", Balance: money.NGN(100)},
		{PIN: "PEN2", Balance: money.NGN(200)},
	}
	// Post-merge: re-tenanted IDs, same PINs and balances.
	after := []MemberRecord{
		{ID: "acq/m2", PIN: "PEN2", Balance: money.NGN(200)},
		{ID: "acq/m1", PIN: "PEN1", Balance: money.NGN(100)},
	}
	if err := BalanceProof(before, after); err != nil {
		t.Fatal(err)
	}
}

func TestBalanceProofMismatch(t *testing.T) {
	before := []MemberRecord{
		{PIN: "PEN1", Balance: money.NGN(100)},
		{PIN: "PEN2", Balance: money.NGN(200)},
	}
	cases := []struct {
		name  string
		after []MemberRecord
		want  string
	}{
		{"revalued", []MemberRecord{
			{PIN: "PEN1", Balance: money.NGN(99)},
			{PIN: "PEN2", Balance: money.NGN(200)},
		}, "PEN1"},
		{"missing member", []MemberRecord{
			{PIN: "PEN1", Balance: money.NGN(100)},
		}, "PEN2"},
		{"added member", []MemberRecord{
			{PIN: "PEN1", Balance: money.NGN(100)},
			{PIN: "PEN2", Balance: money.NGN(200)},
			{PIN: "PEN3", Balance: money.NGN(1)},
		}, "PEN3"},
	}
	for _, c := range cases {
		err := BalanceProof(before, c.after)
		if !errors.Is(err, ErrBalanceProof) {
			t.Errorf("%s: err = %v", c.name, err)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q missing %q", c.name, err, c.want)
		}
	}
}
