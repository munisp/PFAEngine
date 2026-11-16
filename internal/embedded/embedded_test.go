package embedded

import (
	"testing"
	"time"
)

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func TestKeyLifecycle(t *testing.T) {
	e := NewEngine()
	k, token, err := e.IssueKey("t1", "payroll-acme", []Scope{ScopeContribute}, true, now)
	if err != nil || token == "" {
		t.Fatalf("issue: %v", err)
	}
	// Correct scope authorizes; hash stored, not token.
	got, err := e.Authorize("t1", token, ScopeContribute)
	if err != nil || got.ID != k.ID {
		t.Fatalf("auth: %v %+v", err, got)
	}
	// Wrong scope denied.
	if _, err := e.Authorize("t1", token, ScopeBalanceRead); err != ErrScopeDenied {
		t.Fatalf("scope: %v", err)
	}
	// Unknown token.
	if _, err := e.Authorize("t1", "pfa_nope", ScopeContribute); err != ErrNoKey {
		t.Fatalf("unknown: %v", err)
	}
	// Revoked key rejected.
	if err := e.RevokeKey("t1", k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Authorize("t1", token, ScopeContribute); err != ErrRevoked {
		t.Fatalf("revoked: %v", err)
	}
}

func TestWebhooks(t *testing.T) {
	e := NewEngine()
	w := e.Subscribe("t1", "https://acme.example/hooks", []string{"contribution.posted"}, now)
	if w.Secret == "" {
		t.Fatal("secret")
	}
	ds := e.Dispatch("t1", "contribution.posted", now)
	if len(ds) != 1 || ds[0].WebhookID != w.ID || ds[0].Status != 202 {
		t.Fatalf("dispatch: %+v", ds)
	}
	// Non-matching event delivers nothing.
	if ds := e.Dispatch("t1", "benefit.approved", now); len(ds) != 0 {
		t.Fatalf("misdispatch: %+v", ds)
	}
	// Tenant isolation.
	if ds := e.Dispatch("t2", "contribution.posted", now); len(ds) != 0 {
		t.Fatalf("tenant leak: %+v", ds)
	}
}
