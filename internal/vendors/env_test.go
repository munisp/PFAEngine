package vendors

import (
	"strings"
	"testing"
)

// clearVendorEnv blanks every credential variable so the ambient developer /
// CI environment cannot leak into a case.
func clearVendorEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"SMILE_ID_PARTNER_ID", "SMILE_ID_API_KEY", "SMILE_ID_BASE_URL",
		"DOJAH_APP_ID", "DOJAH_SECRET_KEY", "DOJAH_BASE_URL",
		"TERMII_API_KEY", "TERMII_BASE_URL", "TERMII_SENDER_ID",
		"REMITA_MERCHANT_ID", "REMITA_API_KEY", "REMITA_BASE_URL",
		"AT_API_KEY", "AT_USERNAME", "AT_BASE_URL", "AT_SHORT_CODE",
		"WHATSAPP_ACCESS_TOKEN", "WHATSAPP_PHONE_NUMBER_ID",
		"WHATSAPP_APP_SECRET", "WHATSAPP_BASE_URL",
	} {
		t.Setenv(k, "")
	}
}

func TestFromEnv(t *testing.T) {
	cases := []struct {
		name           string
		env            map[string]string
		wantProviders  []string // kyc.Provider.Name(), in order
		wantSender     bool
		wantWarnings   int
		wantSummaryHas []string
		check          func(t *testing.T, e Env)
	}{
		{
			name:          "none set: no providers, no sender, no warnings",
			wantProviders: nil, wantSender: false, wantWarnings: 0,
		},
		{
			name:          "smile id pair: one provider",
			env:           map[string]string{"SMILE_ID_PARTNER_ID": "p-1", "SMILE_ID_API_KEY": "k-1"},
			wantProviders: []string{"smileid"},
			check: func(t *testing.T, e Env) {
				if e.SmileID.BaseURL != DefaultSmileIDBaseURL {
					t.Errorf("smileid base url = %q, want default %q", e.SmileID.BaseURL, DefaultSmileIDBaseURL)
				}
				if !strings.Contains(e.Summary()[0], "smileid") {
					t.Errorf("summary must name smileid, got %v", e.Summary())
				}
			},
		},
		{
			name: "smile id + dojah: two providers, smileid first",
			env: map[string]string{
				"SMILE_ID_PARTNER_ID": "p-1", "SMILE_ID_API_KEY": "k-1",
				"DOJAH_APP_ID": "app-1", "DOJAH_SECRET_KEY": "sk-1",
				"SMILE_ID_BASE_URL": "https://sandbox.smileidentity.com/v1",
			},
			wantProviders:  []string{"smileid", "dojah"},
			wantSummaryHas: []string{"smileid", "dojah"},
			check: func(t *testing.T, e Env) {
				if e.SmileID.BaseURL != "https://sandbox.smileidentity.com/v1" {
					t.Errorf("smileid base url override not honoured: %q", e.SmileID.BaseURL)
				}
				if e.Dojah.BaseURL != DefaultDojahBaseURL {
					t.Errorf("dojah base url = %q, want default %q", e.Dojah.BaseURL, DefaultDojahBaseURL)
				}
			},
		},
		{
			name:          "partial dojah (app id only): warning, not configured",
			env:           map[string]string{"DOJAH_APP_ID": "app-1"},
			wantProviders: nil, wantWarnings: 1,
			check: func(t *testing.T, e Env) {
				if e.Dojah != nil {
					t.Error("partially-configured dojah must not be live")
				}
				w := e.Warnings[0]
				if !strings.Contains(w, "dojah") || !strings.Contains(w, "DOJAH_SECRET_KEY") {
					t.Errorf("warning must name the missing var, got %q", w)
				}
				if strings.Contains(w, "app-1") {
					t.Errorf("warning must not leak the credential value: %q", w)
				}
			},
		},
		{
			name:          "partial smile id (api key only): warning, not configured",
			env:           map[string]string{"SMILE_ID_API_KEY": "k-1"},
			wantProviders: nil, wantWarnings: 1,
			check: func(t *testing.T, e Env) {
				if e.SmileID != nil {
					t.Error("partially-configured smileid must not be live")
				}
			},
		},
		{
			name:         "termii key: sender ok with defaults",
			env:          map[string]string{"TERMII_API_KEY": "tk-1"},
			wantSender:   true,
			wantWarnings: 0,
			check: func(t *testing.T, e Env) {
				if e.Termii.BaseURL != DefaultTermiiBaseURL {
					t.Errorf("termii base url = %q, want %q", e.Termii.BaseURL, DefaultTermiiBaseURL)
				}
				if e.Termii.SenderID != "PFAEngine" {
					t.Errorf("termii sender id = %q, want platform default", e.Termii.SenderID)
				}
			},
		},
		{
			name:         "termii overrides honoured",
			env:          map[string]string{"TERMII_API_KEY": "tk-1", "TERMII_SENDER_ID": "AcmePFA", "TERMII_BASE_URL": "https://sandbox.termii.test"},
			wantSender:   true,
			wantWarnings: 0,
			check: func(t *testing.T, e Env) {
				if e.Termii.SenderID != "AcmePFA" || e.Termii.BaseURL != "https://sandbox.termii.test" {
					t.Errorf("termii overrides not honoured: %+v", e.Termii)
				}
			},
		},
		{
			name:          "remita pair configured",
			env:           map[string]string{"REMITA_MERCHANT_ID": "m-1", "REMITA_API_KEY": "rk-1"},
			wantProviders: nil, wantWarnings: 0,
			wantSummaryHas: []string{"remita"},
			check: func(t *testing.T, e Env) {
				if e.Remita == nil || e.Remita.BaseURL != DefaultRemitaBaseURL {
					t.Errorf("remita not configured as expected: %+v", e.Remita)
				}
			},
		},
		{
			name:          "partial africastalking: warning, not configured",
			env:           map[string]string{"AT_USERNAME": "pfaengine"},
			wantProviders: nil, wantWarnings: 1,
			check: func(t *testing.T, e Env) {
				if e.AfricasTalking != nil {
					t.Error("partially-configured africastalking must not be live")
				}
			},
		},
		{
			name:          "whatsapp pair configured",
			env:           map[string]string{"WHATSAPP_ACCESS_TOKEN": "wt-1", "WHATSAPP_PHONE_NUMBER_ID": "pn-1"},
			wantProviders: nil, wantWarnings: 0,
			wantSummaryHas: []string{"whatsapp"},
			check: func(t *testing.T, e Env) {
				if e.WhatsApp == nil || e.WhatsApp.BaseURL != DefaultWhatsAppURL {
					t.Errorf("whatsapp not configured as expected: %+v", e.WhatsApp)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearVendorEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			e := FromEnv()

			provs := e.KYCProviders()
			if len(provs) != len(tc.wantProviders) {
				t.Fatalf("KYCProviders len = %d, want %d (%v)", len(provs), len(tc.wantProviders), e.Warnings)
			}
			for i, name := range tc.wantProviders {
				if provs[i].Name() != name {
					t.Errorf("provider[%d] = %q, want %q", i, provs[i].Name(), name)
				}
			}

			if _, ok := e.Sender(); ok != tc.wantSender {
				t.Errorf("Sender ok = %v, want %v", ok, tc.wantSender)
			}

			if len(e.Warnings) != tc.wantWarnings {
				t.Errorf("warnings = %v, want %d", e.Warnings, tc.wantWarnings)
			}

			summary := strings.Join(e.Summary(), ", ")
			for _, want := range tc.wantSummaryHas {
				if !strings.Contains(summary, want) {
					t.Errorf("summary %q missing %q", summary, want)
				}
			}

			if tc.check != nil {
				tc.check(t, e)
			}
		})
	}
}

// Summary must never include credential material even when everything is
// configured.
func TestSummaryNoCredentials(t *testing.T) {
	clearVendorEnv(t)
	t.Setenv("SMILE_ID_PARTNER_ID", "partner-secret-ish")
	t.Setenv("SMILE_ID_API_KEY", "smile-api-key-value")
	t.Setenv("TERMII_API_KEY", "termii-api-key-value")
	e := FromEnv()
	joined := strings.Join(e.Summary(), " ")
	for _, cred := range []string{"partner-secret-ish", "smile-api-key-value", "termii-api-key-value"} {
		if strings.Contains(joined, cred) {
			t.Fatalf("summary leaked credential %q: %q", cred, joined)
		}
	}
	if e.Summary() == nil {
		t.Fatal("summary must be non-nil when vendors are live")
	}
}
