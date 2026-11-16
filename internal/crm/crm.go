// Package crm implements the platform's next-generation CRM layer
// (Tranche 20): the member 360° profile, omnichannel interaction
// timeline (WhatsApp / USSD / PWA / email / call / branch / agent),
// rule-based segmentation, targeted outbound campaigns and the
// PenCom-compliant complaints register.
//
// The CRM is deliberately read-composed: it never duplicates system-of-
// record data. It holds only CRM-native artefacts (interactions,
// campaigns, segment definitions) and assembles Member360 views on
// demand through provider interfaces injected by the service layer —
// keeping internal/crm free of import cycles with the domain engines.
package crm

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/casemgmt"
)

// Channel enumerates every member touchpoint the platform supports.
type Channel string

const (
	ChWhatsApp Channel = "WHATSAPP"
	ChUSSD     Channel = "USSD"
	ChWeb      Channel = "WEB"
	ChMobile   Channel = "MOBILE"
	ChEmail    Channel = "EMAIL"
	ChCall     Channel = "CALL"
	ChBranch   Channel = "BRANCH"
	ChAgent    Channel = "AGENT"
	ChSMS      Channel = "SMS"
)

// Direction of an interaction.
type Direction string

const (
	Inbound  Direction = "INBOUND"
	Outbound Direction = "OUTBOUND"
)

// Interaction is one touchpoint event in the member timeline.
type Interaction struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	MemberID  string    `json:"member_id"`
	Channel   Channel   `json:"channel"`
	Direction Direction `json:"direction"`
	Kind      string    `json:"kind"` // ENQUIRY|COMPLAINT|CONTRIBUTION|CLAIM|STATEMENT|CALLBACK|CAMPAIGN
	Summary   string    `json:"summary"`
	CaseID    string    `json:"case_id,omitempty"`
	StaffID   string    `json:"staff_id,omitempty"`
	At        time.Time `json:"at"`
}

// SegmentTag is a computed member classification.
type SegmentTag string

const (
	SegDormant       SegmentTag = "DORMANT"    // no contribution in 180+ days
	SegHighValue     SegmentTag = "HIGH_VALUE" // balance ≥ ₦25m
	SegMicroPension  SegmentTag = "MICRO_PENSION"
	SegDiaspora      SegmentTag = "DIASPORA"
	SegPreRetirement SegmentTag = "PRE_RETIREMENT" // age ≥ 45
	SegNewMember     SegmentTag = "NEW_MEMBER"     // joined < 90 days
	SegAtRisk        SegmentTag = "AT_RISK"        // open complaint or breached case
	SegActive        SegmentTag = "ACTIVE"
)

// MemberFacts is the read-model the segmenter evaluates. The service
// layer populates it from the domain engines (member registry, ledger,
// cases) so crm never imports them.
type MemberFacts struct {
	MemberID           string
	JoinedAt           time.Time
	AgeYears           int
	BalanceKobo        int64
	LastContributionAt time.Time // zero when never contributed
	MicroPension       bool
	Diaspora           bool
	OpenComplaints     int
	BreachedOpenCases  int
	OpenCases          int
}

// Member360 is the single-pane member view for staff.
type Member360 struct {
	MemberID         string          `json:"member_id"`
	Facts            MemberFacts     `json:"facts"`
	Segments         []SegmentTag    `json:"segments"`
	OpenCases        []casemgmt.Case `json:"open_cases"`
	RecentCases      []casemgmt.Case `json:"recent_cases"`
	Timeline         []Interaction   `json:"timeline"`
	CSATAvg          float64         `json:"csat_avg"`
	ComplaintsTotal  int             `json:"complaints_total"`
	ComplaintsUpheld int             `json:"complaints_upheld"`
}

// Campaign is a targeted outbound communication to a segment.
type Campaign struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	Name       string     `json:"name"`
	Segment    SegmentTag `json:"segment"`
	Channel    Channel    `json:"channel"`
	Message    string     `json:"message"`
	Status     string     `json:"status"` // DRAFT|ACTIVE|DONE
	Recipients int        `json:"recipients"`
	CreatedAt  time.Time  `json:"created_at"`
	LaunchedAt *time.Time `json:"launched_at,omitempty"`
}

// ComplaintRow is one line of the PenCom quarterly complaints register
// (PFAs must maintain and report a complaint register).
type ComplaintRow struct {
	CaseID        string     `json:"case_id"`
	MemberID      string     `json:"member_id"`
	Subject       string     `json:"subject"`
	ReceivedAt    time.Time  `json:"received_at"`
	ResolvedAt    *time.Time `json:"resolved_at,omitempty"`
	Status        string     `json:"status"`
	Outcome       string     `json:"outcome,omitempty"` // resolution code
	DaysToResolve int        `json:"days_to_resolve"`
	CSAT          int        `json:"csat,omitempty"`
}

// CaseProvider is the slice of casemgmt the CRM reads through.
type CaseProvider interface {
	List(tenantID string, f casemgmt.Filter) []casemgmt.Case
}

// FactsProvider resolves MemberFacts for a member (service-layer adapter).
type FactsProvider func(tenantID, memberID string) (MemberFacts, error)

// Engine is the CRM store + composer.
type Engine struct {
	mu           sync.Mutex
	interactions map[string][]Interaction // tenantID|memberID -> timeline
	campaigns    map[string]*Campaign
	cseq, iseq   int
	cases        CaseProvider
	facts        FactsProvider
	now          func() time.Time
}

// New wires the CRM to its read providers.
func New(cases CaseProvider, facts FactsProvider) *Engine {
	return &Engine{
		interactions: map[string][]Interaction{},
		campaigns:    map[string]*Campaign{},
		cases:        cases,
		facts:        facts,
		now:          time.Now,
	}
}

func key(tenantID, memberID string) string { return tenantID + "|" + memberID }

// Record appends an interaction to the member timeline.
func (e *Engine) Record(in Interaction) (Interaction, error) {
	if in.TenantID == "" || in.MemberID == "" || in.Channel == "" || in.Summary == "" {
		return Interaction{}, errors.New("crm: tenant, member, channel and summary required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.iseq++
	in.ID = fmt.Sprintf("ix-%07d", e.iseq)
	if in.At.IsZero() {
		in.At = e.now()
	}
	if in.Direction == "" {
		in.Direction = Inbound
	}
	k := key(in.TenantID, in.MemberID)
	e.interactions[k] = append(e.interactions[k], in)
	return in, nil
}

// Timeline returns the member's interaction history, newest first.
func (e *Engine) Timeline(tenantID, memberID string, limit int) []Interaction {
	e.mu.Lock()
	src := append([]Interaction(nil), e.interactions[key(tenantID, memberID)]...)
	e.mu.Unlock()
	sort.Slice(src, func(i, j int) bool { return src[i].At.After(src[j].At) })
	if limit > 0 && len(src) > limit {
		src = src[:limit]
	}
	return src
}

// Segment evaluates the ruleset over MemberFacts.
func Segment(f MemberFacts, now time.Time) []SegmentTag {
	var tags []SegmentTag
	if f.MicroPension {
		tags = append(tags, SegMicroPension)
	}
	if f.Diaspora {
		tags = append(tags, SegDiaspora)
	}
	if f.BalanceKobo >= 25_000_000_00 { // ₦25m in kobo
		tags = append(tags, SegHighValue)
	}
	if f.AgeYears >= 45 {
		tags = append(tags, SegPreRetirement)
	}
	if now.Sub(f.JoinedAt) < 90*24*time.Hour {
		tags = append(tags, SegNewMember)
	}
	if f.LastContributionAt.IsZero() || now.Sub(f.LastContributionAt) >= 180*24*time.Hour {
		tags = append(tags, SegDormant)
	} else {
		tags = append(tags, SegActive)
	}
	if f.OpenComplaints > 0 || f.BreachedOpenCases > 0 {
		tags = append(tags, SegAtRisk)
	}
	return tags
}

// Has reports whether a tag list contains t.
func Has(tags []SegmentTag, t SegmentTag) bool {
	for _, x := range tags {
		if x == t {
			return true
		}
	}
	return false
}

// Member360 assembles the full single-pane view.
func (e *Engine) Member360(tenantID, memberID string) (Member360, error) {
	if e.facts == nil {
		return Member360{}, errors.New("crm: no facts provider configured")
	}
	f, err := e.facts(tenantID, memberID)
	if err != nil {
		return Member360{}, err
	}
	f.MemberID = memberID
	v := Member360{MemberID: memberID}
	if e.cases != nil {
		// Case-derived facts (open complaints, breached cases) come from
		// the live case engine — the authoritative source — and feed the
		// segmenter before tags are computed.
		all := e.cases.List(tenantID, casemgmt.Filter{MemberID: memberID})
		var csatSum, csatN int
		for _, c := range all {
			if !c.Status.IsTerminal() {
				v.OpenCases = append(v.OpenCases, c)
				f.OpenCases++
				if c.Type == casemgmt.TypeComplaint {
					f.OpenComplaints++
				}
				if c.SLABreached {
					f.BreachedOpenCases++
				}
			} else if len(v.RecentCases) < 10 {
				v.RecentCases = append(v.RecentCases, c)
			}
			if c.Type == casemgmt.TypeComplaint {
				v.ComplaintsTotal++
				if c.ResolutionCode == "UPHELD" {
					v.ComplaintsUpheld++
				}
			}
			if c.CSAT > 0 {
				csatSum += c.CSAT
				csatN++
			}
		}
		if csatN > 0 {
			v.CSATAvg = float64(csatSum) / float64(csatN)
		}
	}
	v.Facts = f
	v.Segments = Segment(f, e.now())
	v.Timeline = e.Timeline(tenantID, memberID, 50)
	return v, nil
}

// CreateCampaign drafts a campaign.
func (e *Engine) CreateCampaign(tenantID, name string, seg SegmentTag, ch Channel, message string) (Campaign, error) {
	if tenantID == "" || name == "" || seg == "" || ch == "" || message == "" {
		return Campaign{}, errors.New("crm: campaign name, segment, channel and message required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cseq++
	c := &Campaign{ID: fmt.Sprintf("cmp-%05d", e.cseq), TenantID: tenantID, Name: name,
		Segment: seg, Channel: ch, Message: message, Status: "DRAFT", CreatedAt: e.now()}
	e.campaigns[c.ID] = c
	return *c, nil
}

// LaunchCampaign activates a campaign against the resolved member list
// (the service layer resolves segment membership) and logs an outbound
// interaction per recipient — the notification service picks delivery up
// from there.
func (e *Engine) LaunchCampaign(tenantID, id string, memberIDs []string) (Campaign, error) {
	e.mu.Lock()
	c, ok := e.campaigns[id]
	if !ok || c.TenantID != tenantID {
		e.mu.Unlock()
		return Campaign{}, fmt.Errorf("crm: campaign %s not found", id)
	}
	if c.Status != "DRAFT" {
		e.mu.Unlock()
		return Campaign{}, errors.New("crm: campaign already launched")
	}
	now := e.now()
	c.Status = "ACTIVE"
	c.LaunchedAt = &now
	c.Recipients = len(memberIDs)
	snap := *c
	e.mu.Unlock()
	for _, m := range memberIDs {
		_, _ = e.Record(Interaction{
			TenantID: tenantID, MemberID: m, Channel: snap.Channel,
			Direction: Outbound, Kind: "CAMPAIGN",
			Summary: "Campaign “" + snap.Name + "”: " + truncate(snap.Message, 120),
		})
	}
	return snap, nil
}

// Campaigns lists campaigns, newest first.
func (e *Engine) Campaigns(tenantID string) []Campaign {
	e.mu.Lock()
	var out []Campaign
	for _, c := range e.campaigns {
		if c.TenantID == tenantID {
			out = append(out, *c)
		}
	}
	e.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// ComplaintsRegister renders the PenCom quarterly complaints register for
// a period: every COMPLAINT case received in [from, to].
func (e *Engine) ComplaintsRegister(tenantID string, from, to time.Time) []ComplaintRow {
	if e.cases == nil {
		return nil
	}
	var rows []ComplaintRow
	for _, c := range e.cases.List(tenantID, casemgmt.Filter{Type: []casemgmt.Type{casemgmt.TypeComplaint}}) {
		if c.CreatedAt.Before(from) || c.CreatedAt.After(to) {
			continue
		}
		r := ComplaintRow{
			CaseID: c.ID, MemberID: c.MemberID, Subject: c.Subject,
			ReceivedAt: c.CreatedAt, ResolvedAt: c.ResolvedAt,
			Status: string(c.Status), Outcome: c.ResolutionCode, CSAT: c.CSAT,
		}
		if c.ResolvedAt != nil {
			r.DaysToResolve = int(c.ResolvedAt.Sub(c.CreatedAt).Hours() / 24)
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ReceivedAt.Before(rows[j].ReceivedAt) })
	return rows
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
