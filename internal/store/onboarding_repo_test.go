package store

// Tranche 14 tests: the case validator runs everywhere; DB round-trips are
// gated on TEST_DATABASE_URL (apply db/migrations/0001_init.sql,
// 0002_tranche6.sql, then 0003_onboarding.sql first).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/i18n"
	"github.com/munisp/pfaengine/internal/onboarding"
	"github.com/munisp/pfaengine/internal/prefs"
)

func TestValidateOnboardingCase(t *testing.T) {
	valid := onboarding.Case{
		ID: "onb-1", TenantID: "t1", Stakeholder: onboarding.StakeholderMember,
		Status: onboarding.StatusDraft, SubmittedBy: "ada",
	}
	withDoc := valid
	withDoc.Docs = []onboarding.Document{{ID: "d1", Kind: "nin_slip", Ref: "s3://b/d1"}}
	badDoc := valid
	badDoc.Docs = []onboarding.Document{{ID: "d1"}}
	cases := []struct {
		name    string
		c       onboarding.Case
		wantErr bool
	}{
		{"valid", valid, false},
		{"valid with doc", withDoc, false},
		{"missing id", onboarding.Case{TenantID: "t1", Stakeholder: onboarding.StakeholderMember, Status: onboarding.StatusDraft, SubmittedBy: "a"}, true},
		{"missing tenant", onboarding.Case{ID: "c", Stakeholder: onboarding.StakeholderMember, Status: onboarding.StatusDraft, SubmittedBy: "a"}, true},
		{"missing stakeholder", onboarding.Case{ID: "c", TenantID: "t1", Status: onboarding.StatusDraft, SubmittedBy: "a"}, true},
		{"missing status", onboarding.Case{ID: "c", TenantID: "t1", Stakeholder: onboarding.StakeholderMember, SubmittedBy: "a"}, true},
		{"missing submitter", onboarding.Case{ID: "c", TenantID: "t1", Stakeholder: onboarding.StakeholderMember, Status: onboarding.StatusDraft}, true},
		{"doc missing kind/ref", badDoc, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateOnboardingCase(tc.c); (err != nil) != tc.wantErr {
				t.Fatalf("validateOnboardingCase(%+v) err=%v wantErr=%v", tc.c, err, tc.wantErr)
			}
		})
	}
}

// Integration test — runs only when TEST_DATABASE_URL is set.
func TestTranche14OnboardingRoundTrip(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Unique run suffix keeps the integration test idempotent across
	// repeated runs against a persistent database.
	run := fmt.Sprintf("%d", time.Now().UnixNano())
	tenant := "t14-" + run

	repo := NewOnboardingRepo(db)
	ps := onboarding.NewPersistentStore(onboarding.NewStore(audit.NewStore()), repo)

	// Full lifecycle: create (with a WORM document) -> advance all required
	// steps -> maker-checker approve -> reload and assert.
	docs := []onboarding.Document{{
		ID: "doc-" + run, Kind: "glip_policy", Ref: "s3://evidence/" + run,
		UploadedAt: time.Now().UTC(),
	}}
	c, err := ps.Create(ctx, tenant, onboarding.StakeholderEmployer, "hr-"+run, docs)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range onboarding.Pipeline(onboarding.StakeholderEmployer) {
		if !s.Required {
			continue
		}
		if err := ps.Advance(ctx, tenant, c.ID, s.Name); err != nil {
			t.Fatalf("Advance(%s): %v", s.Name, err)
		}
	}
	if err := ps.Approve(ctx, tenant, c.ID, "ops-"+run); err != nil {
		t.Fatal(err)
	}
	if err := ps.LinkRef(ctx, tenant, c.ID, "emp-"+run); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Load(ctx, tenant, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != onboarding.StatusActive || got.ApprovedBy != "ops-"+run ||
		got.RefID != "emp-"+run || got.SubmittedBy != "hr-"+run {
		t.Fatalf("Load = %+v", got)
	}
	// Steps reload in pipeline order, all done (approval marker included).
	tmpl := onboarding.Pipeline(onboarding.StakeholderEmployer)
	if len(got.Steps) != len(tmpl) {
		t.Fatalf("steps = %d, want %d", len(got.Steps), len(tmpl))
	}
	for i, s := range got.Steps {
		if s.Name != tmpl[i].Name || !s.Done || s.At.IsZero() {
			t.Fatalf("step %d = %+v, want %s done", i, s, tmpl[i].Name)
		}
	}
	if len(got.Docs) != 1 || got.Docs[0].ID != "doc-"+run || got.Docs[0].Ref != "s3://evidence/"+run {
		t.Fatalf("docs = %+v", got.Docs)
	}

	// Load-through: a fresh in-memory store over the same repo sees the
	// case (restart semantics).
	ps2 := onboarding.NewPersistentStore(onboarding.NewStore(audit.NewStore()), repo)
	if got2, ok := ps2.Get(ctx, tenant, c.ID); !ok || got2.Status != onboarding.StatusActive {
		t.Fatalf("load-through Get = %+v, %v", got2, ok)
	}

	// List filters.
	sh, st := string(onboarding.StakeholderEmployer), string(onboarding.StatusActive)
	list, err := repo.List(ctx, tenant, &sh, &st)
	if err != nil || len(list) != 1 || list[0].ID != c.ID {
		t.Fatalf("List = %+v, %v", list, err)
	}
	if list, err := repo.List(ctx, tenant, nil, nil); err != nil || len(list) != 1 {
		t.Fatalf("List unfiltered = %d, %v", len(list), err)
	}
	rejected := string(onboarding.StatusRejected)
	if list, err := repo.List(ctx, tenant, nil, &rejected); err != nil || len(list) != 0 {
		t.Fatalf("List REJECTED = %d, %v", len(list), err)
	}

	// WORM enforcement: UPDATE and DELETE on onboarding_document fail (the
	// migration trigger rejects them for every role, owner included).
	if _, err := db.sql.ExecContext(ctx,
		`UPDATE onboarding_document SET uri='tampered' WHERE id=$1`, "doc-"+run); err == nil {
		t.Fatal("UPDATE on WORM onboarding_document succeeded")
	}
	if _, err := db.sql.ExecContext(ctx,
		`DELETE FROM onboarding_document WHERE id=$1`, "doc-"+run); err == nil {
		t.Fatal("DELETE on WORM onboarding_document succeeded")
	}

	// Re-saving with the same document is idempotent (ON CONFLICT DO
	// NOTHING), not a WORM violation.
	if err := repo.Save(ctx, got); err != nil {
		t.Fatalf("re-Save with existing doc: %v", err)
	}

	// Tenant isolation: another tenant sees nothing.
	if _, err := repo.Load(ctx, "other-tenant", c.ID); !errors.Is(err, onboarding.ErrCaseNotFound) {
		t.Fatalf("cross-tenant Load should be ErrCaseNotFound, got %v", err)
	}
}

// Integration test — runs only when TEST_DATABASE_URL is set.
func TestTranche14PrefsRoundTrip(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	run := fmt.Sprintf("%d", time.Now().UnixNano())
	tenant, memberID := "t14p-"+run, "mem-t14-"+run
	repo := NewPrefsRepo(db)

	// Nothing stored yet.
	if _, err := repo.Get(ctx, tenant, memberID); !errors.Is(err, prefs.ErrNotFound) {
		t.Fatalf("Get unset = %v, want prefs.ErrNotFound", err)
	}

	// Locale upsert round-trip.
	if err := repo.Set(ctx, tenant, memberID, i18n.Yoruba); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.Get(ctx, tenant, memberID); err != nil || got != i18n.Yoruba {
		t.Fatalf("Get = %q, %v; want yo", got, err)
	}
	if err := repo.Set(ctx, tenant, memberID, i18n.Pidgin); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.Get(ctx, tenant, memberID); err != nil || got != i18n.Pidgin {
		t.Fatalf("Get after upsert = %q, %v; want pcm-NG", got, err)
	}

	// Phone binding with E.164 normalisation: "+234 ..." stored, bare-digit
	// webhook sender resolves.
	suffix := run[len(run)-9:]
	phone := "+234 803-" + suffix
	if err := repo.LinkPhone(ctx, tenant, phone, memberID); err != nil {
		t.Fatal(err)
	}
	digits := "234803" + suffix
	id, err := repo.MemberForPhone(ctx, tenant, digits)
	if err != nil || id != memberID {
		t.Fatalf("MemberForPhone = %q, %v; want %q", id, err, memberID)
	}
	if got := repo.LocaleForPhone(ctx, tenant, digits); got != i18n.Pidgin {
		t.Fatalf("LocaleForPhone = %q, want pcm-NG", got)
	}
	if got := repo.LocaleForPhone(ctx, tenant, "2348000000000"); got != prefs.DefaultLocale {
		t.Fatalf("LocaleForPhone unknown = %q, want %q", got, prefs.DefaultLocale)
	}

	// Re-linking normalises identically (upsert, no duplicate row).
	if err := repo.LinkPhone(ctx, tenant, strings.ReplaceAll(digits, "234", "+234"), memberID); err != nil {
		t.Fatal(err)
	}

	// Tenant isolation.
	if _, err := repo.MemberForPhone(ctx, "other-tenant", digits); !errors.Is(err, prefs.ErrNotFound) {
		t.Fatalf("cross-tenant MemberForPhone = %v, want prefs.ErrNotFound", err)
	}
}
