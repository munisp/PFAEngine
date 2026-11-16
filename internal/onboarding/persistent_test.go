package onboarding

// Tranche 14 unit tests: write-through behaviour of PersistentStore with a
// fake Repo (records saves, injects errors), rollback on repo failure,
// memory fallback, load-through and ID-collision retry. No database needed.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/munisp/pfaengine/internal/audit"
)

// fakeRepo is an in-memory Repo recording every Save and able to inject
// errors on demand.
type fakeRepo struct {
	mu      sync.Mutex
	saves   []Case
	byID    map[string]Case // tenantID + "/" + id
	saveErr error
	loadErr error
	listErr error
}

func newFakeRepo() *fakeRepo { return &fakeRepo{byID: map[string]Case{}} }

func cloneCase(c Case) Case {
	cp := c
	cp.Steps = append([]Step{}, c.Steps...)
	cp.Docs = append([]Document{}, c.Docs...)
	return cp
}

func (f *fakeRepo) Save(_ context.Context, c Case) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saves = append(f.saves, cloneCase(c))
	f.byID[c.TenantID+"/"+c.ID] = cloneCase(c)
	return nil
}

func (f *fakeRepo) Load(_ context.Context, tenantID, id string) (Case, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loadErr != nil {
		return Case{}, f.loadErr
	}
	c, ok := f.byID[tenantID+"/"+id]
	if !ok {
		return Case{}, ErrCaseNotFound
	}
	return cloneCase(c), nil
}

func (f *fakeRepo) List(_ context.Context, tenantID string, stakeholder, status *string) ([]Case, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []Case
	for _, c := range f.byID {
		if c.TenantID != tenantID {
			continue
		}
		if stakeholder != nil && string(c.Stakeholder) != *stakeholder {
			continue
		}
		if status != nil && string(c.Status) != *status {
			continue
		}
		out = append(out, cloneCase(c))
	}
	return out, nil
}

func (f *fakeRepo) savedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saves)
}

func (f *fakeRepo) lastSaved(t *testing.T) Case {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.saves) == 0 {
		t.Fatal("fakeRepo: no saves recorded")
	}
	return cloneCase(f.saves[len(f.saves)-1])
}

func newPersistent(repo Repo) *PersistentStore {
	return NewPersistentStore(NewStore(audit.NewStore()), repo)
}

// advanceAllPersistent completes every required step in order.
func advanceAllPersistent(t *testing.T, p *PersistentStore, tenant, caseID string, sh Stakeholder) {
	t.Helper()
	for _, s := range Pipeline(sh) {
		if !s.Required {
			continue
		}
		if err := p.Advance(context.Background(), tenant, caseID, s.Name); err != nil {
			t.Fatalf("Advance(%s): %v", s.Name, err)
		}
	}
}

func TestPersistentCreateWriteThrough(t *testing.T) {
	repo := newFakeRepo()
	p := newPersistent(repo)
	ctx := context.Background()
	docs := []Document{{ID: "doc-1", Kind: "nin_slip", Ref: "s3://bucket/doc-1"}}
	c, err := p.Create(ctx, "t1", StakeholderMember, "ada", docs)
	if err != nil {
		t.Fatal(err)
	}
	if repo.savedCount() != 1 {
		t.Fatalf("saves = %d, want 1", repo.savedCount())
	}
	got := repo.lastSaved(t)
	if got.ID != c.ID || got.TenantID != "t1" || got.Stakeholder != StakeholderMember ||
		got.Status != StatusDraft || got.SubmittedBy != "ada" {
		t.Fatalf("saved case = %+v", got)
	}
	if len(got.Steps) != len(Pipeline(StakeholderMember)) || len(got.Docs) != 1 || got.Docs[0].ID != "doc-1" {
		t.Fatalf("saved steps/docs = %+v / %+v", got.Steps, got.Docs)
	}
}

func TestPersistentMutationWriteThrough(t *testing.T) {
	repo := newFakeRepo()
	p := newPersistent(repo)
	ctx := context.Background()
	// EMPLOYER exercises the maker-checker path: steps -> APPROVAL_PENDING
	// -> Approve -> ACTIVE (approval marker stamped).
	c, err := p.Create(ctx, "t1", StakeholderEmployer, "hr-user", nil)
	if err != nil {
		t.Fatal(err)
	}
	advanceAllPersistent(t, p, "t1", c.ID, StakeholderEmployer)
	got := repo.lastSaved(t)
	if got.Status != StatusApprovalPending {
		t.Fatalf("status after steps = %s, want APPROVAL_PENDING", got.Status)
	}
	if err := p.Approve(ctx, "t1", c.ID, "ops-user"); err != nil {
		t.Fatal(err)
	}
	got = repo.lastSaved(t)
	if got.Status != StatusActive || got.ApprovedBy != "ops-user" {
		t.Fatalf("saved after approve = %+v", got)
	}
	for _, s := range got.Steps {
		if !s.Done {
			t.Errorf("step %s not done after approval", s.Name)
		}
	}
	// Create + 4 step saves + approve save.
	if want := 1 + 4 + 1; repo.savedCount() != want {
		t.Fatalf("saves = %d, want %d", repo.savedCount(), want)
	}
	// LinkRef persists too.
	if err := p.LinkRef(ctx, "t1", c.ID, "emp-1"); err != nil {
		t.Fatal(err)
	}
	if got := repo.lastSaved(t); got.RefID != "emp-1" {
		t.Fatalf("RefID = %q, want emp-1", got.RefID)
	}
}

func TestPersistentRepoErrorFailsMutation(t *testing.T) {
	repo := newFakeRepo()
	p := newPersistent(repo)
	ctx := context.Background()
	boom := errors.New("db down")

	// Create fails and leaves nothing in memory.
	repo.saveErr = boom
	c, err := p.Create(ctx, "t1", StakeholderMember, "ada", nil)
	if !errors.Is(err, boom) {
		t.Fatalf("Create err = %v, want %v", err, boom)
	}
	if c != nil {
		t.Fatalf("Create case = %+v, want nil", c)
	}
	if got := p.mem.List("t1", "", ""); len(got) != 0 {
		t.Fatalf("memory has %d cases after failed Create, want 0", len(got))
	}

	// A failing Advance rolls the in-memory step back.
	repo.saveErr = nil
	c, err = p.Create(ctx, "t1", StakeholderMember, "ada", nil)
	if err != nil {
		t.Fatal(err)
	}
	repo.saveErr = boom
	if err := p.Advance(ctx, "t1", c.ID, "nin_verify"); !errors.Is(err, boom) {
		t.Fatalf("Advance err = %v, want %v", err, boom)
	}
	m, ok := p.mem.Get(c.ID)
	if !ok {
		t.Fatal("case missing from memory")
	}
	if m.Steps[0].Done || m.Status != StatusDraft {
		t.Fatalf("memory not rolled back: step done=%v status=%s", m.Steps[0].Done, m.Status)
	}
	// And the mutation succeeds once the repo recovers.
	repo.saveErr = nil
	if err := p.Advance(ctx, "t1", c.ID, "nin_verify"); err != nil {
		t.Fatalf("Advance after recovery: %v", err)
	}
}

func TestPersistentNilRepoIsInMemory(t *testing.T) {
	p := newPersistent(nil)
	ctx := context.Background()
	c, err := p.Create(ctx, "t1", StakeholderMember, "ada", nil)
	if err != nil {
		t.Fatal(err)
	}
	advanceAllPersistent(t, p, "t1", c.ID, StakeholderMember)
	got, ok := p.Get(ctx, "t1", c.ID)
	if !ok || got.Status != StatusActive {
		t.Fatalf("Get = %+v, %v; want ACTIVE", got, ok)
	}
	if list := p.List(ctx, "t1", StakeholderMember, StatusActive); len(list) != 1 {
		t.Fatalf("List = %d cases, want 1", len(list))
	}
}

func TestPersistentLoadThrough(t *testing.T) {
	repo := newFakeRepo()
	ctx := context.Background()
	// Process A persists a case.
	pa := newPersistent(repo)
	c, err := pa.Create(ctx, "t1", StakeholderMember, "ada", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := pa.Advance(ctx, "t1", c.ID, "nin_verify"); err != nil {
		t.Fatal(err)
	}
	// Process B: fresh memory, same repo. Get loads through...
	pb := newPersistent(repo)
	got, ok := pb.Get(ctx, "t1", c.ID)
	if !ok {
		t.Fatal("load-through Get: not found")
	}
	if got.Status != StatusKYCPending || !got.Steps[0].Done || got.Steps[0].Name != "nin_verify" {
		t.Fatalf("load-through case = %+v", got)
	}
	// ...and mutations hydrate the in-memory state machine first.
	if err := pb.Advance(ctx, "t1", c.ID, "bvn_validate"); err != nil {
		t.Fatalf("Advance after hydrate: %v", err)
	}
	if got := repo.lastSaved(t); !got.Steps[1].Done {
		t.Fatalf("saved step bvn_validate done=%v", got.Steps[1])
	}
	// Unknown case still 404s through the repo.
	if _, ok := pb.Get(ctx, "t1", "onb-999999"); ok {
		t.Fatal("Get unknown case: found")
	}
	if err := pb.Advance(ctx, "t1", "onb-999999", "nin_verify"); !errors.Is(err, ErrCaseNotFound) {
		t.Fatalf("Advance unknown case err = %v, want ErrCaseNotFound", err)
	}
}

func TestPersistentReadFallsBackToMemoryOnRepoError(t *testing.T) {
	repo := newFakeRepo()
	p := newPersistent(repo)
	ctx := context.Background()
	c, err := p.Create(ctx, "t1", StakeholderMember, "ada", nil)
	if err != nil {
		t.Fatal(err)
	}
	repo.loadErr = errors.New("connection reset")
	repo.listErr = errors.New("connection reset")
	if got, ok := p.Get(ctx, "t1", c.ID); !ok || got.ID != c.ID {
		t.Fatalf("Get with repo down = %+v, %v; want memory copy", got, ok)
	}
	if list := p.List(ctx, "t1", "", ""); len(list) != 1 {
		t.Fatalf("List with repo down = %d, want 1 (memory)", len(list))
	}
}

func TestPersistentListViaRepo(t *testing.T) {
	repo := newFakeRepo()
	p := newPersistent(repo)
	ctx := context.Background()
	if _, err := p.Create(ctx, "t1", StakeholderMember, "ada", nil); err != nil {
		t.Fatal(err)
	}
	emp, err := p.Create(ctx, "t1", StakeholderEmployer, "hr", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Create(ctx, "t2", StakeholderMember, "ada", nil); err != nil {
		t.Fatal(err)
	}
	all := p.List(ctx, "t1", "", "")
	if len(all) != 2 {
		t.Fatalf("List all = %d, want 2", len(all))
	}
	employers := p.List(ctx, "t1", StakeholderEmployer, "")
	if len(employers) != 1 || employers[0].ID != emp.ID {
		t.Fatalf("List EMPLOYER = %+v", employers)
	}
	drafts := p.List(ctx, "t1", "", StatusDraft)
	if len(drafts) != 2 {
		t.Fatalf("List DRAFT = %d, want 2", len(drafts))
	}
}

func TestPersistentCreateSkipsPersistedIDCollisions(t *testing.T) {
	repo := newFakeRepo()
	ctx := context.Background()
	// Simulate a previous process lifetime: onb-000001 already persisted.
	repo.byID["t1/onb-000001"] = Case{ID: "onb-000001", TenantID: "t1", Stakeholder: StakeholderMember, Status: StatusActive}
	p := newPersistent(repo) // fresh memory: sequence restarts at 1
	c, err := p.Create(ctx, "t1", StakeholderMember, "ada", nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "onb-000002" {
		t.Fatalf("Create ID = %q, want onb-000002 (collision skipped)", c.ID)
	}
	if got := repo.lastSaved(t); got.ID != "onb-000002" {
		t.Fatalf("saved ID = %q", got.ID)
	}
	// The pre-existing case is untouched.
	old, err := repo.Load(ctx, "t1", "onb-000001")
	if err != nil || old.Status != StatusActive {
		t.Fatalf("pre-existing case = %+v, %v", old, err)
	}
}
