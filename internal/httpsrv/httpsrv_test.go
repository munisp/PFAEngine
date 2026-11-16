package httpsrv

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/authn"
)

func newTestServer(t *testing.T, handler http.Handler, opts ...Option) *httptest.Server {
	t.Helper()
	s := New("test-svc", "127.0.0.1:0", handler, opts...)
	// Swap the production listener for httptest's TLS-less test server but
	// keep the Server's mux (probes + request-ID middleware).
	ts := httptest.NewServer(s.srv.Handler)
	t.Cleanup(ts.Close)
	return ts
}

func TestHealthzAlwaysOK(t *testing.T) {
	ts := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("healthz status = %d, want 200", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" || body["service"] != "test-svc" {
		t.Fatalf("healthz body = %v", body)
	}
}

func TestReadyzHonoursReadyHook(t *testing.T) {
	ready := false
	ts := newTestServer(t,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		WithReady(func() bool { return ready }))

	resp, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("readyz (not ready) status = %d, want 503", resp.StatusCode)
	}
	var env ErrorBody
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Code != "NOT_READY" || env.Error == "" {
		t.Fatalf("readyz envelope = %+v", env)
	}

	ready = true
	resp2, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("readyz (ready) status = %d, want 200", resp2.StatusCode)
	}
}

func TestTenantMiddlewareEnforcement(t *testing.T) {
	cases := []struct {
		name   string
		tenant string
		want   int
	}{
		{"missing header rejected", "", 400},
		{"blank header rejected", "   ", 400},
		{"present header accepted", "demo-pfa", 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotTenant string
			inner := Tenant(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotTenant = TenantOf(r)
				WriteJSON(w, 200, map[string]string{"ok": "1"})
			}))
			ts := newTestServer(t, inner)
			req, _ := http.NewRequest("GET", ts.URL+"/api/v1/thing", nil)
			if tc.tenant != "" {
				req.Header.Set(TenantHeader, tc.tenant)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
			if tc.want == 200 && gotTenant != strings.TrimSpace(tc.tenant) {
				t.Fatalf("tenant in context = %q, want %q", gotTenant, tc.tenant)
			}
			if tc.want == 400 {
				var env ErrorBody
				if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
					t.Fatal(err)
				}
				if env.Code != "TENANT_REQUIRED" {
					t.Fatalf("error code = %q", env.Code)
				}
			}
		})
	}
}

func TestRequestIDGeneratedAndEchoed(t *testing.T) {
	var ctxID string
	ts := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxID = RequestIDOf(r)
		w.WriteHeader(204)
	}))
	resp, err := http.Get(ts.URL + "/anything")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	hdr := resp.Header.Get(RequestIDHeader)
	if hdr == "" || !strings.HasPrefix(hdr, "req-") {
		t.Fatalf("generated request ID = %q", hdr)
	}
	if ctxID != hdr {
		t.Fatalf("context ID %q != response header %q", ctxID, hdr)
	}
}

func TestRequestIDPropagatesInbound(t *testing.T) {
	ts := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(RequestIDHeader); got != "req-fixed-1" {
			t.Errorf("upstream header = %q", got)
		}
		w.WriteHeader(204)
	}))
	req, _ := http.NewRequest("GET", ts.URL+"/x", nil)
	req.Header.Set(RequestIDHeader, "req-fixed-1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get(RequestIDHeader); got != "req-fixed-1" {
		t.Fatalf("echoed request ID = %q", got)
	}
}

func TestErrorEnvelopeShape(t *testing.T) {
	ts := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, 422, "KYC_FAILED", errString("nin mismatch"))
	}))
	resp, err := http.Get(ts.URL + "/fail")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var env map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 422 || env["error"] != "nin mismatch" || env["code"] != "KYC_FAILED" {
		t.Fatalf("envelope = %v status=%d", env, resp.StatusCode)
	}
}

func TestAuthMiddleware(t *testing.T) {
	svc := authn.NewService("test-signing-secret")
	if _, err := svc.CreateAccount("t1", "ops@example.com", "pw", authn.RoleOps); err != nil {
		t.Fatal(err)
	}
	token, err := svc.Login("ops@example.com", "pw", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	inner := Auth(svc, authn.RoleOps)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	ts := newTestServer(t, inner)

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"no header", "", 401},
		{"bad scheme", "Basic abc", 401},
		{"garbage token", "Bearer garbage", 401},
		{"valid token", "Bearer " + token, 204},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", ts.URL+"/secure", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestGracefulShutdownDrainsInflight(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		<-release // simulate a slow in-flight request
		w.WriteHeader(200)
	})
	s := New("shutdown-test", "127.0.0.1:0", handler, WithShutdownTimeout(5*time.Second))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve(ln) }()

	respCh := make(chan int, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/slow")
		if err != nil {
			respCh <- -1
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		respCh <- resp.StatusCode
	}()
	<-started

	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownDone <- s.Shutdown(ctx)
	}()
	time.Sleep(50 * time.Millisecond)
	select {
	case <-shutdownDone:
		t.Fatal("Shutdown returned while a request was still in flight")
	default:
	}
	close(release)
	if code := <-respCh; code != 200 {
		t.Fatalf("in-flight request status = %d, want 200 (drained, not cut)", code)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if _, err := http.Get("http://" + ln.Addr().String() + "/healthz"); err == nil {
		t.Fatal("server still accepting connections after shutdown")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
