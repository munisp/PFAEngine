package crm

import (
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/casemgmt"
)

func fixture() (*Engine, *casemgmt.Engine) {
	cm := casemgmt.New(nil)
	facts := func(tenantID, memberID string) (MemberFacts, error) {
		return MemberFacts{
			MemberID: memberID,
			JoinedAt: time.Now().Add(-400 * 24 * time.Hour),
			AgeYears: 47, BalanceKobo: 30_000_000_00,
			LastContributionAt: time.Now().Add(-200 * 24 * time.Hour),
		}, nil
	}
	return New(cm, facts), cm
}

func TestSegmentRules(t *testing.T) {
	now := time.Now()
	tags := Segment(MemberFacts{
		JoinedAt: now.Add(-365 * 24 * time.Hour), AgeYears: 50,
		BalanceKobo: 30_000_000_00, LastContributionAt: now.Add(-10 * 24 * time.Hour),
	}, now)
	if !Has(tags, SegHighValue) || !Has(tags, SegPreRetirement) || !Has(tags, SegActive) {
		t.Fatalf("tags: %v", tags)
	}
	dormant := Segment(MemberFacts{JoinedAt: now.Add(-365 * 24 * time.Hour)}, now)
	if !Has(dormant, SegDormant) || Has(dormant, SegActive) {
		t.Fatalf("dormant: %v", dormant)
	}
	atRisk := Segment(MemberFacts{OpenComplaints: 1}, now)
	if !Has(atRisk, SegAtRisk) {
		t.Fatalf("at-risk: %v", atRisk)
	}
}

func TestTimeline(t *testing.T) {
	e, _ := fixture()
	if _, err := e.Record(Interaction{TenantID: "t1", MemberID: "m1", Channel: ChWhatsApp, Summary: "asked for balance"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Record(Interaction{TenantID: "t1", MemberID: "m1", Channel: ChCall, Direction: Outbound, Summary: "callback", Kind: "CALLBACK"}); err != nil {
		t.Fatal(err)
	}
	tl := e.Timeline("t1", "m1", 0)
	if len(tl) != 2 {
		t.Fatalf("timeline: %d", len(tl))
	}
	if len(e.Timeline("t1", "other", 0)) != 0 {
		t.Fatal("timeline must be member-scoped")
	}
}

func TestMember360(t *testing.T) {
	e, cm := fixture()
	c, _, _ := cm.OpenComplaint("t1", "m1", "Late statement", "two months late", casemgmt.SourceMember, nil)
	v, err := e.Member360("t1", "m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.OpenCases) != 1 || v.ComplaintsTotal != 1 {
		t.Fatalf("360: %+v", v)
	}
	if !Has(v.Segments, SegAtRisk) {
		t.Fatalf("segments: %v", v.Segments)
	}
	if _, err := cm.Resolve("t1", c.ID, "reissued", "UPHELD", "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := cm.Rate("t1", c.ID, 5, "m1"); err != nil {
		t.Fatal(err)
	}
	v, _ = e.Member360("t1", "m1")
	if v.CSATAvg != 5 || v.ComplaintsUpheld != 1 || len(v.OpenCases) != 0 {
		t.Fatalf("360 after resolve: %+v", v)
	}
}

func TestCampaign(t *testing.T) {
	e, _ := fixture()
	c, err := e.CreateCampaign("t1", "Dormant reactivation", SegDormant, ChSMS, "Your RSA misses you — contribute today")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.LaunchCampaign("t1", c.ID, []string{"m1", "m2", "m3"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.LaunchCampaign("t1", c.ID, nil); err == nil {
		t.Fatal("relaunch must fail")
	}
	got := e.Campaigns("t1")
	if len(got) != 1 || got[0].Recipients != 3 || got[0].Status != "ACTIVE" {
		t.Fatalf("campaigns: %+v", got)
	}
	if n := len(e.Timeline("t1", "m1", 0)); n != 1 {
		t.Fatalf("campaign interactions: %d", n)
	}
}

func TestComplaintsRegister(t *testing.T) {
	e, cm := fixture()
	from := time.Now().Add(-time.Hour)
	c, _, _ := cm.OpenComplaint("t1", "m1", "USSD down", "cannot check balance", casemgmt.SourceChatbot, []string{"ussd"})
	_, _, _ = cm.Open(casemgmt.OpenInput{TenantID: "t1", Type: casemgmt.TypeFraud, MemberID: "m1", Subject: "fraud"}) // must not appear
	if _, err := cm.Resolve("t1", c.ID, "fixed", "UPHELD", "s1"); err != nil {
		t.Fatal(err)
	}
	rows := e.ComplaintsRegister("t1", from, time.Now().Add(time.Hour))
	if len(rows) != 1 || rows[0].CaseID != c.ID || rows[0].Outcome != "UPHELD" {
		t.Fatalf("register: %+v", rows)
	}
}
