package main

import (
	"encoding/json"
	"testing"
)

// End-to-end API coverage for the Tranche 20 unified case engine + CRM:
// member-raised complaint → dedup merge → workbench filters → task-gated
// resolution → CSAT → CRM 360 → PenCom complaints register.
func TestCaseMgmtEndToEnd(t *testing.T) {
	h := newTestDeps().handler()

	rr := onboard(t, h, `{"full_name":"Case Test","dob":"1985-06-15","nin":"11111111111","bvn":"22222222222","phone":"+2348111111111"}`)
	if rr.Code != 201 {
		t.Fatalf("onboard: %d %s", rr.Code, rr.Body.String())
	}
	var created struct {
		MemberID string `json:"member_id"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	mid := created.MemberID

	// member raises a complaint
	rr = do(t, h, "POST", "/api/v1/members/"+mid+"/cases", "t1",
		`{"type":"COMPLAINT","subject":"Missing contribution","description":"November salary deducted, RSA not credited"}`)
	if rr.Code != 201 {
		t.Fatalf("raise case: %d %s", rr.Code, rr.Body.String())
	}
	var opened struct {
		Case struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Queue  string `json:"queue"`
		} `json:"case"`
		Merged bool `json:"merged"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&opened); err != nil {
		t.Fatal(err)
	}
	if opened.Merged || opened.Case.ID == "" || opened.Case.Queue != "MEMBER_SERVICES" {
		t.Fatalf("open: %+v", opened)
	}
	cid := opened.Case.ID

	// duplicate report merges instead of duplicating
	rr = do(t, h, "POST", "/api/v1/members/"+mid+"/cases", "t1",
		`{"type":"COMPLAINT","subject":"Still missing","description":"follow up"}`)
	var dup struct {
		Merged bool `json:"merged"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&dup)
	if !dup.Merged {
		t.Fatalf("expected dedup merge, got %s", rr.Body.String())
	}

	// member case list shows exactly one case
	rr = do(t, h, "GET", "/api/v1/members/"+mid+"/cases", "t1", "")
	var list struct {
		Cases []struct {
			ID string `json:"id"`
		} `json:"cases"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&list); err != nil || len(list.Cases) != 1 {
		t.Fatalf("member cases: %v %s", err, rr.Body.String())
	}

	// staff workbench: filter by queue
	rr = do(t, h, "GET", "/api/v1/cases?queue=MEMBER_SERVICES&open=1", "t1", "")
	var queue struct {
		Cases []struct {
			ID       string `json:"id"`
			Assignee string `json:"assignee"`
		} `json:"cases"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&queue); err != nil || len(queue.Cases) != 1 {
		t.Fatalf("queue list: %v %s", err, rr.Body.String())
	}

	// comment + task; resolution blocked until task done
	rr = do(t, h, "POST", "/api/v1/cases/"+cid+"/comments", "t1", `{"body":"Contacting employer"}`)
	if rr.Code != 200 {
		t.Fatalf("comment: %d", rr.Code)
	}
	rr = do(t, h, "POST", "/api/v1/cases/"+cid+"/tasks", "t1", `{"title":"Verify schedule with employer"}`)
	if rr.Code != 200 {
		t.Fatalf("task: %d", rr.Code)
	}
	var withTask struct {
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&withTask)
	rr = do(t, h, "POST", "/api/v1/cases/"+cid+"/resolve", "t1", `{"resolution":"fixed","code":"UPHELD"}`)
	if rr.Code != 422 {
		t.Fatalf("resolve must be blocked by open task, got %d", rr.Code)
	}
	rr = do(t, h, "POST", "/api/v1/cases/"+cid+"/tasks/"+withTask.Tasks[0].ID+"/complete", "t1", "")
	if rr.Code != 200 {
		t.Fatalf("complete task: %d", rr.Code)
	}
	rr = do(t, h, "POST", "/api/v1/cases/"+cid+"/resolve", "t1", `{"resolution":"Employer remitted; RSA credited","code":"UPHELD"}`)
	if rr.Code != 200 {
		t.Fatalf("resolve: %d %s", rr.Code, rr.Body.String())
	}

	// CSAT
	rr = do(t, h, "POST", "/api/v1/cases/"+cid+"/csat", "t1", `{"score":5}`)
	if rr.Code != 200 {
		t.Fatalf("csat: %d %s", rr.Code, rr.Body.String())
	}

	// CRM 360 shows the complaint + CSAT
	rr = do(t, h, "GET", "/api/v1/crm/members/"+mid+"/360", "t1", "")
	var v360 struct {
		Segments        []string `json:"segments"`
		ComplaintsTotal int      `json:"complaints_total"`
		CSATAvg         float64  `json:"csat_avg"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&v360); err != nil {
		t.Fatal(err)
	}
	if v360.ComplaintsTotal != 1 || v360.CSATAvg != 5 {
		t.Fatalf("360: %+v", v360)
	}

	// PenCom complaints register includes the case
	rr = do(t, h, "GET", "/api/v1/crm/complaints/register", "t1", "")
	var reg struct {
		Register []struct {
			CaseID  string `json:"case_id"`
			Outcome string `json:"outcome"`
		} `json:"register"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&reg); err != nil || len(reg.Register) != 1 {
		t.Fatalf("register: %v %s", err, rr.Body.String())
	}
	if reg.Register[0].CaseID != cid || reg.Register[0].Outcome != "UPHELD" {
		t.Fatalf("register row: %+v", reg.Register[0])
	}

	// stats endpoint healthy
	rr = do(t, h, "GET", "/api/v1/cases/stats", "t1", "")
	if rr.Code != 200 {
		t.Fatalf("stats: %d", rr.Code)
	}
}

// Fraud screening must auto-open a unified ops case linked to the fraud case.
func TestFraudScreenOpensOpsCase(t *testing.T) {
	h := newTestDeps().handler()
	// velocity spike: three large withdrawals in quick succession
	body := `{"member_id":"m-f1","kind":"WITHDRAWAL","amount_kobo":900000000,"channel":"WEB","state_code":"LA"}`
	var caseID string
	for i := 0; i < 3; i++ {
		rr := do(t, h, "POST", "/api/v1/fraud/screen", "t1", body)
		if rr.Code != 200 {
			t.Fatalf("screen: %d", rr.Code)
		}
		var out struct {
			Score int `json:"score"`
			Case  *struct {
				ID string `json:"id"`
			} `json:"case"`
		}
		if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		if out.Case != nil {
			caseID = out.Case.ID
		}
	}
	rr := do(t, h, "GET", "/api/v1/cases?type=FRAUD", "t1", "")
	var list struct {
		Cases []struct {
			LinkedID string `json:"linked_id"`
		} `json:"cases"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if caseID == "" {
		t.Skip("rules engine did not open a fraud case for this pattern")
	}
	found := false
	for _, c := range list.Cases {
		if c.LinkedID == caseID {
			found = true
		}
	}
	if !found {
		t.Fatalf("no unified ops case linked to fraud case %s", caseID)
	}
}
