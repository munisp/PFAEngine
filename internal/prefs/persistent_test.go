package prefs

// Tranche 14 unit tests: write-through behaviour of PersistentStore with a
// fake Repo (records writes, injects errors), read preference, memory
// fallback and E.164 normalisation. No database needed.

import (
	"context"
	"errors"
	"testing"

	"github.com/munisp/pfaengine/internal/i18n"
)

// fakePrefsRepo is an in-memory Repo able to inject errors on demand.
type fakePrefsRepo struct {
	locales map[string]i18n.Locale // tenantID + "/" + memberID
	phones  map[string]string      // tenantID + "/" + normalised phone -> memberID
	err     error
}

func newFakePrefsRepo() *fakePrefsRepo {
	return &fakePrefsRepo{locales: map[string]i18n.Locale{}, phones: map[string]string{}}
}

func (f *fakePrefsRepo) Set(_ context.Context, tenantID, memberID string, loc i18n.Locale) error {
	if f.err != nil {
		return f.err
	}
	f.locales[tenantID+"/"+memberID] = loc
	return nil
}

func (f *fakePrefsRepo) Get(_ context.Context, tenantID, memberID string) (i18n.Locale, error) {
	if f.err != nil {
		return "", f.err
	}
	loc, ok := f.locales[tenantID+"/"+memberID]
	if !ok {
		return "", ErrNotFound
	}
	return loc, nil
}

func (f *fakePrefsRepo) LinkPhone(_ context.Context, tenantID, phone, memberID string) error {
	if f.err != nil {
		return f.err
	}
	phone = NormalizePhone(phone)
	if phone == "" || memberID == "" {
		return nil
	}
	f.phones[tenantID+"/"+phone] = memberID
	return nil
}

func (f *fakePrefsRepo) MemberForPhone(_ context.Context, tenantID, phone string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	id, ok := f.phones[tenantID+"/"+NormalizePhone(phone)]
	if !ok {
		return "", ErrNotFound
	}
	return id, nil
}

func TestPersistentSetGetWriteThrough(t *testing.T) {
	repo := newFakePrefsRepo()
	s := NewPersistentStore(NewStore(), repo)
	ctx := context.Background()
	if err := s.Set(ctx, "t1", "mem-1", i18n.Yoruba); err != nil {
		t.Fatal(err)
	}
	if repo.locales["t1/mem-1"] != i18n.Yoruba {
		t.Fatalf("repo locale = %q, want yo", repo.locales["t1/mem-1"])
	}
	if got := s.Get(ctx, "t1", "mem-1"); got != i18n.Yoruba {
		t.Fatalf("Get = %q, want yo", got)
	}
}

func TestPersistentSetRepoErrorFailsMutation(t *testing.T) {
	repo := newFakePrefsRepo()
	s := NewPersistentStore(NewStore(), repo)
	ctx := context.Background()
	boom := errors.New("db down")
	repo.err = boom
	if err := s.Set(ctx, "t1", "mem-1", i18n.Hausa); !errors.Is(err, boom) {
		t.Fatalf("Set err = %v, want %v", err, boom)
	}
	// Memory untouched: still the default.
	if got := s.mem.Get("mem-1"); got != DefaultLocale {
		t.Fatalf("memory locale after failed Set = %q, want %q", got, DefaultLocale)
	}
	if err := s.LinkPhone(ctx, "t1", "+2348030000000", "mem-1"); !errors.Is(err, boom) {
		t.Fatalf("LinkPhone err = %v, want %v", err, boom)
	}
	if _, ok := s.mem.MemberForPhone("2348030000000"); ok {
		t.Fatal("memory phone binding written despite repo error")
	}
}

func TestPersistentGetPrefersRepo(t *testing.T) {
	repo := newFakePrefsRepo()
	repo.locales["t1/mem-1"] = i18n.Pidgin
	s := NewPersistentStore(NewStore(), repo) // empty memory
	if got := s.Get(context.Background(), "t1", "mem-1"); got != i18n.Pidgin {
		t.Fatalf("Get = %q, want pcm-NG", got)
	}
	// Repo value is cached back into memory.
	if got := s.mem.Get("mem-1"); got != i18n.Pidgin {
		t.Fatalf("memory cache = %q, want pcm-NG", got)
	}
}

func TestPersistentGetFallsBackToMemoryOnRepoError(t *testing.T) {
	repo := newFakePrefsRepo()
	s := NewPersistentStore(NewStore(), repo)
	ctx := context.Background()
	if err := s.Set(ctx, "t1", "mem-1", i18n.Igbo); err != nil {
		t.Fatal(err)
	}
	repo.err = errors.New("connection reset")
	if got := s.Get(ctx, "t1", "mem-1"); got != i18n.Igbo {
		t.Fatalf("Get with repo down = %q, want ig (memory)", got)
	}
	if id, ok := s.MemberForPhone(ctx, "t1", "2348030000000"); ok || id != "" {
		t.Fatalf("MemberForPhone with repo down = %q, %v", id, ok)
	}
}

func TestPersistentPhoneRoundTripNormalised(t *testing.T) {
	repo := newFakePrefsRepo()
	s := NewPersistentStore(NewStore(), repo)
	ctx := context.Background()
	if err := s.LinkPhone(ctx, "t1", "+234 803-000-0000", "mem-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "t1", "mem-1", i18n.Hausa); err != nil {
		t.Fatal(err)
	}
	// WhatsApp senders arrive as bare digits.
	id, ok := s.MemberForPhone(ctx, "t1", "2348030000000")
	if !ok || id != "mem-1" {
		t.Fatalf("MemberForPhone = %q, %v; want mem-1", id, ok)
	}
	if got := s.LocaleForPhone(ctx, "t1", "2348030000000"); got != i18n.Hausa {
		t.Fatalf("LocaleForPhone = %q, want ha", got)
	}
	if got := s.LocaleForPhone(ctx, "t1", "2348099999999"); got != DefaultLocale {
		t.Fatalf("LocaleForPhone unknown = %q, want %q", got, DefaultLocale)
	}
	// Tenant isolation: the binding does not leak across tenants.
	if _, ok := s.MemberForPhone(ctx, "t2", "2348030000000"); ok {
		t.Fatal("cross-tenant phone lookup succeeded")
	}
}

func TestPersistentNilRepoIsInMemory(t *testing.T) {
	s := NewPersistentStore(NewStore(), nil)
	ctx := context.Background()
	if err := s.Set(ctx, "t1", "mem-1", i18n.Yoruba); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkPhone(ctx, "t1", "+2348030000000", "mem-1"); err != nil {
		t.Fatal(err)
	}
	if got := s.Get(ctx, "t1", "mem-1"); got != i18n.Yoruba {
		t.Fatalf("Get = %q, want yo", got)
	}
	if got := s.LocaleForPhone(ctx, "t1", "2348030000000"); got != i18n.Yoruba {
		t.Fatalf("LocaleForPhone = %q, want yo", got)
	}
}
