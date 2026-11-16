package prefs

// Tranche 14: durable, write-through persistence for member preferences.
// PersistentStore wraps the in-memory Store with a Repo (implemented by
// internal/store.PrefsRepo against Postgres). The in-memory Store is
// unchanged and remains fully usable on its own.

import (
	"context"
	"errors"

	"github.com/munisp/pfaengine/internal/i18n"
)

// ErrNotFound is returned by Repo implementations when the member (or
// phone binding) has no persisted preference.
var ErrNotFound = errors.New("prefs: not found")

// Repo is the durable backing store for member preferences. Get and
// MemberForPhone return ErrNotFound when nothing is stored; LinkPhone
// normalises the number with NormalizePhone before persisting.
type Repo interface {
	Set(ctx context.Context, tenantID, memberID string, loc i18n.Locale) error
	Get(ctx context.Context, tenantID, memberID string) (i18n.Locale, error)
	LinkPhone(ctx context.Context, tenantID, phone, memberID string) error
	MemberForPhone(ctx context.Context, tenantID, phone string) (string, error)
}

// PersistentStore is a write-through Store: mutations persist to the repo
// first and a repo error fails the mutation (memory untouched); reads
// prefer the repo and fall back to memory on transient repo errors or a
// nil repo (previous behaviour).
type PersistentStore struct {
	mem  *Store
	repo Repo
}

// NewPersistentStore wraps mem with write-through persistence to repo
// (nil repo = in-memory only).
func NewPersistentStore(mem *Store, repo Repo) *PersistentStore {
	return &PersistentStore{mem: mem, repo: repo}
}

// Set records the member's preferred locale in the repo (when configured)
// and in memory.
func (s *PersistentStore) Set(ctx context.Context, tenantID, memberID string, loc i18n.Locale) error {
	if s.repo != nil {
		if err := s.repo.Set(ctx, tenantID, memberID, loc); err != nil {
			return err
		}
	}
	s.mem.Set(memberID, loc)
	return nil
}

// Get returns the member's preferred locale (defaulting to English),
// preferring the repo and refreshing the in-memory copy.
func (s *PersistentStore) Get(ctx context.Context, tenantID, memberID string) i18n.Locale {
	if s.repo != nil {
		loc, err := s.repo.Get(ctx, tenantID, memberID)
		if err == nil {
			s.mem.Set(memberID, loc)
			return loc
		}
		if !errors.Is(err, ErrNotFound) {
			// transient repo error: fall through to the in-memory copy
			return s.mem.Get(memberID)
		}
	}
	return s.mem.Get(memberID)
}

// LinkPhone binds a phone number to a member ID (see Store.LinkPhone) in
// the repo (when configured) and in memory.
func (s *PersistentStore) LinkPhone(ctx context.Context, tenantID, phone, memberID string) error {
	if s.repo != nil {
		if err := s.repo.LinkPhone(ctx, tenantID, phone, memberID); err != nil {
			return err
		}
	}
	s.mem.LinkPhone(phone, memberID)
	return nil
}

// MemberForPhone resolves a phone number to a member ID, preferring the
// repo and refreshing the in-memory binding.
func (s *PersistentStore) MemberForPhone(ctx context.Context, tenantID, phone string) (string, bool) {
	if s.repo != nil {
		id, err := s.repo.MemberForPhone(ctx, tenantID, phone)
		if err == nil {
			s.mem.LinkPhone(phone, id)
			return id, true
		}
		if !errors.Is(err, ErrNotFound) {
			// transient repo error: fall through to the in-memory binding
			return s.mem.MemberForPhone(phone)
		}
		return "", false
	}
	return s.mem.MemberForPhone(phone)
}

// LocaleForPhone resolves the locale for an inbound channel sender:
// phone → member → locale, defaulting to English for unknown numbers.
func (s *PersistentStore) LocaleForPhone(ctx context.Context, tenantID, phone string) i18n.Locale {
	if id, ok := s.MemberForPhone(ctx, tenantID, phone); ok {
		return s.Get(ctx, tenantID, id)
	}
	return DefaultLocale
}
