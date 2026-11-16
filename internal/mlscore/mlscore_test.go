package mlscore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestScoreOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/score" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(ScoreResponse{
			Score: 0.97, Threshold: 0.45, Flagged: true,
			Model: "fraudfusion:v1", Arm: "champion",
		})
	}))
	defer srv.Close()
	c := New(srv.URL)
	out, ok := c.Score(context.Background(), "mem-1", Txn{
		TxnID: "t1", MemberID: "mem-1", Kind: "withdrawal",
		AmountKobo: 900000000, Channel: "ussd", State: "KN",
		Day: 500, MinuteOfDay: 30, DeviceID: "dev-x",
	}, nil)
	if !ok || !out.Flagged || out.Score != 0.97 {
		t.Fatalf("got %+v ok=%v", out, ok)
	}
}

func TestScoreDegradedFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"degraded": true, "score": nil, "reason": "no champion model registered",
		})
	}))
	defer srv.Close()
	_, ok := New(srv.URL).Score(context.Background(), "m", Txn{}, nil)
	if ok {
		t.Fatal("degraded service must fail closed")
	}
}

func TestScoreDownFailsClosed(t *testing.T) {
	c := New("http://127.0.0.1:1") // nothing listening
	_, ok := c.Score(context.Background(), "m", Txn{}, nil)
	if ok {
		t.Fatal("unreachable service must fail closed")
	}
}

func TestScoreTimeoutFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, ok := New(srv.URL).Score(ctx, "m", Txn{}, nil)
	if ok {
		t.Fatal("slow service must fail closed via client timeout")
	}
}

func TestHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"status": "ok", "champion": "fraudfusion:v3", "degraded": false,
		})
	}))
	defer srv.Close()
	v, err := New(srv.URL).Health(context.Background())
	if err != nil || v != "fraudfusion:v3" {
		t.Fatalf("got %q err=%v", v, err)
	}
}
