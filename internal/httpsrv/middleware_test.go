package httpsrv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/munisp/pfaengine/internal/authn"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}

func TestRateLimitAllowsBurstThenRejects(t *testing.T) {
	h := RateLimit(1, 3)(okHandler()) // 1 rps, burst 3
	for i := 0; i < 3; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
		req.RemoteAddr = "10.0.0.1:5555"
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d within burst: status = %d, want 200", i+1, rr.Code)
		}
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	req.RemoteAddr = "10.0.0.1:5555"
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("over-burst status = %d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Fatal("429 response missing Retry-After header")
	}
	var env ErrorBody
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Code != "RATE_LIMITED" {
		t.Fatalf("envelope code = %q, want RATE_LIMITED", env.Code)
	}
}

func TestRateLimitRefillsOverTime(t *testing.T) {
	h := RateLimit(50, 1)(okHandler()) // 50 rps => a token every 20ms
	hit := func() int {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
		req.RemoteAddr = "10.0.0.2:5555"
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	if hit() != http.StatusOK {
		t.Fatal("first request should pass")
	}
	if hit() != http.StatusTooManyRequests {
		t.Fatal("second request should be limited")
	}
	time.Sleep(30 * time.Millisecond)
	if hit() != http.StatusOK {
		t.Fatal("bucket should have refilled")
	}
}

func TestRateLimitIsPerClientIP(t *testing.T) {
	h := RateLimit(1, 1)(okHandler())
	req := func(ip string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
		r.RemoteAddr = ip + ":5555"
		return r
	}
	rr1, rr2 := httptest.NewRecorder(), httptest.NewRecorder()
	h.ServeHTTP(rr1, req("10.1.1.1"))
	h.ServeHTTP(rr2, req("10.2.2.2"))
	if rr1.Code != http.StatusOK || rr2.Code != http.StatusOK {
		t.Fatalf("distinct IPs must have independent buckets: %d, %d", rr1.Code, rr2.Code)
	}
}

func TestMaxBodyRejectsOversizedContentLength(t *testing.T) {
	h := MaxBody(16)(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/x", strings.NewReader(strings.Repeat("a", 64)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rr.Code)
	}
	var env ErrorBody
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Code != "BODY_TOO_LARGE" {
		t.Fatalf("envelope code = %q, want BODY_TOO_LARGE", env.Code)
	}
}

func TestMaxBodyPassesWithinLimit(t *testing.T) {
	h := MaxBody(64)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]string
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/x", strings.NewReader(`{"a":"b"}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	cases := []struct {
		path        string
		wantNoStore bool
	}{
		{"/api/v1/members", true},
		{"/healthz", false},
	}
	for _, tc := range cases {
		rr := httptest.NewRecorder()
		SecurityHeaders(okHandler()).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", tc.path, got)
		}
		if got := rr.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("%s: X-Frame-Options = %q", tc.path, got)
		}
		cc := rr.Header().Get("Cache-Control")
		if tc.wantNoStore && cc != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", tc.path, cc)
		}
		if !tc.wantNoStore && cc != "" {
			t.Errorf("%s: Cache-Control = %q, want empty off /api", tc.path, cc)
		}
	}
}

func TestOptionalAuth(t *testing.T) {
	svc := authn.NewService("test-signing-secret-0123456789")
	h := OptionalAuth(svc)(okHandler())

	// No header: passes through (dev mode).
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/x", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("no-token status = %d, want 200", rr.Code)
	}

	// Garbage bearer token: 401 with envelope.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("bad-token status = %d, want 401", rr.Code)
	}
	var env ErrorBody
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Code != "AUTH_INVALID" {
		t.Fatalf("envelope code = %q, want AUTH_INVALID", env.Code)
	}

	// Malformed scheme: 401.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("basic-auth status = %d, want 401", rr.Code)
	}
}
