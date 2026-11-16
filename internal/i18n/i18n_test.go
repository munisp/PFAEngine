package i18n

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTFallback(t *testing.T) {
	b := Bundle{Locale: English, Messages: map[string]string{"balance": "Balance: ₦%s"}}
	if got := b.T("no_such_key"); got != "no_such_key" {
		t.Fatalf("missing key should fall back to key, got %q", got)
	}
	if got := b.T(""); got != "" {
		t.Fatalf("empty key should not panic, got %q", got)
	}
	// Empty bundle must not panic either.
	var zero Bundle
	if got := zero.T("balance"); got != "balance" {
		t.Fatalf("nil bundle T = %q", got)
	}
}

func TestTFormatting(t *testing.T) {
	b := Bundle{Locale: English, Messages: map[string]string{
		"balance":               "Your RSA balance is ₦%s.",
		"contribution_received": "Received ₦%s for %s.",
		"plain":                 "No args here.",
	}}
	if got := b.T("balance", "12,345.67"); got != "Your RSA balance is ₦12,345.67." {
		t.Fatalf("T = %q", got)
	}
	if got := b.T("contribution_received", "14,400.00", "2026-11"); got != "Received ₦14,400.00 for 2026-11." {
		t.Fatalf("T = %q", got)
	}
	if got := b.T("plain", "ignored"); got != "No args here.%!(EXTRA string=ignored)" {
		t.Fatalf("extra args surface via Sprintf, got %q", got)
	}
	if got := b.T("plain"); got != "No args here." {
		t.Fatalf("T = %q", got)
	}
}

func TestLoadDirRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := map[string]string{"balance": "Balance ₦%s", "welcome": "Welcome %s"}
	raw := `{"balance": "Balance ₦%s", "welcome": "Welcome %s"}`
	if err := os.WriteFile(filepath.Join(dir, "en.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	// Wrapped form and a non-JSON file that must be ignored.
	if err := os.WriteFile(filepath.Join(dir, "yo.json"),
		[]byte(`{"messages": {"balance": "Àkópọ̀ ₦%s"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte("ignore me"), 0o644); err != nil {
		t.Fatal(err)
	}

	bundles, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != 2 {
		t.Fatalf("want 2 bundles, got %d", len(bundles))
	}
	en := bundles[English]
	if en.Locale != English || en.T("welcome", "Ada") != "Welcome Ada" {
		t.Fatalf("en bundle = %+v", en)
	}
	if got := bundles[Yoruba].T("balance", "1.00"); got != "Àkópọ̀ ₦1.00" {
		t.Fatalf("yo T = %q", got)
	}
	for k := range want {
		if en.Messages[k] != want[k] {
			t.Fatalf("en message %q = %q", k, en.Messages[k])
		}
	}
}

func TestLoadDirErrors(t *testing.T) {
	if _, err := LoadDir(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected error for missing dir")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "en.json"), []byte(`[1,2,3]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDir(dir); err == nil {
		t.Fatal("expected error for malformed JSON bundle")
	}
}

func TestRepoBundles(t *testing.T) {
	// The shipped i18n/ directory must load and cover the core keys.
	bundles, err := LoadDir(filepath.Join("..", "..", "i18n"))
	if err != nil {
		t.Fatal(err)
	}
	core := []string{
		"welcome", "balance", "contribution_received", "benefit_approved",
		"benefit_rejected", "i_am_alive_reminder", "i_am_alive_confirmed",
		"transfer_complete", "statement_ready", "penalty_applied",
		"fee_charged", "pin_issued", "account_suspended",
		"document_required", "payment_received", "error_generic",
	}
	for _, loc := range []Locale{English, Pidgin} {
		b, ok := bundles[loc]
		if !ok {
			t.Fatalf("locale %s not loaded", loc)
		}
		if len(b.Messages) < 15 {
			t.Fatalf("locale %s has %d messages, want >= 15", loc, len(b.Messages))
		}
		for _, k := range core {
			if b.T(k) == k {
				t.Errorf("locale %s missing key %q", loc, k)
			}
		}
	}
	// Pidgin must actually differ from English.
	if bundles[Pidgin].T("balance", "1.00") == bundles[English].T("balance", "1.00") {
		t.Fatal("pcm-NG should differ from en")
	}
}
