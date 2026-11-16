// Package transfer implements FR-402 / SCM-10 / INT-01: the RSA transfer
// workflow against PenCom's RTS — initiation in/out, the 365-day rule,
// quarterly-cycle synchronization, value settlement, notifications.
package transfer

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/money"
)

var (
	Err365Rule  = errors.New("transfer: only one RSA transfer per 365 days (RTS regulation)")
	ErrNotFound = errors.New("transfer: not found")
	ErrBadState = errors.New("transfer: invalid state transition")
	ErrWindow   = errors.New("transfer: RTS submission window closed; queued for next quarter")
)

type State string

const (
	Initiated      State = "INITIATED"
	Validated      State = "VALIDATED"
	QueuedForCycle State = "QUEUED_FOR_CYCLE"
	SubmittedToRTS State = "SUBMITTED_TO_RTS"
	Settled        State = "SETTLED"
	Notified       State = "NOTIFIED"
	Rejected       State = "REJECTED"
)

type Direction string

const (
	Outgoing Direction = "OUT"
	Incoming Direction = "IN"
)

type Request struct {
	ID              string     `json:"id"`
	TenantID        string     `json:"tenant_id"`
	RSAID           string     `json:"rsa_id"`
	RSAPIN          string     `json:"rsa_pin"`
	Direction       Direction  `json:"direction"`
	CounterpartyPFA string     `json:"counterparty_pfa"`
	Value           money.Kobo `json:"value_kobo"`
	State           State      `json:"state"`
	Quarter         string     `json:"quarter"`
	InitiatedAt     time.Time  `json:"initiated_at"`
	SettledAt       time.Time  `json:"settled_at,omitempty"`
}

// Service runs the RTS workflow with quarterly-cycle sync.
type Service struct {
	mu        sync.Mutex
	requests  map[string]*Request
	lastByRSA map[string]time.Time // 365-day rule per RSA
	seq       int
}

func NewService() *Service {
	return &Service{requests: map[string]*Request{}, lastByRSA: map[string]time.Time{}}
}

// Initiate validates the 365-day rule and starts the workflow.
func (s *Service) Initiate(tenantID, rsaID, pin string, dir Direction, counterparty string, value money.Kobo, now time.Time) (*Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if last, ok := s.lastByRSA[rsaID]; ok && now.Sub(last) < 365*24*time.Hour {
		return nil, fmt.Errorf("%w (last: %s)", Err365Rule, last.Format("2006-01-02"))
	}
	s.seq++
	r := &Request{
		ID: fmt.Sprintf("trf-%d", s.seq), TenantID: tenantID, RSAID: rsaID, RSAPIN: pin,
		Direction: dir, CounterpartyPFA: counterparty, Value: value,
		State: Initiated, InitiatedAt: now,
	}
	s.requests[r.ID] = r
	return r, nil
}

func (s *Service) set(id string, from []State, to State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.requests[id]
	if !ok {
		return ErrNotFound
	}
	for _, f := range from {
		if r.State == f {
			r.State = to
			return nil
		}
	}
	return ErrBadState
}

func (s *Service) Validate(id string) error { return s.set(id, []State{Initiated}, Validated) }

// SubmitToCycle queues into the current/next RTS quarterly window (INT-01).
func (s *Service) SubmitToCycle(id string, now time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.requests[id]
	if !ok {
		return "", ErrNotFound
	}
	if r.State != Validated {
		return "", ErrBadState
	}
	month := int(now.Month())
	q := (month-1)/3 + 1
	if month%3 == 0 {
		r.Quarter = fmt.Sprintf("%d-Q%d", now.Year(), q)
		r.State = SubmittedToRTS
	} else {
		// Queued for the next quarterly window.
		r.Quarter = fmt.Sprintf("%d-Q%d", now.Year(), q)
		r.State = QueuedForCycle
	}
	return r.Quarter, nil
}

// Settle records value settlement between PFAs (FR-402).
func (s *Service) Settle(id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.requests[id]
	if !ok {
		return ErrNotFound
	}
	if r.State != SubmittedToRTS && r.State != QueuedForCycle {
		return ErrBadState
	}
	r.State = Settled
	r.SettledAt = now
	s.lastByRSA[r.RSAID] = now // 365-day clock restarts on settlement
	return nil
}

func (s *Service) Notify(id string) error {
	return s.set(id, []State{Settled}, Notified)
}

func (s *Service) Reject(id, reason string) error {
	return s.set(id, []State{Initiated, Validated, QueuedForCycle}, Rejected)
}

func (s *Service) Get(id string) (*Request, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.requests[id]
	return r, ok
}
