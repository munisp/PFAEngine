package main

// Gap 7 handler tests: upload (base64 -> blobstore -> docupload metadata),
// presigned download URL gating, and fail-closed behaviour when the blob
// store is unconfigured. Routes are exercised through the handler methods
// directly (apiRoutes registration is an integration step).

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/munisp/pfaengine/internal/blobstore"
	"github.com/munisp/pfaengine/internal/httpsrv"
)

func blobTestDeps(t *testing.T) *deps {
	t.Helper()
	d := newTestDeps()
	s, err := blobstore.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d.blob = s
	return d
}

// doTenant runs one handler behind the tenant middleware (as the mux does).
func doTenant(t *testing.T, h http.HandlerFunc, method, path, tenant, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if tenant != "" {
		req.Header.Set(httpsrv.TenantHeader, tenant)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	httpsrv.Tenant(h).ServeHTTP(rr, req)
	return rr
}

func TestDocUploadAndDownloadURL(t *testing.T) {
	d := blobTestDeps(t)
	payload := []byte("fake-pdf-bytes-for-kyc")
	body := `{"member_id":"m1","kind":"NIN_SLIP","filename":"nin slip.pdf","mime":"application/pdf","data_b64":"` +
		base64.StdEncoding.EncodeToString(payload) + `"}`
	rr := doTenant(t, d.docUploadBlob, "POST", "/api/v1/documents/upload", "t1", body, nil)
	if rr.Code != 201 {
		t.Fatalf("upload: %d %s", rr.Code, rr.Body)
	}
	var doc map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	docID, _ := doc["id"].(string)
	ref, _ := doc["ref"].(string)
	if docID == "" || ref == "" {
		t.Fatalf("doc missing id/ref: %v", doc)
	}
	sum := sha256.Sum256(payload)
	if doc["sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("server-side sha256 = %v", doc["sha256"])
	}
	// The blob actually landed in the store, bytes intact.
	rc, err := d.blob.Get(context.Background(), ref)
	if err != nil {
		t.Fatalf("blob get: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != string(payload) {
		t.Fatalf("blob bytes mismatch: %q", got)
	}

	// Owner may fetch a download URL.
	rr = doTenant(t, func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", docID)
		d.docDownloadURL(w, r)
	}, "GET", "/api/v1/documents/"+docID+"/url?member_id=m1", "t1", "", nil)
	if rr.Code != 200 {
		t.Fatalf("owner url: %d %s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), `"url"`) {
		t.Fatalf("no url in response: %s", rr.Body)
	}

	// A different member is refused; staff pass via X-Actor-Role.
	rr = doTenant(t, func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", docID)
		d.docDownloadURL(w, r)
	}, "GET", "/api/v1/documents/"+docID+"/url?member_id=m2", "t1", "", nil)
	if rr.Code != 403 {
		t.Fatalf("foreign member: %d, want 403", rr.Code)
	}
	rr = doTenant(t, func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", docID)
		d.docDownloadURL(w, r)
	}, "GET", "/api/v1/documents/"+docID+"/url", "t1", "", map[string]string{"X-Actor-Role": "STAFF"})
	if rr.Code != 200 {
		t.Fatalf("staff url: %d %s", rr.Code, rr.Body)
	}
}

func TestDocUploadValidation(t *testing.T) {
	d := blobTestDeps(t)
	// Mime allowlist.
	rr := doTenant(t, d.docUploadBlob, "POST", "/api/v1/documents/upload", "t1",
		`{"member_id":"m1","kind":"NIN_SLIP","filename":"evil.html","mime":"text/html","data_b64":"PGI+"}`, nil)
	if rr.Code != 400 || !strings.Contains(rr.Body.String(), "MIME_NOT_ALLOWED") {
		t.Fatalf("mime: %d %s", rr.Code, rr.Body)
	}
	// Bad base64.
	rr = doTenant(t, d.docUploadBlob, "POST", "/api/v1/documents/upload", "t1",
		`{"member_id":"m1","kind":"NIN_SLIP","filename":"f.pdf","mime":"application/pdf","data_b64":"!!!"}`, nil)
	if rr.Code != 400 {
		t.Fatalf("base64: %d %s", rr.Code, rr.Body)
	}
	// Traversal filename rejected (Key sanitisation fails closed).
	rr = doTenant(t, d.docUploadBlob, "POST", "/api/v1/documents/upload", "t1",
		`{"member_id":"m1","kind":"NIN_SLIP","filename":"../../etc/passwd","mime":"application/pdf","data_b64":"`+
			base64.StdEncoding.EncodeToString([]byte("x"))+`"}`, nil)
	if rr.Code != 400 || !strings.Contains(rr.Body.String(), "BAD_FILENAME") {
		t.Fatalf("traversal: %d %s", rr.Code, rr.Body)
	}
	// Unknown kind -> metadata submit fails; blob must be cleaned up.
	rr = doTenant(t, d.docUploadBlob, "POST", "/api/v1/documents/upload", "t1",
		`{"member_id":"m1","kind":"NOPE","filename":"f.pdf","mime":"application/pdf","data_b64":"`+
			base64.StdEncoding.EncodeToString([]byte("x"))+`"}`, nil)
	if rr.Code != 400 {
		t.Fatalf("kind: %d %s", rr.Code, rr.Body)
	}
	if docs := d.docupload.ByMember("t1", "m1"); len(docs) != 0 {
		t.Fatalf("rejected submit left metadata: %v", docs)
	}
}

func TestDocBlobFailClosed(t *testing.T) {
	d := newTestDeps() // blob == nil
	rr := doTenant(t, d.docUploadBlob, "POST", "/api/v1/documents/upload", "t1",
		`{"member_id":"m1","kind":"NIN_SLIP","filename":"f.pdf","mime":"application/pdf","data_b64":"eA=="}`, nil)
	if rr.Code != 503 || !strings.Contains(rr.Body.String(), "BLOB_UNCONFIGURED") {
		t.Fatalf("upload unconfigured: %d %s", rr.Code, rr.Body)
	}
	rr = doTenant(t, func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", "doc-000001")
		d.docDownloadURL(w, r)
	}, "GET", "/api/v1/documents/doc-000001/url", "t1", "", map[string]string{"X-Actor-Role": "ADMIN"})
	if rr.Code != 503 || !strings.Contains(rr.Body.String(), "BLOB_UNCONFIGURED") {
		t.Fatalf("url unconfigured: %d %s", rr.Code, rr.Body)
	}
}
