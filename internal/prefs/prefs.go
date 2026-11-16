// Package prefs implements member-facing preferences (REG-06): the
// per-member display/notification locale and the phone→member mapping the
// notification service uses to localise WhatsApp replies.
//
// The store is in-memory and thread-safe. member-svc and the notification
// service are separate binaries, so each process holds its own Store; in
// production this must be backed by a shared store (Postgres member row or
// Redis) — the in-memory implementation only shares state inside one
// process (monolith deployments and tests).
package prefs

import (
	"strings"
	"sync"

	"github.com/munisp/pfaengine/internal/i18n"
)

// DefaultLocale is used when a member (or phone number) has no preference.
const DefaultLocale = i18n.English

// supported enumerates the locales members may select (REG-06). Yoruba,
// Hausa and Igbo are selectable even where a message bundle is missing —
// bundle lookups fall back to English (see internal/chatbot.Responder).
var supported = map[i18n.Locale]bool{
	i18n.English: true,
	i18n.Yoruba:  true,
	i18n.Hausa:   true,
	i18n.Igbo:    true,
	i18n.Pidgin:  true,
}

// Supported reports whether loc is a member-selectable locale.
func Supported(loc i18n.Locale) bool { return supported[loc] }

// Store holds member locale preferences and the E.164 phone→member mapping.
type Store struct {
	mu       sync.RWMutex
	byMember map[string]i18n.Locale
	byPhone  map[string]string // normalised E.164 (digits only) -> memberID
}

func NewStore() *Store {
	return &Store{byMember: map[string]i18n.Locale{}, byPhone: map[string]string{}}
}

// Set records the member's preferred locale.
func (s *Store) Set(memberID string, loc i18n.Locale) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byMember[memberID] = loc
}

// Get returns the member's preferred locale, defaulting to English.
func (s *Store) Get(memberID string) i18n.Locale {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if loc, ok := s.byMember[memberID]; ok {
		return loc
	}
	return DefaultLocale
}

// LinkPhone binds a member's phone number (any E.164-ish form) to their
// member ID so inbound channel messages can be attributed. Called by
// member-svc when a member provides a phone at onboarding/login.
func (s *Store) LinkPhone(phone, memberID string) {
	phone = NormalizePhone(phone)
	if phone == "" || memberID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byPhone[phone] = memberID
}

// MemberForPhone resolves a phone number to a member ID.
func (s *Store) MemberForPhone(phone string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byPhone[NormalizePhone(phone)]
	return id, ok
}

// LocaleForPhone resolves the locale for an inbound channel sender
// (WhatsApp `from`): phone → member → locale, defaulting to English for
// unknown numbers.
func (s *Store) LocaleForPhone(phone string) i18n.Locale {
	if id, ok := s.MemberForPhone(phone); ok {
		return s.Get(id)
	}
	return DefaultLocale
}

// NormalizePhone reduces an E.164-ish number to bare digits so "+234...".
// (member records) and "234..." (WhatsApp webhook senders) match.
func NormalizePhone(phone string) string {
	return strings.NewReplacer("+", "", " ", "", "-", "").Replace(strings.TrimSpace(phone))
}
