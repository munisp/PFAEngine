package main

// Tranche 21/22 end-to-end tests: regulatory surface routes + eKYC/KYB.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestRegulatoryEndToEnd(t *testing.T) {
	h := newTestDeps().handler()

	// forms library seeded with the 9 standard PenCom forms
	rr := do(t, h, "GET", "/api/v1/forms", "t1", "")
	if rr.Code != 200 {
		t.Fatalf("forms list: %d %s", rr.Code, rr.Body)
	}
	var forms struct {
		Forms []map[string]any `json:"forms"`
	}
	json.Unmarshal(rr.Body.Bytes(), &forms)
	if len(forms.Forms) != 9 {
		t.Fatalf("expected 9 seeded forms, got %d", len(forms.Forms))
	}

	// document upload -> pending -> approve
	rr = do(t, h, "POST", "/api/v1/documents", "t1",
		`{"member_id":"m1","kind":"NIN_SLIP","filename":"nin.pdf","mime":"application/pdf","size":1024,"ref":"ref://1","sha256":"abc"}`)
	if rr.Code != 201 {
		t.Fatalf("doc submit: %d %s", rr.Code, rr.Body)
	}
	var doc map[string]any
	json.Unmarshal(rr.Body.Bytes(), &doc)
	docID := doc["id"].(string)
	rr = do(t, h, "GET", "/api/v1/documents/pending", "t1", "")
	if !strings.Contains(rr.Body.String(), docID) {
		t.Fatalf("pending should contain %s: %s", docID, rr.Body)
	}
	rr = do(t, h, "POST", "/api/v1/documents/"+docID+"/review", "t1", `{"approve":true,"note":"ok"}`)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "APPROVED") {
		t.Fatalf("doc review: %d %s", rr.Code, rr.Body)
	}

	// branch + appointment
	rr = do(t, h, "POST", "/api/v1/branches", "t1",
		`{"name":"Ikeja","city":"Lagos","state":"Lagos","address":"1 Allen Ave"}`)
	if rr.Code != 201 {
		t.Fatalf("branch add: %d %s", rr.Code, rr.Body)
	}
	var br map[string]any
	json.Unmarshal(rr.Body.Bytes(), &br)
	branchID := br["id"].(string)
	rr = do(t, h, "GET", "/api/v1/branches/"+branchID+"/availability?date=2030-01-06", "t1", "")
	if rr.Code != 200 {
		t.Fatalf("availability: %d %s", rr.Code, rr.Body)
	}
	rr = do(t, h, "POST", "/api/v1/appointments", "t1",
		`{"branch_id":"`+branchID+`","member_id":"m1","date":"2030-01-06","slot":0,"purpose":"RSA registration"}`)
	if rr.Code != 201 {
		t.Fatalf("book: %d %s", rr.Code, rr.Body)
	}
	// double-booking the same slot must fail
	rr = do(t, h, "POST", "/api/v1/appointments", "t1",
		`{"branch_id":"`+branchID+`","member_id":"m2","date":"2030-01-06","slot":0,"purpose":"x"}`)
	if rr.Code == 201 {
		t.Fatal("double-booking must be rejected")
	}

	// disclosures: WORM — duplicate kind+period rejected
	rr = do(t, h, "POST", "/api/v1/disclosures", "t1",
		`{"kind":"AUDITED_RSA_FUND","title":"Audited FS 2025","period":"2025","ref":"ref://fs","sha256":"x"}`)
	if rr.Code != 201 {
		t.Fatalf("disclosure publish: %d %s", rr.Code, rr.Body)
	}
	rr = do(t, h, "POST", "/api/v1/disclosures", "t1",
		`{"kind":"AUDITED_RSA_FUND","title":"Audited FS 2025 dup","period":"2025","ref":"ref://fs2","sha256":"y"}`)
	if rr.Code != 409 {
		t.Fatalf("duplicate disclosure must be 409, got %d", rr.Code)
	}

	// news CMS: create -> publish -> feed
	rr = do(t, h, "POST", "/api/v1/news", "t1",
		`{"title":"PenCom issues new guidelines","body":"...","category":"PENCOM_NOTICE","pinned":true}`)
	if rr.Code != 201 {
		t.Fatalf("news create: %d %s", rr.Code, rr.Body)
	}
	var art map[string]any
	json.Unmarshal(rr.Body.Bytes(), &art)
	artID := art["id"].(string)
	if rr = do(t, h, "POST", "/api/v1/news/"+artID+"/publish", "t1", ""); rr.Code != 200 {
		t.Fatalf("news publish: %d %s", rr.Code, rr.Body)
	}
	rr = do(t, h, "GET", "/api/v1/news", "t1", "")
	if !strings.Contains(rr.Body.String(), artID) {
		t.Fatalf("feed should contain published article: %s", rr.Body)
	}

	// fund performance: publish NAVs then compute
	for _, nav := range []string{
		`{"date":"2024-11-15","price":100000}`, `{"date":"2025-11-15","price":108000}`,
		`{"date":"2026-08-15","price":109000}`, `{"date":"2026-11-15","price":110300}`,
	} {
		if rr = do(t, h, "POST", "/api/v1/funds/FUND_II/nav", "t1", nav); rr.Code != 201 {
			t.Fatalf("nav publish: %d %s", rr.Code, rr.Body)
		}
	}
	rr = do(t, h, "GET", "/api/v1/funds/performance?fund_id=FUND_II", "t1", "")
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "return_12m_bps") {
		t.Fatalf("performance: %d %s", rr.Code, rr.Body)
	}

	// tenancy licence profile
	rr = do(t, h, "GET", "/api/v1/tenancy/license", "t1", "")
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "PFA-TEST") {
		t.Fatalf("license get: %d %s", rr.Code, rr.Body)
	}
	rr = do(t, h, "PUT", "/api/v1/tenancy/license", "t1",
		`{"license_no":"PFA-1234","class":"PFA","status":"ACTIVE","issued_at":"2020-01-01","expires_at":"2030-01-01"}`)
	if rr.Code != 200 {
		t.Fatalf("license set: %d %s", rr.Code, rr.Body)
	}
	rr = do(t, h, "GET", "/api/v1/tenancy/license", "t1", "")
	if !strings.Contains(rr.Body.String(), "PFA-1234") {
		t.Fatalf("license update not reflected: %s", rr.Body)
	}
}

func TestEKYCFailClosedEndToEnd(t *testing.T) {
	h := newTestDeps().handler() // KYC_SERVICE_URL unset -> fail-closed

	rr := do(t, h, "GET", "/api/v1/ekyc/health", "t1", "")
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"ok":false`) {
		t.Fatalf("health must report not-ok when sidecar unset: %d %s", rr.Code, rr.Body)
	}

	rr = do(t, h, "POST", "/api/v1/ekyc/sessions", "t1", `{"member_id":"m1"}`)
	if rr.Code != 201 {
		t.Fatalf("start: %d %s", rr.Code, rr.Body)
	}
	var sess map[string]any
	json.Unmarshal(rr.Body.Bytes(), &sess)
	sid := sess["id"].(string)

	rr = do(t, h, "POST", "/api/v1/ekyc/sessions/"+sess["id"].(string)+"/document", "t1",
		`{"kind":"NIN_SLIP","ref":"ref://nin","image_b64":"aW1hZ2U="}`)
	if rr.Code != 200 {
		t.Fatalf("document: %d %s", rr.Code, rr.Body)
	}
	rr = do(t, h, "POST", "/api/v1/ekyc/sessions/"+sid+"/identity", "t1",
		`{"nin":"12345678901","bvn":"98765432109","name":"Amara Okafor","dob":"1992-01-01","phone":"0803"}`)
	if rr.Code != 200 {
		t.Fatalf("identity: %d %s", rr.Code, rr.Body)
	}
	rr = do(t, h, "POST", "/api/v1/ekyc/sessions/"+sid+"/biometric", "t1", `{"frames":["c2VsZmll"]}`)
	if rr.Code != 200 {
		t.Fatalf("biometric: %d %s", rr.Code, rr.Body)
	}
	rr = do(t, h, "POST", "/api/v1/ekyc/sessions/"+sid+"/decide", "t1", "")
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "MANUAL_REVIEW") {
		t.Fatalf("fail-closed decision must be MANUAL_REVIEW: %d %s", rr.Code, rr.Body)
	}
	json.Unmarshal(rr.Body.Bytes(), &sess)
	if sess["case_id"] == "" {
		t.Fatal("manual review must open a KYC_REVIEW case")
	}

	// staff override approves
	rr = do(t, h, "POST", "/api/v1/ekyc/sessions/"+sid+"/override", "t1", `{"approve":true,"note":"verified"}`)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "AUTO_APPROVE") {
		t.Fatalf("override: %d %s", rr.Code, rr.Body)
	}
}

func TestKYBEndToEnd(t *testing.T) {
	h := newTestDeps().handler()
	rr := do(t, h, "POST", "/api/v1/kyb/businesses", "t1", `{
		"employer_id":"emp1","name":"Acme Ltd","rc_number":"RC123456","tin":"12345678",
		"directors":[{"name":"Amara Okafor","dob":"1980-05-05","role":"MD"}],
		"ubos":[{"name":"Amara Okafor","dob":"1980-05-05","ownership_pct":60}],
		"doc_refs":["ref://cac-cert"]}`)
	if rr.Code != 201 || !strings.Contains(rr.Body.String(), "VERIFIED") {
		t.Fatalf("kyb submit: %d %s", rr.Code, rr.Body)
	}
	// bad RC rejected
	rr = do(t, h, "POST", "/api/v1/kyb/businesses", "t1",
		`{"employer_id":"emp2","name":"Bad","rc_number":"XX1","directors":[],"ubos":[]}`)
	if rr.Code != 400 {
		t.Fatalf("bad RC must be 400, got %d", rr.Code)
	}
	// no UBO >= 25% -> manual review with case
	rr = do(t, h, "POST", "/api/v1/kyb/businesses", "t1", `{
		"employer_id":"emp3","name":"Fragile Ltd","rc_number":"RC99999",
		"directors":[{"name":"A B","dob":"1970-01-01"}],
		"ubos":[{"name":"A B","dob":"1970-01-01","ownership_pct":10},{"name":"C D","dob":"1971-01-01","ownership_pct":10}],
		"doc_refs":["ref://cac"]}`)
	if rr.Code != 201 {
		t.Fatalf("kyb submit 3: %d %s", rr.Code, rr.Body)
	}
	var biz map[string]any
	json.Unmarshal(rr.Body.Bytes(), &biz)
	if biz["status"] != "MANUAL_REVIEW" || biz["case_id"] == "" {
		t.Fatalf("expected manual review with case: %s", rr.Body)
	}
	rr = do(t, h, "POST", "/api/v1/kyb/businesses/"+biz["id"].(string)+"/review", "t1",
		`{"approve":true,"note":"structure confirmed"}`)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "VERIFIED") {
		t.Fatalf("kyb review: %d %s", rr.Code, rr.Body)
	}
}

func TestKYBRoutesRequireJSON(t *testing.T) {
	h := newTestDeps().handler()
	rr := do(t, h, "POST", "/api/v1/kyb/businesses", "t1", `{bad json`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}
