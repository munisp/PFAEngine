package authn

import (
	"testing"
	"time"
)

func TestLoginWithTOTP(t *testing.T) {
	s := NewService("0123456789abcdef-signing")
	a, err := s.CreateAccount("t1", "admin@pfa.ng", "s3cret", RoleTenantAdmin)
	if err != nil {
		t.Fatal(err)
	}
	// Without MFA enrollment: plain login.
	tok, err := s.Login("admin@pfa.ng", "s3cret", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	tid, roles, err := s.VerifyToken(tok)
	if err != nil || tid != "t1" {
		t.Fatalf("token verify failed: %v %s", err, tid)
	}
	if err := RequireRole(roles, RoleOps); err != nil { // tenant admin passes all
		t.Fatal("tenant admin should pass RBAC")
	}
	// Enroll MFA.
	secret, err := s.EnrollMFA(a.Email)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	code, err := TOTP(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login("admin@pfa.ng", "s3cret", "", now); err != ErrMFARequired {
		t.Fatal("MFA must be required after enrollment")
	}
	if _, err := s.Login("admin@pfa.ng", "s3cret", "000000", now); err != ErrBadMFA {
		t.Fatalf("bad code: %v", err)
	}
	if _, err := s.Login("admin@pfa.ng", "s3cret", code, now); err != nil {
		t.Fatalf("valid TOTP rejected: %v", err)
	}
}

func TestTamperedTokenRejected(t *testing.T) {
	s := NewService("0123456789abcdef-signing")
	s.CreateAccount("t1", "ops@pfa.ng", "pw", RoleOps)
	tok, _ := s.Login("ops@pfa.ng", "pw", "", time.Now())
	if _, _, err := s.VerifyToken(tok + "x"); err != ErrBadToken {
		t.Fatal("tampered token must fail")
	}
}
