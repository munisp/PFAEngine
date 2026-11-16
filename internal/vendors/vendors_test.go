package vendors

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/munisp/pfaengine/internal/notify"
)

func TestSmileIDNINVerify(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/verify" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(smileIDResponse{ResultCode: "1012", JobID: "job-1", ResultText: "Exact match"})
	}))
	defer srv.Close()
	s := &SmileID{PartnerID: "p1", APIKey: "key", BaseURL: srv.URL, HTTP: srv.Client()}
	res, err := s.VerifyNIN("12345678901", "Amara", "1992-01-01")
	if err != nil || !res.Matched {
		t.Fatalf("verify failed: %v %+v", err, res)
	}
	if res.Token == "12345678901" || res.Token == "" {
		t.Fatal("NIN must be tokenized")
	}
}

func TestDojahBVN(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "sk" {
			t.Fatal("missing auth header")
		}
		json.NewEncoder(w).Encode(map[string]any{"entity": map[string]string{"first_name": "Amara"}})
	}))
	defer srv.Close()
	d := &Dojah{AppID: "app", SecretKey: "sk", BaseURL: srv.URL, HTTP: srv.Client()}
	res, err := d.ValidateBVN("98765432109", "Amara", "1992-01-01", "0803")
	if err != nil || !res.Matched {
		t.Fatalf("bvn failed: %v", err)
	}
}

func TestTermiiSMS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(termiiSendResp{MessageID: "msg-1", Code: "ok"})
	}))
	defer srv.Close()
	tm := &Termii{APIKey: "k", SenderID: "PFA", BaseURL: srv.URL, HTTP: srv.Client()}
	id, err := tm.Send(notify.SMS, "2348030000000", "Your RSA received NGN 144,000")
	if err != nil || id != "msg-1" {
		t.Fatalf("send failed: %v %s", err, id)
	}
	if _, err := tm.Send(notify.Email, "a@b.c", "x"); err == nil {
		t.Fatal("email must be rejected on SMS adapter")
	}
}

func TestRemitaRRR(t *testing.T) {
	r := &Remita{MerchantID: "m1", APIKey: "k1"}
	ins := r.GenerateRRR("emp-1", "sched-1", 14400000)
	if ins.RRR == "" || ins.AmountKobo != 14400000 {
		t.Fatalf("bad instruction: %+v", ins)
	}
}

func TestUSSDFlow(t *testing.T) {
	at := &AfricasTalking{}
	h := Handler{
		Balance:     func(pin string) (string, error) { return "NGN 6,932,000.00", nil },
		LastContrib: func(pin string) (string, error) { return "NGN 144,000 (2026-11)", nil },
	}
	r1 := at.Handle(h, "s1", "0803", "")
	if r1.Text[:3] != "CON" {
		t.Fatal("expect CON prompt")
	}
	r2 := at.Handle(h, "s1", "0803", "PEN100*1")
	if r2.Text[:3] != "END" || r2.Text == "" {
		t.Fatalf("bad balance reply: %s", r2.Text)
	}
}
