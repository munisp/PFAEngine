package blobstore

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestFSRoundTrip(t *testing.T) {
	s, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key, err := Key("tenant-1", "blob-01", "passport photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if key != "tenant-1/blob-01/passport_photo.jpg" {
		t.Fatalf("key = %q", key)
	}
	payload := []byte("fake-jpeg-bytes-\x00\x01\x02")
	if err := s.Put(ctx, key, bytes.NewReader(payload), int64(len(payload)), "image/jpeg"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rc, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("round trip mismatch: got %q", got)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, key); err == nil {
		t.Fatal("Get after Delete should fail")
	}
	// Delete of a missing key is a no-op (idempotent cleanup path).
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("Delete missing: %v", err)
	}
}

func TestFSPresignIsFileURL(t *testing.T) {
	s, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.PresignGet(context.Background(), "t/d/f.pdf", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u, "file://") {
		t.Fatalf("fs presign = %q, want file:// URL", u)
	}
}

func TestFSSizeMismatch(t *testing.T) {
	s, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = s.Put(context.Background(), "t/d/f", strings.NewReader("abc"), 10, "application/pdf")
	if err == nil {
		t.Fatal("size mismatch should fail")
	}
}

func TestKeySanitization(t *testing.T) {
	ok := []struct{ tenant, doc, file, want string }{
		{"demo", "doc-000001", "nin.pdf", "demo/doc-000001/nin.pdf"},
		{"pfa_1", "b1", "My Passport Photo.JPG", "pfa_1/b1/My_Passport_Photo.JPG"},
	}
	for _, tc := range ok {
		got, err := Key(tc.tenant, tc.doc, tc.file)
		if err != nil {
			t.Fatalf("Key(%q,%q,%q): %v", tc.tenant, tc.doc, tc.file, err)
		}
		if got != tc.want {
			t.Fatalf("Key(%q,%q,%q) = %q, want %q", tc.tenant, tc.doc, tc.file, got, tc.want)
		}
	}
	bad := [][3]string{
		{"demo", "doc", "../etc/passwd"},
		{"demo", "doc", ".."},
		{"demo", "..", "f.pdf"},
		{"../evil", "doc", "f.pdf"},
		{"demo", "doc", "/etc/passwd"}, // absolute path
		{"demo", "doc", `..\windows`},  // windows traversal
		{"demo", "doc", ""},            // empty filename
		{"", "doc", "f.pdf"},           // empty tenant
		{"demo", "doc", "a/b/c.pdf"},   // embedded separator
	}
	for _, tc := range bad {
		if k, err := Key(tc[0], tc[1], tc[2]); err == nil {
			t.Fatalf("Key(%q,%q,%q) = %q, want error", tc[0], tc[1], tc[2], k)
		}
	}
	// Backends re-validate keys even when callers bypass Key().
	s, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(context.Background(), "../escape.txt", strings.NewReader("x"), 1, "text/plain"); err == nil {
		t.Fatal("fs Put with traversal key should fail")
	}
	s3 := NewS3(Config{Endpoint: "minio:9000", Bucket: "b", AccessKey: "a", SecretKey: "s", Region: "us-east-1"})
	if _, err := s3.PresignGet(context.Background(), "../escape", time.Minute); err == nil {
		t.Fatal("s3 PresignGet with traversal key should fail")
	}
}

func TestFromEnv(t *testing.T) {
	for _, k := range append(append([]string{}, s3RequiredEnv...), "S3_REGION", "S3_USE_SSL", "BLOB_REQUIRED", "BLOB_FS_ROOT") {
		t.Setenv(k, "")
	}
	// Nothing configured -> FS fallback with warning.
	root := t.TempDir()
	t.Setenv("BLOB_FS_ROOT", root)
	s, warn, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv fallback: %v", err)
	}
	if warn == "" {
		t.Fatal("expected a fallback warning")
	}
	if _, ok := s.(*fsStore); !ok {
		t.Fatalf("fallback store type = %T", s)
	}

	// BLOB_REQUIRED=true -> hard error.
	t.Setenv("BLOB_REQUIRED", "true")
	if _, _, err := FromEnv(); err == nil {
		t.Fatal("BLOB_REQUIRED=true with no S3 config should fail")
	}

	// Partial config -> error naming the missing variables (fail-closed).
	t.Setenv("BLOB_REQUIRED", "")
	t.Setenv("S3_ENDPOINT", "minio:9000")
	t.Setenv("S3_ACCESS_KEY", "ak")
	_, _, err = FromEnv()
	if err == nil {
		t.Fatal("partial config should fail")
	}
	for _, want := range []string{"S3_BUCKET", "S3_SECRET_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should name %s", err, want)
		}
	}

	// Full config -> S3 store, no warning; region/SSL defaults applied.
	t.Setenv("S3_BUCKET", "kyc-docs")
	t.Setenv("S3_SECRET_KEY", "sk")
	s, warn, err = FromEnv()
	if err != nil {
		t.Fatalf("FromEnv s3: %v", err)
	}
	if warn != "" {
		t.Fatalf("unexpected warning %q", warn)
	}
	s3, ok := s.(*s3Store)
	if !ok {
		t.Fatalf("store type = %T", s)
	}
	if s3.cfg.Region != "us-east-1" || !s3.cfg.UseSSL {
		t.Fatalf("defaults: region=%q ssl=%v", s3.cfg.Region, s3.cfg.UseSSL)
	}
}

// TestS3PresignShape validates the SigV4 query-string presign against the
// AWS documentation's worked example structure — no network involved.
func TestS3PresignShape(t *testing.T) {
	s3 := NewS3(Config{
		Endpoint: "minio:9000", Bucket: "kyc-docs",
		AccessKey: "AKIAEXAMPLE", SecretKey: "secret", Region: "us-east-1",
	}).(*s3Store)
	s3.now = func() time.Time { return time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC) }
	u, err := s3.PresignGet(context.Background(), "demo/blob-1/nin.pdf", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u, "http://minio:9000/kyc-docs/demo/blob-1/nin.pdf?") {
		t.Fatalf("presign URL = %q", u)
	}
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	if got := q.Get("X-Amz-Algorithm"); got != "AWS4-HMAC-SHA256" {
		t.Fatalf("algorithm = %q", got)
	}
	if got := q.Get("X-Amz-Credential"); got != "AKIAEXAMPLE/20240102/us-east-1/s3/aws4_request" {
		t.Fatalf("credential = %q", got)
	}
	if got := q.Get("X-Amz-Date"); got != "20240102T030405Z" {
		t.Fatalf("date = %q", got)
	}
	if got := q.Get("X-Amz-Expires"); got != "900" {
		t.Fatalf("expires = %q", got)
	}
	if got := q.Get("X-Amz-SignedHeaders"); got != "host" {
		t.Fatalf("signed headers = %q", got)
	}
	sig := q.Get("X-Amz-Signature")
	if len(sig) != 64 {
		t.Fatalf("signature %q not 64 hex chars", sig)
	}
	if _, err := hex.DecodeString(sig); err != nil {
		t.Fatalf("signature not hex: %v", err)
	}
	// Deterministic: same inputs -> same signature; different key -> differ.
	u2, _ := s3.PresignGet(context.Background(), "demo/blob-1/nin.pdf", 15*time.Minute)
	if u != u2 {
		t.Fatal("presign not deterministic")
	}
	u3, _ := s3.PresignGet(context.Background(), "demo/blob-2/nin.pdf", 15*time.Minute)
	if u == u3 {
		t.Fatal("signature should bind to the object key")
	}
	// Reject absurd TTLs.
	if _, err := s3.PresignGet(context.Background(), "k", 0); err == nil {
		t.Fatal("zero ttl should fail")
	}
	if _, err := s3.PresignGet(context.Background(), "k", 8*24*time.Hour); err == nil {
		t.Fatal("ttl > 7d should fail")
	}
}

// TestS3HeaderSigning exercises Put/Get/Delete against a stub server and
// asserts every request carries a well-formed SigV4 Authorization header.
func TestS3HeaderSigning(t *testing.T) {
	var auths, contentSHA []string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		contentSHA = append(contentSHA, r.Header.Get("X-Amz-Content-Sha256"))
		if r.Method == http.MethodPut {
			gotBody, _ = io.ReadAll(r.Body)
		}
		if r.Method == http.MethodGet {
			w.Write([]byte("blob-bytes"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	endpoint := strings.TrimPrefix(srv.URL, "http://")
	s3 := NewS3(Config{
		Endpoint: endpoint, Bucket: "b",
		AccessKey: "ak", SecretKey: "sk", Region: "us-east-1",
	}).(*s3Store)
	ctx := context.Background()
	if err := s3.Put(ctx, "t/d/f.pdf", strings.NewReader("hello"), 5, "application/pdf"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if string(gotBody) != "hello" {
		t.Fatalf("server saw body %q", gotBody)
	}
	rc, err := s3.Get(ctx, "t/d/f.pdf")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "blob-bytes" {
		t.Fatalf("Get body = %q", b)
	}
	if err := s3.Delete(ctx, "t/d/f.pdf"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(auths) != 3 {
		t.Fatalf("expected 3 requests, got %d", len(auths))
	}
	for i, a := range auths {
		if !strings.HasPrefix(a, "AWS4-HMAC-SHA256 Credential=ak/") ||
			!strings.Contains(a, "/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=") {
			t.Fatalf("auth[%d] malformed: %q", i, a)
		}
	}
	// The Put payload hash is the SHA-256 of "hello".
	if contentSHA[0] != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("payload hash = %q", contentSHA[0])
	}
	// Empty-body requests use the well-known empty SHA-256.
	if contentSHA[1] != emptySHA256 || contentSHA[2] != emptySHA256 {
		t.Fatalf("empty hashes = %q %q", contentSHA[1], contentSHA[2])
	}
}

// TestS3ErrorStatus ensures non-2xx responses surface as errors.
func TestS3ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	s3 := NewS3(Config{
		Endpoint: strings.TrimPrefix(srv.URL, "http://"), Bucket: "b",
		AccessKey: "ak", SecretKey: "sk", Region: "us-east-1",
	})
	if err := s3.Put(context.Background(), "t/d/f", strings.NewReader("x"), 1, "application/pdf"); err == nil {
		t.Fatal("Put against 403 should fail")
	}
	if _, err := s3.Get(context.Background(), "t/d/f"); err == nil {
		t.Fatal("Get against 403 should fail")
	}
}
