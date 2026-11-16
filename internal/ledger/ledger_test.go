package ledger

import (
	"testing"

	"github.com/munisp/pfaengine/internal/money"
)

func setup(t *testing.T) *Ledger {
	l := New()
	for _, a := range []Account{
		{ID: "custody-cash", TenantID: "t1", Name: "Custody Cash", Type: Asset, Currency: "NGN"},
		{ID: "member-rsa:m1", TenantID: "t1", Name: "RSA m1", Type: Liability, Currency: "NGN"},
		{ID: "suspense:emp1", TenantID: "t1", Name: "Suspense emp1", Type: Suspense, Currency: "NGN"},
	} {
		if err := l.OpenAccount(a); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func TestDoubleEntryBalanceEnforced(t *testing.T) {
	l := setup(t)
	_, err := l.Post("t1", "bad", "r1", []Posting{
		{AccountID: "custody-cash", Debit: money.NGN(100)},
		{AccountID: "member-rsa:m1", Credit: money.NGN(99)},
	})
	if err != ErrUnbalanced {
		t.Fatalf("expected unbalanced error, got %v", err)
	}
}

func TestContributionPostingAndTrialBalance(t *testing.T) {
	l := setup(t)
	_, err := l.Post("t1", "contribution", "sched-1", []Posting{
		{AccountID: "custody-cash", Debit: money.NGN(144000)},
		{AccountID: "member-rsa:m1", Credit: money.NGN(144000)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if l.Balance("custody-cash") != money.NGN(144000) {
		t.Fatalf("cash balance %v", l.Balance("custody-cash").Naira())
	}
	if l.Balance("member-rsa:m1") != money.NGN(144000) {
		t.Fatalf("rsa balance %v", l.Balance("member-rsa:m1").Naira())
	}
	tb := l.TrialBalance()
	var debitNormal, creditNormal int64
	for id, b := range tb {
		a := l.accounts[id]
		if a.Type.NormalDebit() {
			debitNormal += int64(b)
		} else {
			creditNormal += int64(b)
		}
	}
	if debitNormal != creditNormal {
		t.Fatalf("trial balance mismatch: %d vs %d", debitNormal, creditNormal)
	}
}

func TestReversalIsOnlyUndo(t *testing.T) {
	l := setup(t)
	e, _ := l.Post("t1", "c", "r", []Posting{
		{AccountID: "custody-cash", Debit: money.NGN(1000)},
		{AccountID: "member-rsa:m1", Credit: money.NGN(1000)},
	})
	if _, err := l.Reverse(e.ID, "error correction"); err != nil {
		t.Fatal(err)
	}
	if l.Balance("custody-cash") != 0 || l.Balance("member-rsa:m1") != 0 {
		t.Fatal("reversal should zero balances")
	}
}
