package onboarding

// Tranche 14: durable, write-through persistence for the onboarding state
// machine. PersistentStore wraps the in-memory Store (which keeps the
// domain rules and audit-chain appends) with a Repo (implemented by
// internal/store.OnboardingRepo against Postgres). The in-memory Store is
// unchanged and remains fully usable on its own.

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// Repo is the durable backing store for onboarding cases. Save upserts the
// full case (steps included) and appends new documents; Load returns
// ErrCaseNotFound when the case is absent (or cross-tenant); List filters
// by stakeholder/status with a nil wildcard.
type Repo interface {
	Save(ctx context.Context, c Case) error
	Load(ctx context.Context, tenantID, id string) (Case, error)
	List(ctx context.Context, tenantID string, stakeholder, status *string) ([]Case, error)
}

// PersistentStore is a write-through Store: every mutation is applied to
// the in-memory state machine first (preserving validation and the audit
// appends) and then saved to the repo; a repo error fails the mutation and
// the in-memory change is rolled back. (The audit append for the attempted
// mutation is retained — the chain is append-only.) Reads prefer the repo
// and fall back to memory on transient repo errors; with a nil repo the
// store is purely in-memory (previous behaviour).
type PersistentStore struct {
	mem  *Store
	repo Repo
}

// NewPersistentStore wraps mem with write-through persistence to repo
// (nil repo = in-memory only).
func NewPersistentStore(mem *Store, repo Repo) *PersistentStore {
	return &PersistentStore{mem: mem, repo: repo}
}

// Create opens a case (see Store.Create) and persists it. Because mem's ID
// sequence restarts at 1 in a fresh process, Create skips IDs already
// present in the repo (collisions with earlier process lifetimes).
func (p *PersistentStore) Create(ctx context.Context, tenantID string, sh Stakeholder, submittedBy string, docs []Document) (*Case, error) {
	for {
		c, err := p.mem.Create(tenantID, sh, submittedBy, docs)
		if err != nil {
			return nil, err
		}
		if p.repo == nil {
			return c, nil
		}
		if _, err := p.repo.Load(ctx, tenantID, c.ID); err == nil {
			// ID collision with a previously persisted case: drop the
			// in-memory case and retry with the next sequence number.
			p.restore(c.ID, nil)
			continue
		} else if !errors.Is(err, ErrCaseNotFound) {
			p.restore(c.ID, nil)
			return nil, err
		}
		if err := p.repo.Save(ctx, *c); err != nil {
			p.restore(c.ID, nil)
			return nil, err
		}
		return c, nil
	}
}

// Get returns the case, preferring the repo; on a transient repo error it
// degrades to the in-memory copy. With a nil repo it is Store.Get.
func (p *PersistentStore) Get(ctx context.Context, tenantID, id string) (*Case, bool) {
	if p.repo != nil {
		c, err := p.repo.Load(ctx, tenantID, id)
		if err == nil {
			p.cache(c)
			cp := c
			return &cp, true
		}
		if errors.Is(err, ErrCaseNotFound) {
			return nil, false
		}
		// transient repo error: fall through to the in-memory copy
	}
	return p.mem.Get(id)
}

// List returns the tenant's cases (see Store.List), preferring the repo
// and degrading to memory on repo error.
func (p *PersistentStore) List(ctx context.Context, tenantID string, sh Stakeholder, st Status) []*Case {
	if p.repo != nil {
		var shp, stp *string
		if sh != "" {
			s := string(sh)
			shp = &s
		}
		if st != "" {
			s := string(st)
			stp = &s
		}
		if cases, err := p.repo.List(ctx, tenantID, shp, stp); err == nil {
			out := make([]*Case, 0, len(cases))
			for _, c := range cases {
				p.cache(c)
				cp := c
				out = append(out, &cp)
			}
			return out
		}
		// repo error: fall through to the in-memory list
	}
	return p.mem.List(tenantID, sh, st)
}

// Advance completes a required pipeline step (see Store.Advance) and
// persists the resulting case.
func (p *PersistentStore) Advance(ctx context.Context, tenantID, caseID, step string) error {
	if err := p.hydrate(ctx, tenantID, caseID); err != nil {
		return err
	}
	prev, _ := p.mem.Get(caseID)
	if err := p.mem.Advance(caseID, step); err != nil {
		return err
	}
	return p.save(ctx, caseID, prev)
}

// Approve moves an APPROVAL_PENDING case to ACTIVE (see Store.Approve) and
// persists the resulting case.
func (p *PersistentStore) Approve(ctx context.Context, tenantID, caseID, approver string) error {
	if err := p.hydrate(ctx, tenantID, caseID); err != nil {
		return err
	}
	prev, _ := p.mem.Get(caseID)
	if err := p.mem.Approve(caseID, approver); err != nil {
		return err
	}
	return p.save(ctx, caseID, prev)
}

// Reject moves a non-terminal case to REJECTED (see Store.Reject) and
// persists the resulting case.
func (p *PersistentStore) Reject(ctx context.Context, tenantID, caseID, approver, reason string) error {
	if err := p.hydrate(ctx, tenantID, caseID); err != nil {
		return err
	}
	prev, _ := p.mem.Get(caseID)
	if err := p.mem.Reject(caseID, approver, reason); err != nil {
		return err
	}
	return p.save(ctx, caseID, prev)
}

// Suspend moves an ACTIVE case to SUSPENDED (see Store.Suspend) and
// persists the resulting case.
func (p *PersistentStore) Suspend(ctx context.Context, tenantID, caseID, actor string) error {
	if err := p.hydrate(ctx, tenantID, caseID); err != nil {
		return err
	}
	prev, _ := p.mem.Get(caseID)
	if err := p.mem.Suspend(caseID, actor); err != nil {
		return err
	}
	return p.save(ctx, caseID, prev)
}

// LinkRef binds the case to its provisioned entity (see Store.LinkRef) and
// persists the resulting case.
func (p *PersistentStore) LinkRef(ctx context.Context, tenantID, caseID, refID string) error {
	if err := p.hydrate(ctx, tenantID, caseID); err != nil {
		return err
	}
	prev, _ := p.mem.Get(caseID)
	if err := p.mem.LinkRef(caseID, refID); err != nil {
		return err
	}
	return p.save(ctx, caseID, prev)
}

// save persists the current in-memory state of the case; on a repo error
// the in-memory state is rolled back to prev (nil prev = case removed) so
// memory and the durable store stay consistent.
func (p *PersistentStore) save(ctx context.Context, caseID string, prev *Case) error {
	if p.repo == nil {
		return nil
	}
	c, ok := p.mem.Get(caseID)
	if !ok {
		return ErrCaseNotFound
	}
	if err := p.repo.Save(ctx, *c); err != nil {
		p.restore(caseID, prev)
		return err
	}
	return nil
}

// hydrate loads the case from the repo into memory when it is not already
// resident (e.g. after a process restart) so the in-memory state machine
// can mutate it. No-op when the repo is nil or the case is resident.
func (p *PersistentStore) hydrate(ctx context.Context, tenantID, id string) error {
	if p.repo == nil {
		return nil
	}
	p.mem.mu.Lock()
	_, ok := p.mem.cases[id]
	p.mem.mu.Unlock()
	if ok {
		return nil
	}
	c, err := p.repo.Load(ctx, tenantID, id)
	if err != nil {
		return err
	}
	p.cache(c)
	return nil
}

// cache inserts (or refreshes) the in-memory copy of a repo-loaded case
// and advances the ID sequence past it so later Creates cannot collide.
func (p *PersistentStore) cache(c Case) {
	p.mem.mu.Lock()
	defer p.mem.mu.Unlock()
	cp := copyCase(&c)
	p.mem.cases[c.ID] = cp
	if n, err := strconv.Atoi(strings.TrimPrefix(c.ID, "onb-")); err == nil && n > p.mem.seq {
		p.mem.seq = n
	}
}

// restore rolls the in-memory case back to prev (a copy from Store.Get);
// nil prev removes the case (failed Create).
func (p *PersistentStore) restore(id string, prev *Case) {
	p.mem.mu.Lock()
	defer p.mem.mu.Unlock()
	if prev == nil {
		delete(p.mem.cases, id)
		return
	}
	p.mem.cases[id] = prev
}
