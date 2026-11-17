// Package docupload implements the KYC document submission portal
// (Tranche 21): members upload identity documents (passport photo,
// NIN slip, ID card, proof of address, birth certificate, employment
// letter) which enter a compliance review queue. Every submission opens a
// KYC_REVIEW case in the unified case engine so review work is
// SLA-tracked and assigned, not a passive list.
package docupload

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/casemgmt"
)

// Kind enumerates accepted document types.
type Kind string

const (
	DocPassportPhoto    Kind = "PASSPORT_PHOTO"
	DocNINSlip          Kind = "NIN_SLIP"
	DocIDCard           Kind = "ID_CARD"
	DocProofOfAddress   Kind = "PROOF_OF_ADDRESS"
	DocBirthCertificate Kind = "BIRTH_CERTIFICATE"
	DocEmploymentLetter Kind = "EMPLOYMENT_LETTER"
)

// Status is the document review lifecycle.
type Status string

const (
	Submitted   Status = "SUBMITTED"
	UnderReview Status = "UNDER_REVIEW"
	Approved    Status = "APPROVED"
	Rejected    Status = "REJECTED"
)

// Document is one uploaded KYC artefact (metadata only; the blob lives in
// object storage at Ref).
type Document struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	MemberID   string    `json:"member_id"`
	Kind       Kind      `json:"kind"`
	Filename   string    `json:"filename"`
	Mime       string    `json:"mime"`
	SizeBytes  int64     `json:"size_bytes"`
	Ref        string    `json:"ref"` // object-storage URI
	SHA256     string    `json:"sha256"`
	Status     Status    `json:"status"`
	Reviewer   string    `json:"reviewer,omitempty"`
	ReviewNote string    `json:"review_note,omitempty"`
	CaseID     string    `json:"case_id,omitempty"` // linked KYC_REVIEW case
	UploadedAt time.Time `json:"uploaded_at"`
	ReviewedAt *time.Time `json:"reviewed_at,omitempty"`
}

// Engine manages document capture and review.
type Engine struct {
	mu     sync.Mutex
	docs   map[string]*Document
	seq    int
	cases  *casemgmt.Engine // nil-tolerant (unit tests without casemgmt)
}

// New wires the engine to the case engine (may be nil in tests).
func New(cases *casemgmt.Engine) *Engine {
	return &Engine{docs: map[string]*Document{}, cases: cases}
}

// Submit records an uploaded document and opens a KYC_REVIEW case.
func (e *Engine) Submit(tenantID, memberID string, kind Kind, filename, mime string, size int64, ref, sha string) (Document, error) {
	if tenantID == "" || memberID == "" || kind == "" || filename == "" {
		return Document{}, errors.New("docupload: tenant, member, kind and filename required")
	}
	if size <= 0 || size > 10<<20 {
		return Document{}, errors.New("docupload: size must be 1B..10MB")
	}
	switch kind {
	case DocPassportPhoto, DocNINSlip, DocIDCard, DocProofOfAddress, DocBirthCertificate, DocEmploymentLetter:
	default:
		return Document{}, fmt.Errorf("docupload: unsupported kind %s", kind)
	}
	e.mu.Lock()
	e.seq++
	d := &Document{
		ID: fmt.Sprintf("doc-%06d", e.seq), TenantID: tenantID, MemberID: memberID,
		Kind: kind, Filename: filename, Mime: mime, SizeBytes: size,
		Ref: ref, SHA256: sha, Status: Submitted, UploadedAt: time.Now(),
	}
	e.docs[d.ID] = d
	out := *d
	e.mu.Unlock()

	if e.cases != nil {
		if c, _, err := e.cases.Open(casemgmt.OpenInput{
			TenantID: tenantID, Type: casemgmt.TypeKYCReview, MemberID: memberID,
			Subject:    fmt.Sprintf("KYC document review: %s (%s)", kind, filename),
			LinkedKind: "kyc_document", LinkedID: d.ID,
			Source: casemgmt.SourceMember, Actor: memberID, NoDedup: true,
		}); err == nil {
			e.mu.Lock()
			d.CaseID = c.ID
			out = *d
			e.mu.Unlock()
		}
	}
	return out, nil
}

// Review approves or rejects a document and resolves the linked case.
func (e *Engine) Review(tenantID, id, reviewer string, approve bool, note string) (Document, error) {
	e.mu.Lock()
	d, ok := e.docs[id]
	if !ok || d.TenantID != tenantID {
		e.mu.Unlock()
		return Document{}, fmt.Errorf("docupload: document %s not found", id)
	}
	if d.Status == Approved || d.Status == Rejected {
		e.mu.Unlock()
		return Document{}, errors.New("docupload: document already decided")
	}
	now := time.Now()
	d.Reviewer, d.ReviewNote, d.ReviewedAt = reviewer, note, &now
	if approve {
		d.Status = Approved
	} else {
		d.Status = Rejected
	}
	caseID := d.CaseID
	out := *d
	e.mu.Unlock()

	if e.cases != nil && caseID != "" {
		code := "UPHELD"
		if !approve {
			code = "REJECTED"
		}
		_, _ = e.cases.Resolve(tenantID, caseID, fmt.Sprintf("Document %s: %s", d.Kind, note), code, reviewer)
	}
	return out, nil
}

// StartReview marks a document under review (locks the queue item to the
// reviewer).
func (e *Engine) StartReview(tenantID, id, reviewer string) (Document, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d, ok := e.docs[id]
	if !ok || d.TenantID != tenantID {
		return Document{}, fmt.Errorf("docupload: document %s not found", id)
	}
	if d.Status != Submitted && d.Status != UnderReview {
		return Document{}, errors.New("docupload: document already decided")
	}
	d.Status, d.Reviewer = UnderReview, reviewer
	return *d, nil
}

// Pending returns the review queue (submitted or under review).
func (e *Engine) Pending(tenantID string) []Document {
	return e.filter(tenantID, func(d *Document) bool {
		return d.Status == Submitted || d.Status == UnderReview
	})
}

// ByMember returns a member's document history.
func (e *Engine) ByMember(tenantID, memberID string) []Document {
	return e.filter(tenantID, func(d *Document) bool { return d.MemberID == memberID })
}

func (e *Engine) filter(tenantID string, ok func(*Document) bool) []Document {
	e.mu.Lock()
	var out []Document
	for _, d := range e.docs {
		if d.TenantID == tenantID && ok(d) {
			out = append(out, *d)
		}
	}
	e.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].UploadedAt.After(out[j].UploadedAt) })
	return out
}
