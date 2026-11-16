package benefit

import (
	"testing"

	"github.com/munisp/pfaengine/internal/money"
)

func TestMakerCheckerEnforced(t *testing.T) {
	s := NewService()
	c, err := s.Submit("t1", "rsa-1", LumpSum, money.NGN(1000000), money.NGN(5000000), 55, "rules://ng/pencom/benefit-splits@PRA2014-s7", "maker1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(c.ID, "maker1", true, "self-approve"); err != ErrMakerChecker {
		t.Fatalf("expected maker-checker error, got %v", err)
	}
	if _, err := s.Decide(c.ID, "checker1", true, "ok"); err != nil {
		t.Fatal(err)
	}
	if err := s.InstructPayment(c.ID, "PFC-REF-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmPaid(c.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(c.ID)
	if got.State != StatePaid {
		t.Fatalf("state %s", got.State)
	}
}

func TestEarlyAccess25Rule(t *testing.T) {
	s := NewService()
	if _, err := s.Submit("t1", "rsa-1", EarlyAccess25, money.NGN(100000), money.NGN(1000000), 45, "v", "u"); err != ErrEarlyAccessRule {
		t.Fatal("age 45 must be rejected")
	}
	if _, err := s.Submit("t1", "rsa-1", EarlyAccess25, money.NGN(300000), money.NGN(1000000), 51, "v", "u"); err == nil {
		t.Fatal("over 25% must be rejected")
	}
	if _, err := s.Submit("t1", "rsa-1", EarlyAccess25, money.NGN(250000), money.NGN(1000000), 51, "v", "u"); err != nil {
		t.Fatalf("valid 25%% access rejected: %v", err)
	}
}

func TestProgrammedWithdrawal(t *testing.T) {
	if ProgrammedWithdrawalSchedule(money.NGN(235100000/100), 240) <= 0 {
		t.Fatal("schedule should be positive")
	}
}
