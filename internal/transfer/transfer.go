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
	ETD             time.Time  `json:"etd,omitempty"` // Effective Transfer Date (FR-509)
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

// EffectiveTransferDate computes the quarterly Effective Transfer Date
// (FR-509, RSA Transfer Reg. Feb 2024 s.3.1): requests received in the
// first two months of a quarter settle on that quarter's ETD
// (31 Mar / 30 Jun / 30 Sep / 31 Dec); requests received in the third
// month roll to the NEXT quarter's ETD.
func EffectiveTransferDate(t time.Time) time.Time {
	month := int(t.Month())
	q := (month-1)/3 + 1 // current quarter, 1..4
	year := t.Year()
	if month%3 == 0 { // 3rd month of the quarter: roll forward
		q++
		if q == 5 {
			q = 1
			year++
		}
	}
	switch q {
	case 1:
		return time.Date(year, 3, 31, 0, 0, 0, 0, t.Location())
	case 2:
		return time.Date(year, 6, 30, 0, 0, 0, 0, t.Location())
	case 3:
		return time.Date(year, 9, 30, 0, 0, 0, 0, t.Location())
	default:
		return time.Date(year, 12, 31, 0, 0, 0, 0, t.Location())
	}
}

// SubmitToCycle queues into the RTS quarterly cycle and assigns the
// Effective Transfer Date (FR-509). Requests received in the third month
// of a quarter roll to the next quarter (QueuedForCycle); requests in
// months 1–2 join the current quarter's cycle (SubmittedToRTS).
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
	etd := EffectiveTransferDate(now)
	r.ETD = etd
	r.Quarter = fmt.Sprintf("%d-Q%d", etd.Year(), (int(etd.Month())-1)/3+1)
	if int(now.Month())%3 == 0 {
		// Received in the 3rd month: rolls to next quarter's ETD.
		r.State = QueuedForCycle
	} else {
		r.State = SubmittedToRTS
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

// NetPositions computes the Global Net Transfer Position (GNTP, FR-509):
// the net kobo owed per counterparty PFA across the given transfers.
// Positive = receivable from that PFA (incoming transfers exceed outgoing);
// negative = payable. Outgoing transfers send value to the counterparty;
// incoming transfers receive value from them.
func NetPositions(transfers []Request) map[string]money.Kobo {
	net := map[string]money.Kobo{}
	for _, t := range transfers {
		switch t.Direction {
		case Incoming:
			net[t.CounterpartyPFA] += t.Value
		case Outgoing:
			net[t.CounterpartyPFA] -= t.Value
		}
	}
	return net
}
