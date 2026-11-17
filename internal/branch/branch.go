// Package branch implements the branch network registry and book-a-visit
// appointment scheduling (Tranche 21) — the "zero wait time" branch visit
// feature on PFA websites. Members pick a branch, date and timeslot; the
// engine enforces per-slot capacity so double-booking is impossible.
package branch

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/persist"
)

// Branch is a physical service location.
type Branch struct {
	ID          string  `json:"id"`
	TenantID    string  `json:"tenant_id"`
	Name        string  `json:"name"`
	Address     string  `json:"address"`
	City        string  `json:"city"`
	State       string  `json:"state"`
	Phone       string  `json:"phone"`
	Email       string  `json:"email"`
	Lat         float64 `json:"lat"`
	Lng         float64 `json:"lng"`
	OpenHours   string  `json:"open_hours"`    // e.g. "Mon-Fri 08:00-16:00"
	SlotMinutes int     `json:"slot_minutes"`  // appointment length
	SlotsPerDay int     `json:"slots_per_day"` // capacity per day
	Active      bool    `json:"active"`
}

// ApptStatus is the appointment lifecycle.
type ApptStatus string

const (
	Booked    ApptStatus = "BOOKED"
	CheckedIn ApptStatus = "CHECKED_IN"
	Done      ApptStatus = "DONE"
	Cancelled ApptStatus = "CANCELLED"
	NoShow    ApptStatus = "NO_SHOW"
)

// Appointment is one booked visit slot.
type Appointment struct {
	ID       string     `json:"id"`
	TenantID string     `json:"tenant_id"`
	BranchID string     `json:"branch_id"`
	MemberID string     `json:"member_id"`
	Date     string     `json:"date"` // YYYY-MM-DD
	Slot     int        `json:"slot"` // slot index within the day
	Purpose  string     `json:"purpose"`
	Status   ApptStatus `json:"status"`
	BookedAt time.Time  `json:"booked_at"`
}

// Engine manages branches and appointments.
type Engine struct {
	mu       sync.Mutex
	branches map[string]*Branch
	appts    map[string]*Appointment
	bseq     int
	aseq     int
	sink     persist.Sink // nil in pure in-memory mode
}

// New returns an empty engine.
func New() *Engine {
	return &Engine{branches: map[string]*Branch{}, appts: map[string]*Appointment{}}
}

// AttachSink wires write-through snapshot persistence (NFR-10).
func (e *Engine) AttachSink(s persist.Sink) { e.sink = s }

// Restore rehydrates branches and appointments from boot snapshots.
func (e *Engine) Restore(snaps []persist.Snapshot) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range snaps {
		switch s.Kind {
		case "branch.branch":
			var b Branch
			if err := json.Unmarshal(s.Payload, &b); err != nil {
				return err
			}
			bc := b
			e.branches[b.ID] = &bc
			if n, err := strconv.Atoi(strings.TrimPrefix(b.ID, "br-")); err == nil && n > e.bseq {
				e.bseq = n
			}
		case "branch.appt":
			var a Appointment
			if err := json.Unmarshal(s.Payload, &a); err != nil {
				return err
			}
			ac := a
			e.appts[a.ID] = &ac
			if n, err := strconv.Atoi(strings.TrimPrefix(a.ID, "appt-")); err == nil && n > e.aseq {
				e.aseq = n
			}
		}
	}
	return nil
}

// AddBranch registers a branch.
func (e *Engine) AddBranch(b Branch) (Branch, error) {
	if b.TenantID == "" || b.Name == "" || b.State == "" {
		return Branch{}, errors.New("branch: tenant, name and state required")
	}
	if b.SlotMinutes <= 0 {
		b.SlotMinutes = 30
	}
	if b.SlotsPerDay <= 0 {
		b.SlotsPerDay = 14
	}
	e.mu.Lock()
	e.bseq++
	b.ID = fmt.Sprintf("br-%04d", e.bseq)
	b.Active = true
	e.branches[b.ID] = &b
	e.mu.Unlock()
	persist.Save(e.sink, "branch.branch", b.TenantID, b.ID, b)
	return b, nil
}

// Branches lists active branches, optionally filtered by state.
func (e *Engine) Branches(tenantID, state string) []Branch {
	e.mu.Lock()
	var out []Branch
	for _, b := range e.branches {
		if b.TenantID == tenantID && b.Active && (state == "" || b.State == state) {
			out = append(out, *b)
		}
	}
	e.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].State != out[j].State {
			return out[i].State < out[j].State
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Availability returns free slot indexes for a branch on a date.
func (e *Engine) Availability(tenantID, branchID, date string) ([]int, error) {
	b, err := e.getBranch(tenantID, branchID)
	if err != nil {
		return nil, err
	}
	used := map[int]bool{}
	e.mu.Lock()
	for _, a := range e.appts {
		if a.BranchID == branchID && a.Date == date && (a.Status == Booked || a.Status == CheckedIn) {
			used[a.Slot] = true
		}
	}
	e.mu.Unlock()
	free := make([]int, 0, b.SlotsPerDay)
	for s := 0; s < b.SlotsPerDay; s++ {
		if !used[s] {
			free = append(free, s)
		}
	}
	return free, nil
}

// Book reserves a slot; fails closed on capacity (no double-booking).
func (e *Engine) Book(tenantID, branchID, memberID, date string, slot int, purpose string) (Appointment, error) {
	if memberID == "" || date == "" || purpose == "" {
		return Appointment{}, errors.New("branch: member, date and purpose required")
	}
	b, err := e.getBranch(tenantID, branchID)
	if err != nil {
		return Appointment{}, err
	}
	if slot < 0 || slot >= b.SlotsPerDay {
		return Appointment{}, fmt.Errorf("branch: slot %d out of range 0..%d", slot, b.SlotsPerDay-1)
	}
	if date <= time.Now().Format("2006-01-02") {
		return Appointment{}, errors.New("branch: appointment date must be in the future")
	}
	e.mu.Lock()
	for _, a := range e.appts {
		if a.BranchID == branchID && a.Date == date && a.Slot == slot &&
			(a.Status == Booked || a.Status == CheckedIn) {
			e.mu.Unlock()
			return Appointment{}, fmt.Errorf("branch: slot %d on %s already booked", slot, date)
		}
	}
	e.aseq++
	a := &Appointment{
		ID: fmt.Sprintf("appt-%06d", e.aseq), TenantID: tenantID, BranchID: branchID,
		MemberID: memberID, Date: date, Slot: slot, Purpose: purpose,
		Status: Booked, BookedAt: time.Now(),
	}
	e.appts[a.ID] = a
	out := *a
	e.mu.Unlock()
	persist.Save(e.sink, "branch.appt", tenantID, out.ID, out)
	return out, nil
}

// Transition moves an appointment through CHECKED_IN → DONE, or CANCELLED /
// NO_SHOW.
func (e *Engine) Transition(tenantID, id string, to ApptStatus) (Appointment, error) {
	e.mu.Lock()
	a, ok := e.appts[id]
	if !ok || a.TenantID != tenantID {
		e.mu.Unlock()
		return Appointment{}, fmt.Errorf("branch: appointment %s not found", id)
	}
	legal := map[ApptStatus][]ApptStatus{
		Booked:    {CheckedIn, Cancelled, NoShow},
		CheckedIn: {Done, Cancelled},
	}
	okTransition := false
	for _, s := range legal[a.Status] {
		if s == to {
			okTransition = true
		}
	}
	if !okTransition {
		e.mu.Unlock()
		return Appointment{}, fmt.Errorf("branch: illegal transition %s → %s", a.Status, to)
	}
	a.Status = to
	out := *a
	e.mu.Unlock()
	persist.Save(e.sink, "branch.appt", tenantID, out.ID, out)
	return out, nil
}

// Appointments lists appointments; memberID or branchID filter.
func (e *Engine) Appointments(tenantID, memberID, branchID, date string) []Appointment {
	e.mu.Lock()
	var out []Appointment
	for _, a := range e.appts {
		if a.TenantID != tenantID {
			continue
		}
		if memberID != "" && a.MemberID != memberID {
			continue
		}
		if branchID != "" && a.BranchID != branchID {
			continue
		}
		if date != "" && a.Date != date {
			continue
		}
		out = append(out, *a)
	}
	e.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return out[i].Slot < out[j].Slot
	})
	return out
}

func (e *Engine) getBranch(tenantID, id string) (*Branch, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b, ok := e.branches[id]
	if !ok || b.TenantID != tenantID || !b.Active {
		return nil, fmt.Errorf("branch: branch %s not found", id)
	}
	return b, nil
}
