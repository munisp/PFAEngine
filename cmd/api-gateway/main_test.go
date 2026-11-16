package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestProxyForwardsPathAndHeaders(t *testing.T) {
	var gotPath, gotTenant, gotReqID string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotTenant = r.Header.Get("X-Tenant-ID")
		gotReqID = r.Header.Get("X-Request-ID")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	u, _ := url.Parse(upstream.URL)
	p := proxyFor(backend{name: "member-svc", prefix: "/api/v1/members", base: u})

	req := httptest.NewRequest("GET", "/api/v1/members/prs-1/balance", nil)
	req.Header.Set("X-Tenant-ID", "t1")
	req.Header.Set("X-Request-ID", "req-test-1")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("proxy status = %d", rr.Code)
	}
	if gotPath != "/api/v1/members/prs-1/balance" {
		t.Fatalf("upstream path = %q", gotPath)
	}
	if gotTenant != "t1" || gotReqID != "req-test-1" {
		t.Fatalf("headers lost: tenant=%q reqid=%q", gotTenant, gotReqID)
	}
}

func TestProxyUpstreamDownGivesEnvelope502(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:1") // nothing listening
	p := proxyFor(backend{name: "down", prefix: "/api/v1/x", base: u})
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, httptest.NewRequest("GET", "/api/v1/x", nil))
	if rr.Code != 502 {
		t.Fatalf("status = %d, want 502", rr.Code)
	}
	var env struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Code != "UPSTREAM_UNAVAILABLE" || env.Error == "" {
		t.Fatalf("envelope = %+v", env)
	}
}

func TestAllReadyAggregatesBackends(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer up.Close()
	u, _ := url.Parse(up.URL)
	down, _ := url.Parse("http://127.0.0.1:1")

	bs := []backend{{name: "a", prefix: "/a", base: u}}
	if !allReady(bs)() {
		t.Fatal("allReady = false with healthy backend")
	}
	bs = append(bs, backend{name: "b", prefix: "/b", base: down})
	if allReady(bs)() {
		t.Fatal("allReady = true with one unreachable backend")
	}
}
