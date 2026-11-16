package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStripPII(t *testing.T) {
	in := "RSA PEN2000000123 balance 1,250,000.00 NGN was credited"
	out := StripPII(in)
	if out == in {
		t.Fatal("PII not stripped")
	}
	if !contains(out, "[PIN]") || !contains(out, "[AMOUNT]") {
		t.Fatalf("got %q", out)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

func TestRephraseOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req genReq
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model == "" || req.Prompt == "" {
			t.Fatal("bad request")
		}
		json.NewEncoder(w).Encode(genResp{Response: "Your balance is ready, sir."})
	}))
	defer srv.Close()
	out, ok := New(srv.URL, "llama3.2:3b").Rephrase(context.Background(),
		"Your RSA balance is PEN2000000123 worth 50000 NGN", "English")
	if !ok || out == "" {
		t.Fatal("expected rephrase")
	}
}

func TestRephraseFailsClosed(t *testing.T) {
	// server down
	_, ok := New("http://127.0.0.1:1", "").Rephrase(context.Background(), "hi", "English")
	if ok {
		t.Fatal("must fail closed when server is down")
	}
	// server errors
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	if _, ok := New(srv.URL, "").Rephrase(context.Background(), "hi", "English"); ok {
		t.Fatal("must fail closed on 500")
	}
}

func TestRephraseTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		json.NewEncoder(w).Encode(genResp{Response: "x"})
	}))
	defer srv.Close()
	c := New(srv.URL, "")
	c.hc.Timeout = 50 * time.Millisecond
	if _, ok := c.Rephrase(context.Background(), "hi", "English"); ok {
		t.Fatal("must fail closed on timeout")
	}
}
