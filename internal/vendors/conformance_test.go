package vendors

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/notify"
)

// ConformanceCase is one wire-protocol contract check against a vendor
// adapter (INT-02..08). Request must drive the real adapter against an
// httptest server and surface the raw response; the test server recomputes
// the vendor credential (signature/hash/key) independently and reports
// correctness via the X-Signature-Valid response header.
type ConformanceCase struct {
	Name        string
	Request     func(t *testing.T) (resp *http.Response, body []byte, err error)
	WantStatus  int
	MustContain []string
	MustSign    bool // require server-side credential recompute to pass
}

// RunConformance executes a vendor contract suite: HTTP status, response
// payload markers, and — for MustSign cases — proof that the adapter's
// auth/signature scheme recomputes correctly on the server side (presence
// of a header alone is never accepted).
func RunConformance(t *testing.T, vendor string, cases []ConformanceCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(vendor+"/"+c.Name, func(t *testing.T) {
			resp, body, err := c.Request(t)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			if resp == nil {
				t.Fatal("no response captured — adapter did not hit the test server")
			}
			if resp.StatusCode != c.WantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", resp.StatusCode, c.WantStatus, body)
			}
			for _, m := range c.MustContain {
				if !bytes.Contains(body, []byte(m)) {
					t.Errorf("response body missing %q (got: %s)", m, body)
				}
			}
			if c.MustSign && resp.Header.Get("X-Signature-Valid") != "true" {
				t.Errorf("server-side credential recompute failed (X-Signature-Valid=%q)",
					resp.Header.Get("X-Signature-Valid"))
			}
		})
	}
}

// captureTransport records the last response (status, headers, body)
// while handing the adapter an unconsumed copy of the body.
type captureTransport struct {
	base http.RoundTripper
	resp *http.Response
	body []byte
}

func (c *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := c.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	if resp.Body != nil {
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(b))
		c.resp, c.body = resp, b
	}
	return resp, nil
}

func captureClient(srv *httptest.Server) (*http.Client, *captureTransport) {
	capT := &captureTransport{base: srv.Client().Transport}
	if capT.base == nil {
		capT.base = http.DefaultTransport
	}
	return &http.Client{Transport: capT}, capT
}

func signedHeader(w http.ResponseWriter, ok bool) {
	w.Header().Set("X-Signature-Valid", strconv.FormatBool(ok))
	w.Header().Set("Content-Type", "application/json")
}

// --- Smile ID: HMAC-SHA256(timestamp + partner_id + "sid_request") ---

func TestSmileIDConformance(t *testing.T) {
	const partnerID, apiKey = "partner-9", "smile-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/verify" {
			http.Error(w, "bad path "+r.URL.Path, http.StatusNotFound)
			return
		}
		var req smileIDJobRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mac := hmac.New(sha256.New, []byte(apiKey))
		mac.Write([]byte(req.Timestamp + partnerID + "sid_request"))
		want := base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(mac.Sum(nil))))
		signedHeader(w, req.PartnerID == partnerID && req.Timestamp != "" && hmac.Equal([]byte(req.Signature), []byte(want)))
		_ = json.NewEncoder(w).Encode(smileIDResponse{ResultCode: "1012", JobID: "job-conf-1", ResultText: "Exact match"})
	}))
	defer srv.Close()

	call := func(t *testing.T, verify func(s *SmileID) error) (resp *http.Response, body []byte, err error) {
		hc, capT := captureClient(srv)
		s := &SmileID{PartnerID: partnerID, APIKey: apiKey, BaseURL: srv.URL, HTTP: hc}
		err = verify(s)
		return capT.resp, capT.body, err
	}
	RunConformance(t, "smileid", []ConformanceCase{
		{
			Name: "VerifyNIN HMAC-SHA256 signature",
			Request: func(t *testing.T) (*http.Response, []byte, error) {
				return call(t, func(s *SmileID) error { _, err := s.VerifyNIN("12345678901", "Amara", "1992-01-01"); return err })
			},
			WantStatus:  http.StatusOK,
			MustContain: []string{"1012", "job-conf-1"},
			MustSign:    true,
		},
		{
			Name: "ValidateBVN HMAC-SHA256 signature",
			Request: func(t *testing.T) (*http.Response, []byte, error) {
				return call(t, func(s *SmileID) error {
					_, err := s.ValidateBVN("98765432109", "Amara", "1992-01-01", "0803")
					return err
				})
			},
			WantStatus:  http.StatusOK,
			MustContain: []string{"1012"},
			MustSign:    true,
		},
	})
}

// --- Dojah: AppId + Authorization headers ---

func TestDojahConformance(t *testing.T) {
	const appID, secret = "app-1", "sk-live-1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok := r.Header.Get("AppId") == appID && r.Header.Get("Authorization") == secret
		signedHeader(w, ok)
		_ = json.NewEncoder(w).Encode(map[string]any{"entity": map[string]string{"first_name": "Amara"}})
	}))
	defer srv.Close()

	call := func(t *testing.T, verify func(d *Dojah) error) (resp *http.Response, body []byte, err error) {
		hc, capT := captureClient(srv)
		d := &Dojah{AppID: appID, SecretKey: secret, BaseURL: srv.URL, HTTP: hc}
		err = verify(d)
		return capT.resp, capT.body, err
	}
	RunConformance(t, "dojah", []ConformanceCase{
		{
			Name: "VerifyNIN Authorization header",
			Request: func(t *testing.T) (*http.Response, []byte, error) {
				return call(t, func(d *Dojah) error { _, err := d.VerifyNIN("12345678901", "Amara", "1992-01-01"); return err })
			},
			WantStatus:  http.StatusOK,
			MustContain: []string{"first_name"},
			MustSign:    true,
		},
		{
			Name: "ValidateBVN Authorization header",
			Request: func(t *testing.T) (*http.Response, []byte, error) {
				return call(t, func(d *Dojah) error {
					_, err := d.ValidateBVN("98765432109", "Amara", "1992-01-01", "0803")
					return err
				})
			},
			WantStatus:  http.StatusOK,
			MustContain: []string{"first_name"},
			MustSign:    true,
		},
	})
}

// --- Termii: api_key in the JSON payload ---

func TestTermiiConformance(t *testing.T) {
	const apiKey, senderID = "termii-key-1", "PFAEngine"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req termiiSendReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ok := req.APIKey == apiKey && req.From == senderID && req.To != "" && req.SMS != "" &&
			r.Header.Get("Content-Type") == "application/json"
		signedHeader(w, ok)
		_ = json.NewEncoder(w).Encode(termiiSendResp{MessageID: "termii-msg-1", Code: "ok"})
	}))
	defer srv.Close()

	call := func(t *testing.T, to, body string) (resp *http.Response, raw []byte, err error) {
		hc, capT := captureClient(srv)
		tm := &Termii{APIKey: apiKey, SenderID: senderID, BaseURL: srv.URL, HTTP: hc}
		_, err = tm.Send(notify.SMS, to, body)
		return capT.resp, capT.body, err
	}
	RunConformance(t, "termii", []ConformanceCase{
		{
			Name: "SMS send api_key credential",
			Request: func(t *testing.T) (*http.Response, []byte, error) {
				return call(t, "2348030000000", "Your RSA received NGN 144,000")
			},
			WantStatus:  http.StatusOK,
			MustContain: []string{"termii-msg-1"},
			MustSign:    true,
		},
		{
			Name:        "SMS send api_key credential (second recipient)",
			Request:     func(t *testing.T) (*http.Response, []byte, error) { return call(t, "2347011111111", "Statement ready") },
			WantStatus:  http.StatusOK,
			MustContain: []string{"termii-msg-1"},
			MustSign:    true,
		},
	})
}

// --- Remita: SHA-512 hash key ordering ---

func TestRemitaConformance(t *testing.T) {
	const merchantID, apiKey, svcType = "merch-1", "remita-secret", "svc-001"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Path: /ecomm/{merchantId}/{orderId}/{hash}/status.reg
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/ecomm/"), "/")
		if len(parts) != 4 || parts[3] != "status.reg" {
			http.Error(w, "bad path "+r.URL.Path, http.StatusNotFound)
			return
		}
		mid, orderID, gotHash := parts[0], parts[1], parts[2]
		amount := int64(14400000 / 100) // naira, per hashKey convention
		want := sha512.Sum512([]byte(fmt.Sprintf("%s%s%s%d%s", merchantID, svcType, orderID, amount, apiKey)))
		wantHash := hex.EncodeToString(want[:])
		authOK := strings.Contains(r.Header.Get("Authorization"), "remitaConsumerKey="+merchantID) &&
			strings.Contains(r.Header.Get("Authorization"), "remitaConsumerToken="+wantHash)
		signedHeader(w, mid == merchantID && gotHash == wantHash && authOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "00", "message": "Approved"})
	}))
	defer srv.Close()

	call := func(t *testing.T, orderID string, amount money.Kobo) (resp *http.Response, body []byte, err error) {
		hc, capT := captureClient(srv)
		r := &Remita{MerchantID: merchantID, APIKey: apiKey, BaseURL: srv.URL, HTTP: hc}
		_, err = r.CheckStatus(context.Background(), svcType, orderID, amount)
		return capT.resp, capT.body, err
	}
	RunConformance(t, "remita", []ConformanceCase{
		{
			Name:        "status check SHA-512 hash ordering",
			Request:     func(t *testing.T) (*http.Response, []byte, error) { return call(t, "PFA-emp-1-sched-1", 14400000) },
			WantStatus:  http.StatusOK,
			MustContain: []string{"Approved"},
			MustSign:    true,
		},
		{
			Name:        "status check SHA-512 hash ordering (second order)",
			Request:     func(t *testing.T) (*http.Response, []byte, error) { return call(t, "PFA-emp-2-sched-9", 14400000) },
			WantStatus:  http.StatusOK,
			MustContain: []string{"Approved"},
			MustSign:    true,
		},
	})
}

// --- Africa's Talking: apiKey header + form-encoded body ---

func TestAfricasTalkingConformance(t *testing.T) {
	const apiKey, username = "at-key-1", "pfaengine"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version1/messaging" {
			http.Error(w, "bad path "+r.URL.Path, http.StatusNotFound)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ok := r.Header.Get("apiKey") == apiKey &&
			strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") &&
			r.PostForm.Get("username") == username &&
			r.PostForm.Get("to") != "" && r.PostForm.Get("message") != ""
		signedHeader(w, ok)
		fmt.Fprint(w, `{"SMSMessageData":{"Message":"Sent to 1/1 Total Cost: NGN 2.2","Recipients":[{"messageId":"ATXid_conf1","status":"Success","number":"2348030000000"}]}}`)
	}))
	defer srv.Close()

	call := func(t *testing.T, to, msg string) (resp *http.Response, body []byte, err error) {
		hc, capT := captureClient(srv)
		at := &AfricasTalking{APIKey: apiKey, Username: username, BaseURL: srv.URL, HTTP: hc}
		_, err = at.SendSMS(context.Background(), to, msg)
		return capT.resp, capT.body, err
	}
	RunConformance(t, "africastalking", []ConformanceCase{
		{
			Name: "SMS apiKey header + form encoding",
			Request: func(t *testing.T) (*http.Response, []byte, error) {
				return call(t, "2348030000000", "Balance: NGN 6,932,000.00")
			},
			WantStatus:  http.StatusOK,
			MustContain: []string{"ATXid_conf1", "SMSMessageData"},
			MustSign:    true,
		},
		{
			Name: "SMS apiKey header + form encoding (second message)",
			Request: func(t *testing.T) (*http.Response, []byte, error) {
				return call(t, "2347011111111", "Statement link: https://pfa.example/s/1")
			},
			WantStatus:  http.StatusOK,
			MustContain: []string{"ATXid_conf1"},
			MustSign:    true,
		},
	})
}
