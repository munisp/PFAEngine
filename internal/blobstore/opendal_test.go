package blobstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockStorageSvc is an in-memory stand-in for storage-svc (the Rust
// OpenDAL microservice): same routes, same bearer auth, same presign
// contract — so the Go client is testable without a Rust binary.
type mockStorageSvc struct {
	t       *testing.T
	token   string
	mu      sync.Mutex
	objects map[string][]byte
	// presignCalls records the (key, ttl_secs) pairs POSTed to /presign.
	presignCalls [][2]interface{}
}

func newMockStorageSvc(t *testing.T, token string) (*mockStorageSvc, *httptest.Server) {
	m := &mockStorageSvc{t: t, token: token, objects: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(srv.Close)
	return m, srv
}

func (m *mockStorageSvc) serve(w http.ResponseWriter, r *http.Request) {
	writeErr := func(code int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		fmt.Fprintf(w, `{"error":{"code":%d,"message":%q}}`, code, msg)
	}
	authed := r.Header.Get("Authorization") == "Bearer "+m.token

	if r.URL.Path == "/presign" && r.Method == http.MethodPost {
		if !authed {
			writeErr(http.StatusUnauthorized, "missing bearer token")
			return
		}
		var req presignRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(http.StatusBadRequest, "bad json")
			return
		}
		m.mu.Lock()
		m.presignCalls = append(m.presignCalls, [2]interface{}{req.Key, req.TTLSecs})
		m.mu.Unlock()
		exp := time.Now().Add(time.Duration(req.TTLSecs) * time.Second).Unix()
		out := presignResponse{
			URL:       fmt.Sprintf("/obj/%s?exp=%d&sig=fakesig", req.Key, exp),
			ExpiresAt: exp,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
		return
	}

	key := strings.TrimPrefix(r.URL.Path, "/obj/")
	if key == r.URL.Path {
		writeErr(http.StatusNotFound, "unknown route")
		return
	}
	switch r.Method {
	case http.MethodPut:
		if !authed {
			writeErr(http.StatusUnauthorized, "missing bearer token")
			return
		}
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.objects[key] = body
		m.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		if !authed {
			writeErr(http.StatusUnauthorized, "missing bearer token")
			return
		}
		m.mu.Lock()
		body, ok := m.objects[key]
		m.mu.Unlock()
		if !ok {
			writeErr(http.StatusNotFound, "not found")
			return
		}
		_, _ = w.Write(body)
	case http.MethodDelete:
		if !authed {
			writeErr(http.StatusUnauthorized, "missing bearer token")
			return
		}
		m.mu.Lock()
		delete(m.objects, key)
		m.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	default:
		writeErr(http.StatusMethodNotAllowed, "method")
	}
}

func TestOpenDALRoundTrip(t *testing.T) {
	_, srv := newMockStorageSvc(t, "s3cr3t")
	s, err := NewOpenDAL(srv.URL, "s3cr3t")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	payload := []byte("blob-bytes-\x00\x01")
	if err := s.Put(ctx, "t1/doc1/file.bin", bytes.NewReader(payload), int64(len(payload)), "application/octet-stream"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rc, err := s.Get(ctx, "t1/doc1/file.bin")
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
	if err := s.Delete(ctx, "t1/doc1/file.bin"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, "t1/doc1/file.bin"); err == nil {
		t.Fatal("Get after Delete should fail")
	}
	// Delete of a missing key is a no-op (idempotent cleanup path).
	if err := s.Delete(ctx, "t1/doc1/file.bin"); err != nil {
		t.Fatalf("Delete missing: %v", err)
	}
}

func TestOpenDALRejectsUnsafeConfig(t *testing.T) {
	if _, err := NewOpenDAL("", "tok"); err == nil {
		t.Fatal("empty base URL must fail")
	}
	if _, err := NewOpenDAL("http://x", ""); err == nil {
		t.Fatal("empty token must fail")
	}
	if _, err := NewOpenDAL("storage-svc:8110", "tok"); err == nil {
		t.Fatal("scheme-less base URL must fail")
	}
}

func TestOpenDALPutSizeMismatch(t *testing.T) {
	_, srv := newMockStorageSvc(t, "tok")
	s, err := NewOpenDAL(srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	err = s.Put(context.Background(), "a/b/c", strings.NewReader("short"), 100, "")
	if err == nil || !strings.Contains(err.Error(), "expected 100") {
		t.Fatalf("size mismatch must fail before any request, got %v", err)
	}
}

func TestOpenDALPutRejectsTraversal(t *testing.T) {
	_, srv := newMockStorageSvc(t, "tok")
	s, err := NewOpenDAL(srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"../escape", "a/../../b", "/abs", "a//b"} {
		if err := s.Put(context.Background(), key, strings.NewReader("x"), 1, ""); err == nil {
			t.Fatalf("Put(%q) must be rejected", key)
		}
		if _, err := s.Get(context.Background(), key); err == nil {
			t.Fatalf("Get(%q) must be rejected", key)
		}
	}
}

func TestOpenDALPresignDelegates(t *testing.T) {
	m, srv := newMockStorageSvc(t, "tok")
	s, err := NewOpenDAL(srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.PresignGet(context.Background(), "t1/doc1/file.bin", 5*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	if !strings.Contains(u, "/obj/t1/doc1/file.bin?exp=") || !strings.Contains(u, "sig=") {
		t.Fatalf("unexpected presigned URL %q", u)
	}
	m.mu.Lock()
	calls := m.presignCalls
	m.mu.Unlock()
	if len(calls) != 1 || calls[0][0] != "t1/doc1/file.bin" || calls[0][1] != int64(300) {
		t.Fatalf("storage-svc saw %v, want one call (t1/doc1/file.bin, 300)", calls)
	}
	// TTL bounds mirror the S3 backend contract.
	if _, err := s.PresignGet(context.Background(), "k", 0); err == nil {
		t.Fatal("ttl=0 must fail")
	}
	if _, err := s.PresignGet(context.Background(), "k", 8*24*time.Hour); err == nil {
		t.Fatal("ttl=8d must fail")
	}
	if _, err := s.PresignGet(context.Background(), "../bad", time.Minute); err == nil {
		t.Fatal("unsafe key must fail")
	}
}

func TestOpenDALServerRejectsBadToken(t *testing.T) {
	_, srv := newMockStorageSvc(t, "right-token")
	s, err := NewOpenDAL(srv.URL, "wrong-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(context.Background(), "a/b", strings.NewReader("x"), 1, ""); err == nil {
		t.Fatal("Put with wrong token must fail (401)")
	}
	if _, err := s.Get(context.Background(), "a/b"); err == nil {
		t.Fatal("Get with wrong token must fail (401)")
	}
}

// TestFromEnvPriority pins the backend selection contract:
// STORAGE_SVC_URL (opendal) > S3 direct > FS fallback.
func TestFromEnvPriority(t *testing.T) {
	_, srv := newMockStorageSvc(t, "tok")

	t.Run("opendal wins over S3", func(t *testing.T) {
		t.Setenv("STORAGE_SVC_URL", srv.URL)
		t.Setenv("STORAGE_SVC_TOKEN", "tok")
		t.Setenv("S3_ENDPOINT", "minio:9000")
		t.Setenv("S3_BUCKET", "docs")
		t.Setenv("S3_ACCESS_KEY", "ak")
		t.Setenv("S3_SECRET_KEY", "sk")
		st, warn, err := FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if warn != "" {
			t.Fatalf("warning = %q, want empty on the primary path", warn)
		}
		if _, ok := st.(*opendalStore); !ok {
			t.Fatalf("got %T, want *opendalStore", st)
		}
	})

	t.Run("opendal without token fails closed", func(t *testing.T) {
		t.Setenv("STORAGE_SVC_URL", srv.URL)
		st, _, err := FromEnv()
		if err == nil || st != nil {
			t.Fatalf("missing STORAGE_SVC_TOKEN must fail, got %v %v", st, err)
		}
		if !strings.Contains(err.Error(), "STORAGE_SVC_TOKEN") {
			t.Fatalf("error must name the missing variable: %v", err)
		}
	})

	t.Run("S3 when no storage-svc", func(t *testing.T) {
		t.Setenv("S3_ENDPOINT", "minio:9000")
		t.Setenv("S3_BUCKET", "docs")
		t.Setenv("S3_ACCESS_KEY", "ak")
		t.Setenv("S3_SECRET_KEY", "sk")
		st, warn, err := FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if warn != "" {
			t.Fatalf("warning = %q", warn)
		}
		if _, ok := st.(*s3Store); !ok {
			t.Fatalf("got %T, want *s3Store", st)
		}
	})

	t.Run("FS fallback with warning", func(t *testing.T) {
		t.Setenv("BLOB_FS_ROOT", t.TempDir())
		st, warn, err := FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if warn == "" {
			t.Fatal("FS fallback must come with a warning")
		}
		if _, ok := st.(*fsStore); !ok {
			t.Fatalf("got %T, want *fsStore", st)
		}
	})
}
