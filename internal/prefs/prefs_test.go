package prefs

import (
	"testing"

	"github.com/munisp/pfaengine/internal/i18n"
)

func TestSupported(t *testing.T) {
	cases := []struct {
		loc  i18n.Locale
		want bool
	}{
		{i18n.English, true},
		{i18n.Yoruba, true},
		{i18n.Hausa, true},
		{i18n.Igbo, true},
		{i18n.Pidgin, true},
		{"fr", false},
		{"", false},
		{"pcm", false},
	}
	for _, c := range cases {
		if got := Supported(c.loc); got != c.want {
			t.Errorf("Supported(%q) = %v, want %v", c.loc, got, c.want)
		}
	}
}

func TestSetGetDefault(t *testing.T) {
	s := NewStore()
	if got := s.Get("prs-1"); got != i18n.English {
		t.Fatalf("default = %q, want en", got)
	}
	s.Set("prs-1", i18n.Pidgin)
	if got := s.Get("prs-1"); got != i18n.Pidgin {
		t.Fatalf("after Set = %q, want pcm-NG", got)
	}
	// Unknown members still default to English.
	if got := s.Get("prs-2"); got != i18n.English {
		t.Fatalf("unknown member = %q, want en", got)
	}
}

func TestPhoneMapping(t *testing.T) {
	s := NewStore()
	s.Set("prs-1", i18n.Yoruba)
	s.LinkPhone("+234 803-000-0000", "prs-1")

	if id, ok := s.MemberForPhone("2348030000000"); !ok || id != "prs-1" {
		t.Fatalf("MemberForPhone = %q, %v", id, ok)
	}
	// WhatsApp webhook senders arrive as bare digits and must resolve.
	if got := s.LocaleForPhone("2348030000000"); got != i18n.Yoruba {
		t.Fatalf("LocaleForPhone = %q, want yo", got)
	}
	// Unknown phone falls back to English.
	if got := s.LocaleForPhone("+2341112223333"); got != i18n.English {
		t.Fatalf("unknown phone locale = %q, want en", got)
	}
	// Empty inputs are ignored.
	s.LinkPhone("", "prs-2")
	s.LinkPhone("+234000", "")
	if _, ok := s.MemberForPhone("+234000"); ok {
		t.Fatal("empty member ID must not be linked")
	}
}
