package casemgmt

import (
	"testing"
	"time"
)

func newTestEngine() *Engine {
	e := New(nil)
	e.now = func() time.Time { return time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC) }
	return e
}

func openComplaint(t *testing.T, e *Engine) Case {
	t.Helper()
	c, merged, err := e.Open(OpenInput{
		TenantID: "t1", Type: TypeComplaint, MemberID: "m-1",
		Subject: "Missing November contribution", Description: "Salary deducted but RSA not credited",
		Source: SourceMember, Actor: "m-1",
	})
	if err != nil || merged {
		t.Fatalf("open: merged=%v err=%v", merged, err)
	}
	return c
}

func TestOpenDefaults(t *testing.T) {
	e := newTestEngine()
	c := openComplaint(t, e)
	if c.Status != Open || c.Priority != P3 || c.Queue != QueueMemberServices {
		t.Fatalf("defaults wrong: %+v", c)
	}
	if c.DueAt.Sub(c.CreatedAt) != 240*time.Hour {
		t.Fatalf("sla wrong: due=%v created=%v", c.DueAt, c.CreatedAt)
	}
	if len(c.Events) != 1 || c.Events[0].Action != "OPENED" {
		t.Fatalf("event log: %+v", c.Events)
	}
}

func TestDedupMerges(t *testing.T) {
	e := newTestEngine()
	c1 := openComplaint(t, e)
	c2, merged, err := e.Open(OpenInput{
		TenantID: "t1", Type: TypeComplaint, MemberID: "m-1",
		Subject: "Same issue again", Description: "follow-up report",
	})
	if err != nil || !merged {
		t.Fatalf("expected merge, got merged=%v err=%v", merged, err)
	}
	if c2.ID != c1.ID {
		t.Fatalf("merged case id %s != %s", c2.ID, c1.ID)
	}
	got, _ := e.Get("t1", c1.ID)
	if len(got.Comments) != 1 {
		t.Fatalf("merged comment missing: %+v", got.Comments)
	}
}

func TestStateMachine(t *testing.T) {
	e := newTestEngine()
	c := openComplaint(t, e)
	if _, err := e.Transition("t1", c.ID, Resolved, "s1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Transition("t1", c.ID, InProgress, "s1", ""); err == nil {
		t.Fatal("RESOLVED → IN_PROGRESS must be rejected")
	}
	if _, err := e.Transition("t1", c.ID, Reopened, "s1", "recurred"); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Get("t1", c.ID)
	if got.ResolvedAt != nil {
		t.Fatal("reopen must clear resolved_at")
	}
}

func TestResolveRequiresTasksDone(t *testing.T) {
	e := newTestEngine()
	c := openComplaint(t, e)
	c, _ = e.AddTask("t1", c.ID, "Verify employer remittance", "s1")
	if _, err := e.Resolve("t1", c.ID, "done", "UPHELD", "s1"); err == nil {
		t.Fatal("resolution must be blocked by open task")
	}
	tid := c.Tasks[0].ID
	if _, err := e.CompleteTask("t1", c.ID, tid, "s1"); err != nil {
		t.Fatal(err)
	}
	got, err := e.Resolve("t1", c.ID, "Refunded via employer schedule", "UPHELD", "s1")
	if err != nil || got.Status != Resolved || got.ResolutionCode != "UPHELD" {
		t.Fatalf("resolve: %v %+v", err, got)
	}
}

func TestRoundRobinAssignment(t *testing.T) {
	e := newTestEngine()
	e.SetRoster(QueueMemberServices, []string{"ada", "bola"})
	var owners []string
	for i := 0; i < 4; i++ {
		c, _, _ := e.Open(OpenInput{TenantID: "t1", Type: TypeComplaint, MemberID: "m-x",
			Subject: "s", NoDedup: true})
		owners = append(owners, c.Assignee)
	}
	if owners[0] != "ada" || owners[1] != "bola" || owners[2] != "ada" || owners[3] != "bola" {
		t.Fatalf("round robin broken: %v", owners)
	}
}

func TestSLASweep(t *testing.T) {
	e := newTestEngine()
	base := e.now
	c := openComplaint(t, e) // P3 complaint: 240h
	// advance 11 days → breach
	e.now = func() time.Time { return base().Add(11 * 24 * time.Hour) }
	hits := e.Sweep("t1")
	if len(hits) != 1 || hits[0].ID != c.ID {
		t.Fatalf("sweep: %+v", hits)
	}
	if n := len(e.Sweep("t1")); n != 0 {
		t.Fatal("breach must fire once")
	}
	got, _ := e.Get("t1", c.ID)
	if !got.SLABreached {
		t.Fatal("flag not persisted")
	}
}

func TestEscalateBumpsPriority(t *testing.T) {
	e := newTestEngine()
	c := openComplaint(t, e) // P3
	got, err := e.Escalate("t1", c.ID, "supervisor", "VIP member")
	if err != nil {
		t.Fatal(err)
	}
	if got.Priority != P2 || got.Status != Escalated {
		t.Fatalf("escalate: %+v", got)
	}
}

func TestFraudHelperPriority(t *testing.T) {
	e := newTestEngine()
	c, _, err := e.OpenFraudAlert("t1", "m-9", "fc-000001", 91, []string{"VELOCITY_SPIKE", "GEO_JUMP"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if c.Priority != P1 || c.Queue != QueueFraudOps || c.Source != SourceML {
		t.Fatalf("fraud helper: %+v", c)
	}
}

func TestCSATOnlyAfterResolve(t *testing.T) {
	e := newTestEngine()
	c := openComplaint(t, e)
	if _, err := e.Rate("t1", c.ID, 5, "m-1"); err == nil {
		t.Fatal("csat before resolution must fail")
	}
	if _, err := e.Resolve("t1", c.ID, "ok", "UPHELD", "s1"); err != nil {
		t.Fatal(err)
	}
	got, err := e.Rate("t1", c.ID, 4, "m-1")
	if err != nil || got.CSAT != 4 {
		t.Fatalf("csat: %v %+v", err, got)
	}
}

func TestListFilterAndStats(t *testing.T) {
	e := newTestEngine()
	c := openComplaint(t, e)
	_, _, _ = e.OpenFraudAlert("t1", "m-2", "fc-1", 70, []string{"DORMANT_THEN_LARGE"}, false)
	list := e.List("t1", Filter{Queue: []Queue{QueueFraudOps}})
	if len(list) != 1 {
		t.Fatalf("queue filter: %d", len(list))
	}
	_, _ = e.Resolve("t1", c.ID, "ok", "UPHELD", "s1")
	_, _ = e.Rate("t1", c.ID, 5, "m-1")
	s := e.Stats("t1")
	if s.Total != 2 || s.Open != 1 || s.CSATAvg != 5 || s.ByQueue[string(QueueMemberServices)] != 1 {
		t.Fatalf("stats: %+v", s)
	}
}

func TestLinkAndAttach(t *testing.T) {
	e := newTestEngine()
	c1 := openComplaint(t, e)
	c2, _, _ := e.Open(OpenInput{TenantID: "t1", Type: TypeServiceRequest, MemberID: "m-1", Subject: "Stmt re-issue", NoDedup: true})
	got, err := e.Link("t1", c1.ID, c2.ID, "s1")
	if err != nil || len(got.Links) != 1 {
		t.Fatalf("link: %v %+v", err, got)
	}
	got, err = e.Attach("t1", c1.ID, Attachment{Name: "payslip.pdf", Mime: "application/pdf", SizeByte: 1024, Ref: "s3://ev/x"}, "s1")
	if err != nil || len(got.Attachments) != 1 {
		t.Fatalf("attach: %v", err)
	}
}
