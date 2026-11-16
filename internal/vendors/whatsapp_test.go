package vendors

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func waSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestWhatsAppVerifyWebhookSignature(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account","entry":[]}`)
	good := waSignature("app-secret-1", body)
	cases := []struct {
		name      string
		secret    string
		body      []byte
		signature string
		want      bool
	}{
		{"valid HMAC-SHA256", "app-secret-1", body, good, true},
		{"tampered body", "app-secret-1", []byte(`{"object":"evil"}`), good, false},
		{"wrong secret signature", "app-secret-1", body, waSignature("other-secret", body), false},
		{"wrong algorithm prefix", "app-secret-1", body, "sha1=" + good[len("sha256="):], false},
		{"missing header", "app-secret-1", body, "", false},
		{"empty app secret fails closed", "", body, good, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cl := &Client{AppSecret: tc.secret}
			if got := cl.VerifyWebhookSignature(tc.body, tc.signature); got != tc.want {
				t.Errorf("VerifyWebhookSignature = %v, want %v", got, tc.want)
			}
		})
	}
	// The expected value must be the real HMAC, not a hard-coded constant:
	if want := "sha256=" + hex.EncodeToString(hmacSHA256("app-secret-1", body)); good != want {
		t.Fatalf("test vector broken: %q != %q", good, want)
	}
}

func hmacSHA256(secret string, body []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return mac.Sum(nil)
}

func TestWhatsAppVerifyChallenge(t *testing.T) {
	q := func(mode, token, challenge string) url.Values {
		v := url.Values{}
		v.Set("hub.mode", mode)
		v.Set("hub.verify_token", token)
		v.Set("hub.challenge", challenge)
		return v
	}
	cases := []struct {
		name      string
		query     url.Values
		verifyTok string
		wantCh    string
		wantOK    bool
	}{
		{"valid handshake", q("subscribe", "tok-1", "1158201444"), "tok-1", "1158201444", true},
		{"wrong verify token", q("subscribe", "tok-2", "1158201444"), "tok-1", "", false},
		{"wrong mode", q("unsubscribe", "tok-1", "1158201444"), "tok-1", "", false},
		{"missing challenge", q("subscribe", "tok-1", ""), "tok-1", "", false},
		{"empty configured token fails closed", q("subscribe", "", "1158201444"), "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{}
			ch, ok := c.VerifyChallenge(tc.query, tc.verifyTok)
			if ok != tc.wantOK || ch != tc.wantCh {
				t.Errorf("VerifyChallenge = (%q, %v), want (%q, %v)", ch, ok, tc.wantCh, tc.wantOK)
			}
		})
	}
}

// waMessagesServer recomputes the adapter's credential and request shape
// server-side and reports correctness via X-Signature-Valid.
func waMessagesServer(t *testing.T, token, phoneNumberID string, want waSendReq) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+phoneNumberID+"/messages" {
			http.Error(w, "bad path "+r.URL.Path, http.StatusNotFound)
			return
		}
		var got waSendReq
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ok := r.Header.Get("Authorization") == "Bearer "+token &&
			r.Header.Get("Content-Type") == "application/json" &&
			got.MessagingProduct == want.MessagingProduct &&
			got.To == want.To && got.Type == want.Type
		switch want.Type {
		case "template":
			ok = ok && got.Template != nil && got.Template.Name == want.Template.Name &&
				got.Template.Language.Code == want.Template.Language.Code &&
				len(got.Template.Components) == 1 && got.Template.Components[0].Type == "body" &&
				len(got.Template.Components[0].Parameters) == len(want.Template.Components[0].Parameters)
			if ok {
				for i, p := range want.Template.Components[0].Parameters {
					ok = ok && got.Template.Components[0].Parameters[i].Text == p.Text
				}
			}
		case "text":
			ok = ok && got.Text != nil && got.Text.Body == want.Text.Body
		}
		signedHeader(w, ok)
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": []map[string]string{{"id": "wamid.conf1"}}})
	}))
}

func TestWhatsAppSendConformance(t *testing.T) {
	const token, phoneID = "wa-token-1", "102290129340398"
	wantTemplate := waSendReq{
		MessagingProduct: "whatsapp", To: "2348030000000", Type: "template",
		Template: &waTemplate{
			Name: "liveness_reminder", Language: waLanguage{Code: "en"},
			Components: []waComponent{{Type: "body", Parameters: []waParameter{
				{Type: "text", Text: "Ada"}, {Type: "text", Text: "2025-06-30"},
			}}},
		},
	}
	wantText := waSendReq{
		MessagingProduct: "whatsapp", To: "2348030000000", Type: "text",
		Text: &waText{Body: "Your RSA balance dey your latest statement."},
	}
	srv := waMessagesServer(t, token, phoneID, wantTemplate)
	defer srv.Close()

	RunConformance(t, "whatsapp", []ConformanceCase{
		{
			Name: "SendTemplate Bearer token + template payload",
			Request: func(t *testing.T) (*http.Response, []byte, error) {
				hc, capT := captureClient(srv)
				c := &Client{PhoneNumberID: phoneID, AccessToken: token, BaseURL: srv.URL, HTTP: hc}
				id, err := c.SendTemplate(context.Background(), "2348030000000", "liveness_reminder", "en",
					[]string{"Ada", "2025-06-30"})
				if err == nil && id != "wamid.conf1" {
					t.Fatalf("message id = %q", id)
				}
				return capT.resp, capT.body, err
			},
			WantStatus:  http.StatusOK,
			MustContain: []string{"wamid.conf1"},
			MustSign:    true,
		},
		{
			Name: "SendText Bearer token + text payload",
			Request: func(t *testing.T) (*http.Response, []byte, error) {
				ts := waMessagesServer(t, token, phoneID, wantText)
				defer ts.Close()
				hc, capT := captureClient(ts)
				c := &Client{PhoneNumberID: phoneID, AccessToken: token, BaseURL: ts.URL, HTTP: hc}
				_, err := c.SendText(context.Background(), "2348030000000", "Your RSA balance dey your latest statement.")
				return capT.resp, capT.body, err
			},
			WantStatus:  http.StatusOK,
			MustContain: []string{"wamid.conf1"},
			MustSign:    true,
		},
	})
}

func TestWhatsAppParseInbound(t *testing.T) {
	// Real-shaped Cloud API payload: one text message plus a status update
	// (which must be ignored).
	payload := `{
	  "object": "whatsapp_business_account",
	  "entry": [{
	    "id": "102290129340398",
	    "changes": [{
	      "field": "messages",
	      "value": {
	        "messaging_product": "whatsapp",
	        "metadata": {"display_phone_number": "15550001111", "phone_number_id": "102290129340398"},
	        "contacts": [{"profile": {"name": "Ada"}, "wa_id": "2348030000000"}],
	        "messages": [{
	          "from": "2348030000000",
	          "id": "wamid.HBgLMjM0ODAzMDAwMDAwMRUCABIYFjNFQjAxMDI4QkM3RjA2MTlGMzAA",
	          "timestamp": "1735689600",
	          "type": "text",
	          "text": {"body": "how much dey my account"}
	        }]
	      }
	    }, {
	      "field": "messages",
	      "value": {
	        "messaging_product": "whatsapp",
	        "metadata": {"display_phone_number": "15550001111", "phone_number_id": "102290129340398"},
	        "statuses": [{
	          "id": "wamid.HBgLMjM0ODAzMDAwMDAwMRUCABIYFjNFQjAxMDI4QkM3RjA2MTlGMzAA",
	          "status": "delivered",
	          "timestamp": "1735689601",
	          "recipient_id": "2348030000000"
	        }]
	      }
	    }]
	  }]
	}`
	c := &Client{}
	msgs, err := c.ParseInbound([]byte(payload))
	if err != nil {
		t.Fatalf("ParseInbound: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1 (status update must be ignored)", len(msgs))
	}
	m := msgs[0]
	if m.From != "2348030000000" || m.Text != "how much dey my account" || m.Type != "text" {
		t.Errorf("message = %+v", m)
	}
	if want := time.Unix(1735689600, 0).UTC(); !m.Timestamp.Equal(want) {
		t.Errorf("timestamp = %v, want %v", m.Timestamp, want)
	}

	if _, err := c.ParseInbound([]byte(`{not json`)); err == nil {
		t.Error("malformed payload must error")
	}
}
