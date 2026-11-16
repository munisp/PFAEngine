// Package member implements FR-201/FR-107/SCM-03: the RSA registry,
// PenCom PIN issuance tracking, nominees with evidence, employment
// history, and member lifecycle categories.
package member

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrNotFound      = errors.New("member: not found")
	ErrDuplicatePIN  = errors.New("member: RSA PIN already registered")
	ErrKYCIncomplete = errors.New("member: KYC incomplete; RSA cannot open")
)

// Lifecycle categories drive rights and workflows (SCM-03).
type Category string

const (
	CatProspective Category = "PROSPECTIVE" // auto-enrolment pool
	CatActive      Category = "ACTIVE"
	CatDeferred    Category = "DEFERRED"
	CatPensioner   Category = "PENSIONER"
	CatBeneficiary Category = "BENEFICIARY"
)

type Person struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	FullName  string    `json:"full_name"`
	DOB       string    `json:"dob"`
	Phone     string    `json:"phone"`
	Email     string    `json:"email"`
	Category  Category  `json:"category"`
	CreatedAt time.Time `json:"created_at"`
}

type Nominee struct {
	Name         string `json:"name"`
	Relationship string `json:"relationship"`
	SharePercent int    `json:"share_percent"`
	EvidenceRef  string `json:"evidence_ref"` // FR-201: nominees with evidence
}

type Employment struct {
	EmployerID string `json:"employer_id"`
	From       string `json:"from"`
	To         string `json:"to,omitempty"`
}

type PINStatus string

const (
	PINRequested PINStatus = "REQUESTED"
	PINIssued    PINStatus = "ISSUED"
	PINFailed    PINStatus = "FAILED"
)

// RSA is a Retirement Savings Account (FR-201).
type RSA struct {
	ID             string       `json:"id"`
	TenantID       string       `json:"tenant_id"`
	PersonID       string       `json:"person_id"`
	PIN            string       `json:"pin"` // PenCom RSA PIN
	PINStatus      PINStatus    `json:"pin_status"`
	FundID         string       `json:"fund_id"`
	SchemeID       string       `json:"scheme_id"`
	Status         string       `json:"status"` // ACTIVE, SUSPENDED, CLOSED
	Nominees       []Nominee    `json:"nominees"`
	Employment     []Employment `json:"employment_history"`
	ContribRatePct int          `json:"contrib_rate_pct"` // voluntary top-up election
	OpenedAt       time.Time    `json:"opened_at"`
}

type Registry struct {
	mu      sync.Mutex
	persons map[string]*Person
	rsas    map[string]*RSA
	byPIN   map[string]string
	seq     int
}

func NewRegistry() *Registry {
	return &Registry{persons: map[string]*Person{}, rsas: map[string]*RSA{}, byPIN: map[string]string{}}
}

func (r *Registry) CreatePerson(tenantID, name, dob, phone, email string) *Person {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	p := &Person{
		ID: fmt.Sprintf("prs-%d", r.seq), TenantID: tenantID, FullName: name,
		DOB: dob, Phone: phone, Email: email, Category: CatProspective, CreatedAt: time.Now().UTC(),
	}
	r.persons[p.ID] = p
	return p
}

func (r *Registry) Person(id string) (*Person, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.persons[id]
	return p, ok
}

// OpenRSA requests PenCom PIN issuance and opens the account (FR-107).
// The PIN exchange runs through the regulator adapter; here we record the
// request and transition on acknowledgement.
func (r *Registry) OpenRSA(tenantID, personID, fundID, schemeID string, kycPassed bool) (*RSA, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.persons[personID]; !ok {
		return nil, ErrNotFound
	}
	if !kycPassed {
		return nil, ErrKYCIncomplete
	}
	r.seq++
	rsa := &RSA{
		ID: fmt.Sprintf("rsa-%d", r.seq), TenantID: tenantID, PersonID: personID,
		FundID: fundID, SchemeID: schemeID, Status: "ACTIVE",
		PINStatus: PINRequested, OpenedAt: time.Now().UTC(),
	}
	r.rsas[rsa.ID] = rsa
	if p := r.persons[personID]; p != nil {
		p.Category = CatActive
	}
	return rsa, nil
}

// AckPIN records PenCom PIN issuance (INT-01).
func (r *Registry) AckPIN(rsaID, pin string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, taken := r.byPIN[pin]; taken {
		return ErrDuplicatePIN
	}
	rsa, ok := r.rsas[rsaID]
	if !ok {
		return ErrNotFound
	}
	rsa.PIN = pin
	rsa.PINStatus = PINIssued
	r.byPIN[pin] = rsaID
	return nil
}

func (r *Registry) RSAByPIN(pin string) (*RSA, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byPIN[pin]
	if !ok {
		return nil, false
	}
	rsa := r.rsas[id]
	return rsa, rsa != nil
}

func (r *Registry) GetRSA(id string) (*RSA, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rsa, ok := r.rsas[id]
	return rsa, ok
}

func (r *Registry) AddNominee(rsaID string, n Nominee) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rsa, ok := r.rsas[rsaID]
	if !ok {
		return ErrNotFound
	}
	total := n.SharePercent
	for _, ex := range rsa.Nominees {
		total += ex.SharePercent
	}
	if total > 100 {
		return errors.New("member: nominee shares exceed 100%")
	}
	rsa.Nominees = append(rsa.Nominees, n)
	return nil
}

func (r *Registry) AddEmployment(rsaID string, e Employment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rsa, ok := r.rsas[rsaID]
	if !ok {
		return ErrNotFound
	}
	rsa.Employment = append(rsa.Employment, e)
	return nil
}

// SetCategory drives lifecycle transitions (SCM-03).
func (r *Registry) SetCategory(personID string, c Category) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.persons[personID]
	if !ok {
		return ErrNotFound
	}
	p.Category = c
	return nil
}

func (r *Registry) List(tenantID string) []*RSA {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*RSA
	for _, rsa := range r.rsas {
		if rsa.TenantID == tenantID {
			out = append(out, rsa)
		}
	}
	return out
}
