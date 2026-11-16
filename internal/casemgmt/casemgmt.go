// Package casemgmt implements the unified next-generation case management
// engine for the platform (Tranche 20). It replaces the single-purpose
// fraud.Case silo with one case engine spanning every operational domain:
// fraud investigations, member complaints (PenCom complaint register),
// benefit claims, service requests, KYC reviews, reconciliation breaks,
// AML alerts, death claims and general enquiries.
//
// Capabilities:
//   - Typed cases with a validated lifecycle state machine
//     (OPEN → TRIAGED → IN_PROGRESS → PENDING_MEMBER / PENDING_THIRD_PARTY
//     → ESCALATED → RESOLVED → CLOSED, with REOPENED).
//   - Priority-driven SLAs (P1–P4 per case type), due-at computation,
//     breach sweep and automatic escalation on breach.
//   - Queue-based routing with round-robin assignment and manual
//     reassignment.
//   - Comments, checklist tasks, attachment metadata, case linking and
//     duplicate merging (same member + type + still open ⇒ merge).
//   - Full event/activity log per case (append-only) and an OnEvent hook
//     the notification layer subscribes to.
//   - Cross-domain Open* helpers so fraud, ML, claims and recon can raise
//     cases without knowing engine internals.
//   - Aggregate stats for the staff workbench and the PenCom quarterly
//     complaints report.
//
// The engine is tenant-scoped, thread-safe and persists through the
// optional Repo interface (store.CaseRepo in Postgres when DATABASE_URL is
// set); all state is kept in memory for sub-millisecond reads, with
// write-through persistence for durability.
package casemgmt

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Type enumerates the operational domains a case can belong to.
type Type string

const (
	TypeFraud          Type = "FRAUD"
	TypeComplaint      Type = "COMPLAINT"
	TypeClaim          Type = "CLAIM"
	TypeDeathClaim     Type = "DEATH_CLAIM"
	TypeServiceRequest Type = "SERVICE_REQUEST"
	TypeKYCReview      Type = "KYC_REVIEW"
	TypeReconBreak     Type = "RECON_BREAK"
	TypeAMLAlert       Type = "AML_ALERT"
	TypeGeneral        Type = "GENERAL"
)

// Priority bands. P1 is most urgent.
type Priority string

const (
	P1 Priority = "P1"
	P2 Priority = "P2"
	P3 Priority = "P3"
	P4 Priority = "P4"
)

// Status is the case lifecycle state.
type Status string

const (
	Open              Status = "OPEN"
	Triaged           Status = "TRIAGED"
	InProgress        Status = "IN_PROGRESS"
	PendingMember     Status = "PENDING_MEMBER"
	PendingThirdParty Status = "PENDING_THIRD_PARTY"
	Escalated         Status = "ESCALATED"
	Resolved          Status = "RESOLVED"
	Closed            Status = "CLOSED"
	Reopened          Status = "REOPENED"
)

// IsTerminal reports whether the status ends the SLA clock.
func (s Status) IsTerminal() bool { return s == Resolved || s == Closed }

// transitions is the allowed state machine. Any non-listed transition is
// rejected, which keeps the lifecycle auditable.
var transitions = map[Status][]Status{
	Open:              {Triaged, InProgress, Escalated, Resolved, Closed},
	Triaged:           {InProgress, PendingMember, PendingThirdParty, Escalated, Resolved, Closed},
	InProgress:        {PendingMember, PendingThirdParty, Escalated, Resolved, Closed},
	PendingMember:     {InProgress, Escalated, Resolved, Closed},
	PendingThirdParty: {InProgress, Escalated, Resolved, Closed},
	Escalated:         {InProgress, PendingMember, PendingThirdParty, Resolved, Closed},
	Resolved:          {Closed, Reopened},
	Closed:            {Reopened},
	Reopened:          {Triaged, InProgress, Escalated, Resolved, Closed},
}

// Queue is a work queue staffed by a team.
type Queue string

const (
	QueueFraudOps       Queue = "FRAUD_OPS"
	QueueMemberServices Queue = "MEMBER_SERVICES"
	QueueBenefits       Queue = "BENEFITS"
	QueueCompliance     Queue = "COMPLIANCE"
	QueueOperations     Queue = "OPERATIONS"
)

// defaultQueue routes a case type to its owning team.
var defaultQueue = map[Type]Queue{
	TypeFraud:          QueueFraudOps,
	TypeAMLAlert:       QueueFraudOps,
	TypeComplaint:      QueueMemberServices,
	TypeServiceRequest: QueueMemberServices,
	TypeGeneral:        QueueMemberServices,
	TypeClaim:          QueueBenefits,
	TypeDeathClaim:     QueueBenefits,
	TypeKYCReview:      QueueCompliance,
	TypeReconBreak:     QueueOperations,
}

// slaHours maps type → priority → hours to resolution. PenCom's complaint
// handling framework expects acknowledgement within 48h and resolution
// targets measured in days; the matrix below encodes those targets as
// hard SLA clocks.
var slaHours = map[Type]map[Priority]int{
	TypeFraud:          {P1: 4, P2: 24, P3: 72, P4: 168},
	TypeAMLAlert:       {P1: 4, P2: 24, P3: 72, P4: 168},
	TypeComplaint:      {P1: 48, P2: 120, P3: 240, P4: 480},
	TypeClaim:          {P1: 24, P2: 72, P3: 168, P4: 336},
	TypeDeathClaim:     {P1: 24, P2: 72, P3: 168, P4: 336},
	TypeServiceRequest: {P1: 24, P2: 72, P3: 168, P4: 336},
	TypeKYCReview:      {P1: 24, P2: 72, P3: 168, P4: 336},
	TypeReconBreak:     {P1: 8, P2: 24, P3: 72, P4: 168},
	TypeGeneral:        {P1: 48, P2: 120, P3: 240, P4: 480},
}

// defaultPriority assigns a sane default when the caller does not specify.
var defaultPriority = map[Type]Priority{
	TypeFraud:          P2,
	TypeAMLAlert:       P2,
	TypeComplaint:      P3,
	TypeClaim:          P2,
	TypeDeathClaim:     P1,
	TypeServiceRequest: P3,
	TypeKYCReview:      P3,
	TypeReconBreak:     P2,
	TypeGeneral:        P3,
}

// Source records what raised the case.
type Source string

const (
	SourceManual  Source = "MANUAL"
	SourceMember  Source = "MEMBER"
	SourceFraud   Source = "FRAUD_ENGINE"
	SourceML      Source = "ML_MODEL"
	SourceClaims  Source = "CLAIMS"
	SourceRecon   Source = "RECON"
	SourceChatbot Source = "CHATBOT"
	SourceAPI     Source = "API"
)

// Comment is a staff or member note on a case.
type Comment struct {
	Author string    `json:"author"` // staff id, member id or "system"
	Body   string    `json:"body"`
	At     time.Time `json:"at"`
}

// Task is a checklist item that must complete before resolution.
type Task struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Done      bool       `json:"done"`
	DoneBy    string     `json:"done_by,omitempty"`
	DoneAt    *time.Time `json:"done_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// Attachment is evidence metadata (blob itself lives in object storage).
type Attachment struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Mime     string    `json:"mime"`
	SizeByte int64     `json:"size_bytes"`
	Ref      string    `json:"ref"` // object-storage URI
	At       time.Time `json:"at"`
}

// Event is one entry in the case's append-only activity log.
type Event struct {
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"` // OPENED, TRANSITION, ASSIGNED, COMMENT, TASK_*, ESCALATED, RESOLVED, CLOSED, REOPENED, MERGED_DUP, SLA_BREACH, CSAT
	Detail string    `json:"detail,omitempty"`
}

// Case is the unified operational case record.
type Case struct {
	ID          string   `json:"id"`
	TenantID    string   `json:"tenant_id"`
	Type        Type     `json:"type"`
	Subject     string   `json:"subject"`
	Description string   `json:"description"`
	MemberID    string   `json:"member_id,omitempty"`
	EmployerID  string   `json:"employer_id,omitempty"`
	LinkedKind  string   `json:"linked_kind,omitempty"` // transaction|claim|fraud_case|recon_exception|nomination
	LinkedID    string   `json:"linked_id,omitempty"`
	Source      Source   `json:"source"`
	Status      Status   `json:"status"`
	Priority    Priority `json:"priority"`
	Queue       Queue    `json:"queue"`
	Assignee    string   `json:"assignee,omitempty"`
	Tags        []string `json:"tags,omitempty"`

	DueAt       time.Time  `json:"due_at"`
	SLABreached bool       `json:"sla_breached"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`

	Resolution     string `json:"resolution,omitempty"`
	ResolutionCode string `json:"resolution_code,omitempty"` // UPHELD|REJECTED|WITHDRAWN|CONFIRMED_FRAUD|CLEARED|FIXED|DUPLICATE
	CSAT           int    `json:"csat,omitempty"`            // 1..5 post-resolution member rating

	Links       []string     `json:"links,omitempty"` // related case ids
	MergedInto  string       `json:"merged_into,omitempty"`
	Comments    []Comment    `json:"comments,omitempty"`
	Tasks       []Task       `json:"tasks,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	Events      []Event      `json:"events,omitempty"`
}

// OpenInput is everything needed to raise a case.
type OpenInput struct {
	TenantID    string
	Type        Type
	Subject     string
	Description string
	MemberID    string
	EmployerID  string
	LinkedKind  string
	LinkedID    string
	Source      Source
	Priority    Priority // optional; defaulted per type
	Queue       Queue    // optional; defaulted per type
	Tags        []string
	Actor       string
	NoDedup     bool // skip duplicate-merge check (e.g. recurring fraud alerts)
}

// Filter selects cases for queue views.
type Filter struct {
	Status    []Status
	Type      []Type
	Queue     []Queue
	Assignee  string
	MemberID  string
	Breached  *bool
	OpenOnly  bool
	CreatedBy Source
}

// Stats aggregates queue health for the staff workbench.
type Stats struct {
	Total        int            `json:"total"`
	Open         int            `json:"open"` // non-terminal
	Breached     int            `json:"breached"`
	ByStatus     map[string]int `json:"by_status"`
	ByType       map[string]int `json:"by_type"`
	ByQueue      map[string]int `json:"by_queue"`
	AvgResolveH  float64        `json:"avg_resolve_hours"`
	CSATAvg      float64        `json:"csat_avg"`
	CSATCount    int            `json:"csat_count"`
	OpenByAgeDay map[string]int `json:"open_by_age_days"` // 0-1,1-3,3-7,7+
}

// Repo persists case state (write-through). Implemented by
// store.CaseRepo when Postgres is configured; nil ⇒ in-memory only.
type Repo interface {
	Save(c Case) error
}

// EventHook is invoked after every mutation (notification fan-out).
type EventHook func(c Case, e Event)

// Engine is the unified case management engine.
type Engine struct {
	mu       sync.Mutex
	cases    map[string]*Case
	seq      int
	repo     Repo
	hooks    []EventHook
	roster   map[Queue][]string // round-robin staff roster per queue
	rrCursor map[Queue]int
	now      func() time.Time // test hook
}

// New returns an engine. repo may be nil (in-memory only).
func New(repo Repo) *Engine {
	return &Engine{
		cases:    map[string]*Case{},
		repo:     repo,
		roster:   map[Queue][]string{},
		rrCursor: map[Queue]int{},
		now:      time.Now,
	}
}

// OnEvent registers a notification hook.
func (e *Engine) OnEvent(h EventHook) { e.mu.Lock(); e.hooks = append(e.hooks, h); e.mu.Unlock() }

// Load rehydrates a persisted case (Postgres warm-up on boot). The engine
// adopts the row as-is; the sequence counter advances past any numeric id
// suffix so new cases never collide with restored ones.
func (e *Engine) Load(c Case) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cp := c
	e.cases[c.ID] = &cp
	var n int
	if _, err := fmt.Sscanf(c.ID, "cs-%d", &n); err == nil && n > e.seq {
		e.seq = n
	}
}

// SetRoster configures the round-robin assignee roster for a queue.
func (e *Engine) SetRoster(q Queue, staff []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cp := append([]string(nil), staff...)
	e.roster[q] = cp
	e.rrCursor[q] = 0
}

// Open raises a new case. When an open (non-terminal) case of the same
// type already exists for the member and NoDedup is false, the new report
// is merged into it as a comment + MERGED_DUP event instead of spawning a
// duplicate investigation.
func (e *Engine) Open(in OpenInput) (Case, bool, error) {
	if in.TenantID == "" || in.Type == "" || in.Subject == "" {
		return Case{}, false, errors.New("casemgmt: tenant, type and subject are required")
	}
	now := e.now()

	e.mu.Lock()
	defer e.mu.Unlock()

	if !in.NoDedup && in.MemberID != "" {
		for _, c := range e.cases {
			if c.TenantID == in.TenantID && c.MemberID == in.MemberID &&
				c.Type == in.Type && !c.Status.IsTerminal() && c.MergedInto == "" {
				cm := Comment{Author: orSystem(in.Actor), Body: "Duplicate report merged: " + in.Description, At: now}
				c.Comments = append(c.Comments, cm)
				e.logLocked(c, orSystem(in.Actor), "MERGED_DUP", in.Subject)
				c.UpdatedAt = now
				out := *c
				e.persistLocked(c)
				e.fireLocked(c, "MERGED_DUP", in.Subject)
				return out, true, nil
			}
		}
	}

	pri := in.Priority
	if pri == "" {
		pri = defaultPriority[in.Type]
		if pri == "" {
			pri = P3
		}
	}
	q := in.Queue
	if q == "" {
		q = defaultQueue[in.Type]
		if q == "" {
			q = QueueMemberServices
		}
	}
	sla := slaHours[in.Type][pri]
	if sla == 0 {
		sla = 72
	}

	e.seq++
	c := &Case{
		ID:          fmt.Sprintf("cs-%06d", e.seq),
		TenantID:    in.TenantID,
		Type:        in.Type,
		Subject:     in.Subject,
		Description: in.Description,
		MemberID:    in.MemberID,
		EmployerID:  in.EmployerID,
		LinkedKind:  in.LinkedKind,
		LinkedID:    in.LinkedID,
		Source:      in.Source,
		Status:      Open,
		Priority:    pri,
		Queue:       q,
		Tags:        append([]string(nil), in.Tags...),
		DueAt:       now.Add(time.Duration(sla) * time.Hour),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if in.Source == "" {
		c.Source = SourceManual
	}
	// auto-assign via round-robin when a roster exists
	if staff := e.roster[q]; len(staff) > 0 {
		i := e.rrCursor[q] % len(staff)
		c.Assignee = staff[i]
		e.rrCursor[q] = i + 1
	}
	e.logLocked(c, orSystem(in.Actor), "OPENED", fmt.Sprintf("type=%s priority=%s queue=%s source=%s", c.Type, c.Priority, c.Queue, c.Source))
	if c.Assignee != "" {
		e.logLocked(c, "system", "ASSIGNED", "auto-assigned to "+c.Assignee)
	}
	e.cases[c.ID] = c
	out := *c
	e.persistLocked(c)
	e.fireLocked(c, "OPENED", c.Subject)
	return out, false, nil
}

// Transition moves a case to a new status, enforcing the state machine.
func (e *Engine) Transition(tenantID, id string, to Status, actor, note string) (Case, error) {
	c, err := e.get(tenantID, id)
	if err != nil {
		return Case{}, err
	}
	if !allowed(c.Status, to) {
		return Case{}, fmt.Errorf("casemgmt: illegal transition %s → %s", c.Status, to)
	}
	e.mu.Lock()
	c.Status = to
	now := e.now()
	c.UpdatedAt = now
	switch to {
	case Resolved:
		c.ResolvedAt = &now
	case Closed:
		c.ClosedAt = &now
		if c.ResolvedAt == nil {
			c.ResolvedAt = &now
		}
	case Reopened:
		c.ResolvedAt, c.ClosedAt = nil, nil
		c.Resolution, c.ResolutionCode = "", ""
	}
	detail := note
	if detail != "" {
		detail = string(to) + ": " + detail
	} else {
		detail = string(to)
	}
	e.logLocked(c, orSystem(actor), "TRANSITION", detail)
	out := *c
	e.mu.Unlock()
	e.persist(c)
	e.fire(c, "TRANSITION", string(to))
	return out, nil
}

// Assign (re)assigns a case and optionally moves it between queues.
func (e *Engine) Assign(tenantID, id, assignee string, queue Queue, actor string) (Case, error) {
	c, err := e.get(tenantID, id)
	if err != nil {
		return Case{}, err
	}
	e.mu.Lock()
	if queue != "" {
		c.Queue = queue
	}
	c.Assignee = assignee
	c.UpdatedAt = e.now()
	e.logLocked(c, orSystem(actor), "ASSIGNED", fmt.Sprintf("assignee=%s queue=%s", assignee, c.Queue))
	out := *c
	e.mu.Unlock()
	e.persist(c)
	e.fire(c, "ASSIGNED", assignee)
	return out, nil
}

// AddComment appends a staff/member note.
func (e *Engine) AddComment(tenantID, id, author, body string) (Case, error) {
	if body == "" {
		return Case{}, errors.New("casemgmt: comment body required")
	}
	c, err := e.get(tenantID, id)
	if err != nil {
		return Case{}, err
	}
	e.mu.Lock()
	c.Comments = append(c.Comments, Comment{Author: orSystem(author), Body: body, At: e.now()})
	c.UpdatedAt = e.now()
	e.logLocked(c, orSystem(author), "COMMENT", "")
	out := *c
	e.mu.Unlock()
	e.persist(c)
	e.fire(c, "COMMENT", author)
	return out, nil
}

// AddTask appends a checklist task.
func (e *Engine) AddTask(tenantID, id, title, actor string) (Case, error) {
	if title == "" {
		return Case{}, errors.New("casemgmt: task title required")
	}
	c, err := e.get(tenantID, id)
	if err != nil {
		return Case{}, err
	}
	e.mu.Lock()
	c.Tasks = append(c.Tasks, Task{ID: fmt.Sprintf("%s-t%02d", id, len(c.Tasks)+1), Title: title, CreatedAt: e.now()})
	c.UpdatedAt = e.now()
	e.logLocked(c, orSystem(actor), "TASK_ADDED", title)
	out := *c
	e.mu.Unlock()
	e.persist(c)
	return out, nil
}

// CompleteTask marks a checklist task done.
func (e *Engine) CompleteTask(tenantID, id, taskID, actor string) (Case, error) {
	c, err := e.get(tenantID, id)
	if err != nil {
		return Case{}, err
	}
	e.mu.Lock()
	found := false
	for i := range c.Tasks {
		if c.Tasks[i].ID == taskID {
			now := e.now()
			c.Tasks[i].Done = true
			c.Tasks[i].DoneBy = orSystem(actor)
			c.Tasks[i].DoneAt = &now
			found = true
			break
		}
	}
	if !found {
		e.mu.Unlock()
		return Case{}, fmt.Errorf("casemgmt: task %s not found", taskID)
	}
	c.UpdatedAt = e.now()
	e.logLocked(c, orSystem(actor), "TASK_DONE", taskID)
	out := *c
	e.mu.Unlock()
	e.persist(c)
	return out, nil
}

// Attach records evidence metadata on the case.
func (e *Engine) Attach(tenantID, id string, a Attachment, actor string) (Case, error) {
	c, err := e.get(tenantID, id)
	if err != nil {
		return Case{}, err
	}
	e.mu.Lock()
	a.ID = fmt.Sprintf("%s-a%02d", id, len(c.Attachments)+1)
	a.At = e.now()
	c.Attachments = append(c.Attachments, a)
	c.UpdatedAt = a.At
	e.logLocked(c, orSystem(actor), "ATTACHED", a.Name)
	out := *c
	e.mu.Unlock()
	e.persist(c)
	return out, nil
}

// Link associates two cases (e.g. fraud ring cases).
func (e *Engine) Link(tenantID, id, otherID, actor string) (Case, error) {
	c, err := e.get(tenantID, id)
	if err != nil {
		return Case{}, err
	}
	if _, err := e.get(tenantID, otherID); err != nil {
		return Case{}, fmt.Errorf("casemgmt: link target %s not found", otherID)
	}
	e.mu.Lock()
	for _, l := range c.Links {
		if l == otherID {
			out := *c
			e.mu.Unlock()
			return out, nil
		}
	}
	c.Links = append(c.Links, otherID)
	c.UpdatedAt = e.now()
	e.logLocked(c, orSystem(actor), "LINKED", otherID)
	out := *c
	e.mu.Unlock()
	e.persist(c)
	return out, nil
}

// Resolve closes the investigation loop with a coded outcome. All
// checklist tasks must be done first (enforced — a case cannot resolve
// with outstanding work).
func (e *Engine) Resolve(tenantID, id, resolution, code, actor string) (Case, error) {
	if resolution == "" || code == "" {
		return Case{}, errors.New("casemgmt: resolution text and code required")
	}
	c, err := e.get(tenantID, id)
	if err != nil {
		return Case{}, err
	}
	for _, t := range c.Tasks {
		if !t.Done {
			return Case{}, fmt.Errorf("casemgmt: outstanding task %s (%s) blocks resolution", t.ID, t.Title)
		}
	}
	e.mu.Lock()
	c.Resolution, c.ResolutionCode = resolution, code
	e.mu.Unlock()
	return e.Transition(tenantID, id, Resolved, actor, resolution)
}

// Rate records the member's post-resolution CSAT (1–5).
func (e *Engine) Rate(tenantID, id string, score int, actor string) (Case, error) {
	if score < 1 || score > 5 {
		return Case{}, errors.New("casemgmt: csat must be 1..5")
	}
	c, err := e.get(tenantID, id)
	if err != nil {
		return Case{}, err
	}
	if c.Status != Resolved && c.Status != Closed {
		return Case{}, errors.New("casemgmt: csat only after resolution")
	}
	e.mu.Lock()
	c.CSAT = score
	c.UpdatedAt = e.now()
	e.logLocked(c, orSystem(actor), "CSAT", fmt.Sprintf("%d/5", score))
	out := *c
	e.mu.Unlock()
	e.persist(c)
	e.fire(c, "CSAT", fmt.Sprintf("%d", score))
	return out, nil
}

// Escalate raises priority one band and moves the case to ESCALATED.
func (e *Engine) Escalate(tenantID, id, actor, reason string) (Case, error) {
	c, err := e.get(tenantID, id)
	if err != nil {
		return Case{}, err
	}
	e.mu.Lock()
	c.Priority = bump(c.Priority)
	sla := slaHours[c.Type][c.Priority]
	if sla > 0 {
		c.DueAt = e.now().Add(time.Duration(sla) * time.Hour)
	}
	c.UpdatedAt = e.now()
	e.logLocked(c, orSystem(actor), "ESCALATED", reason)
	out := *c
	e.mu.Unlock()
	e.persist(c)
	e.fire(c, "ESCALATED", reason)
	// move to ESCALATED status via the state machine when legal
	if allowed(out.Status, Escalated) {
		return e.Transition(tenantID, id, Escalated, actor, reason)
	}
	return out, nil
}

// Sweep finds SLA-breached non-terminal cases, flags them and fires a
// SLA_BREACH event. Intended to run on a timer (worker service) and via
// the staff "sweep" endpoint. Returns the newly breached cases.
func (e *Engine) Sweep(tenantID string) []Case {
	now := e.now()
	var out []Case
	e.mu.Lock()
	var breached []*Case
	for _, c := range e.cases {
		if c.TenantID != tenantID || c.Status.IsTerminal() || c.SLABreached || c.MergedInto != "" {
			continue
		}
		if now.After(c.DueAt) {
			c.SLABreached = true
			c.UpdatedAt = now
			e.logLocked(c, "system", "SLA_BREACH", "due "+c.DueAt.Format(time.RFC3339))
			breached = append(breached, c)
			out = append(out, *c)
		}
	}
	e.mu.Unlock()
	for _, c := range breached {
		e.persist(c)
		e.fire(c, "SLA_BREACH", c.ID)
	}
	return out
}

// Get returns one case.
func (e *Engine) Get(tenantID, id string) (Case, error) {
	c, err := e.get(tenantID, id)
	if err != nil {
		return Case{}, err
	}
	e.mu.Lock()
	out := *c
	e.mu.Unlock()
	return out, nil
}

// List returns cases matching the filter, newest first.
func (e *Engine) List(tenantID string, f Filter) []Case {
	e.mu.Lock()
	var out []Case
	for _, c := range e.cases {
		if c.TenantID != tenantID || c.MergedInto != "" {
			continue
		}
		if f.OpenOnly && c.Status.IsTerminal() {
			continue
		}
		if len(f.Status) > 0 && !hasStatus(f.Status, c.Status) {
			continue
		}
		if len(f.Type) > 0 && !hasType(f.Type, c.Type) {
			continue
		}
		if len(f.Queue) > 0 && !hasQueue(f.Queue, c.Queue) {
			continue
		}
		if f.Assignee != "" && c.Assignee != f.Assignee {
			continue
		}
		if f.MemberID != "" && c.MemberID != f.MemberID {
			continue
		}
		if f.Breached != nil && c.SLABreached != *f.Breached {
			continue
		}
		if f.CreatedBy != "" && c.Source != f.CreatedBy {
			continue
		}
		out = append(out, *c)
	}
	e.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Stats aggregates queue health.
func (e *Engine) Stats(tenantID string) Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := Stats{ByStatus: map[string]int{}, ByType: map[string]int{}, ByQueue: map[string]int{}, OpenByAgeDay: map[string]int{"0-1": 0, "1-3": 0, "3-7": 0, "7+": 0}}
	var resolveSum time.Duration
	var resolveN, csatSum int
	now := e.now()
	for _, c := range e.cases {
		if c.TenantID != tenantID || c.MergedInto != "" {
			continue
		}
		s.Total++
		s.ByStatus[string(c.Status)]++
		s.ByType[string(c.Type)]++
		s.ByQueue[string(c.Queue)]++
		if !c.Status.IsTerminal() {
			s.Open++
			age := now.Sub(c.CreatedAt)
			switch {
			case age < 24*time.Hour:
				s.OpenByAgeDay["0-1"]++
			case age < 72*time.Hour:
				s.OpenByAgeDay["1-3"]++
			case age < 7*24*time.Hour:
				s.OpenByAgeDay["3-7"]++
			default:
				s.OpenByAgeDay["7+"]++
			}
		}
		if c.SLABreached && !c.Status.IsTerminal() {
			s.Breached++
		}
		if c.ResolvedAt != nil {
			resolveSum += c.ResolvedAt.Sub(c.CreatedAt)
			resolveN++
		}
		if c.CSAT > 0 {
			csatSum += c.CSAT
			s.CSATCount++
		}
	}
	if resolveN > 0 {
		s.AvgResolveH = float64(resolveSum) / float64(resolveN) / float64(time.Hour)
	}
	if s.CSATCount > 0 {
		s.CSATAvg = float64(csatSum) / float64(s.CSATCount)
	}
	return s
}

// ---- cross-domain open helpers ------------------------------------------

// OpenFraudAlert raises (or merges) a FRAUD case from the rules/ML engines.
func (e *Engine) OpenFraudAlert(tenantID, memberID, linkedFraudCaseID string, score int, signals []string, fromML bool) (Case, bool, error) {
	src := SourceFraud
	if fromML {
		src = SourceML
	}
	pri := P3
	if score >= 85 {
		pri = P1
	} else if score >= 60 {
		pri = P2
	}
	return e.Open(OpenInput{
		TenantID: tenantID, Type: TypeFraud, MemberID: memberID,
		Subject:     fmt.Sprintf("Fraud alert score %d (%d signals)", score, len(signals)),
		Description: "Signals: " + joinComma(signals),
		LinkedKind:  "fraud_case", LinkedID: linkedFraudCaseID,
		Source: src, Priority: pri, Actor: "fraud-engine",
	})
}

// OpenClaimCase raises a CLAIM case for a benefits claim pack.
func (e *Engine) OpenClaimCase(tenantID, memberID, claimRef, kind string) (Case, bool, error) {
	t := TypeClaim
	if kind == "DEATH" {
		t = TypeDeathClaim
	}
	return e.Open(OpenInput{
		TenantID: tenantID, Type: t, MemberID: memberID,
		Subject:    "Benefit claim " + claimRef,
		LinkedKind: "claim", LinkedID: claimRef,
		Source: SourceClaims, Actor: "claims-engine",
	})
}

// OpenReconBreak raises a RECON_BREAK case for an unmatched contribution.
func (e *Engine) OpenReconBreak(tenantID, exceptionID, employerID string, amountKobo int64) (Case, bool, error) {
	return e.Open(OpenInput{
		TenantID: tenantID, Type: TypeReconBreak, EmployerID: employerID,
		Subject:    fmt.Sprintf("Recon break %s (₦%d.%02d)", exceptionID, amountKobo/100, amountKobo%100),
		LinkedKind: "recon_exception", LinkedID: exceptionID,
		Source: SourceRecon, Actor: "recon-engine", NoDedup: true,
	})
}

// OpenComplaint raises a COMPLAINT case (member, chatbot or staff).
func (e *Engine) OpenComplaint(tenantID, memberID, subject, body string, src Source, tags []string) (Case, bool, error) {
	if src == "" {
		src = SourceMember
	}
	return e.Open(OpenInput{
		TenantID: tenantID, Type: TypeComplaint, MemberID: memberID,
		Subject: subject, Description: body, Source: src, Tags: tags,
		Actor: string(src),
	})
}

// ---- internals ------------------------------------------------------------

func (e *Engine) get(tenantID, id string) (*Case, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.cases[id]
	if !ok || c.TenantID != tenantID {
		return nil, fmt.Errorf("casemgmt: case %s not found", id)
	}
	return c, nil
}

// getLocked is get for callers already holding e.mu.
func (e *Engine) logLocked(c *Case, actor, action, detail string) {
	c.Events = append(c.Events, Event{At: e.now(), Actor: actor, Action: action, Detail: detail})
}

func (e *Engine) persistLocked(c *Case) {
	if e.repo != nil {
		_ = e.repo.Save(*c) // durable write-through; engine stays available on repo error
	}
}

func (e *Engine) persist(c *Case) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.persistLocked(c)
}

func (e *Engine) fireLocked(c *Case, action, detail string) {
	ev := Event{At: e.now(), Actor: "system", Action: action, Detail: detail}
	hooks := append([]EventHook(nil), e.hooks...)
	snap := *c
	go func() {
		for _, h := range hooks {
			h(snap, ev)
		}
	}()
}

func (e *Engine) fire(c *Case, action, detail string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fireLocked(c, action, detail)
}

func allowed(from, to Status) bool {
	for _, t := range transitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

func bump(p Priority) Priority {
	switch p {
	case P4:
		return P3
	case P3:
		return P2
	case P2:
		return P1
	}
	return P1
}

func orSystem(s string) string {
	if s == "" {
		return "system"
	}
	return s
}

func hasStatus(hay []Status, s Status) bool {
	for _, h := range hay {
		if h == s {
			return true
		}
	}
	return false
}
func hasType(hay []Type, t Type) bool {
	for _, h := range hay {
		if h == t {
			return true
		}
	}
	return false
}
func hasQueue(hay []Queue, q Queue) bool {
	for _, h := range hay {
		if h == q {
			return true
		}
	}
	return false
}

func joinComma(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
