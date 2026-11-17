package vendors

import (
	"fmt"
	"os"
	"strings"

	"github.com/munisp/pfaengine/internal/kyc"
	"github.com/munisp/pfaengine/internal/notify"
)

// Env bundles every vendor adapter whose credentials are present in the
// process environment. FromEnv never partially configures an adapter: a
// vendor is LIVE only when its full required credential set is present, and
// any partial configuration is surfaced in Warnings (never silently).
//
// Boot usage (see INTEGRATION-vendors.md):
//
//	ven := vendors.FromEnv()
//	providers := ven.KYCProviders()
//	if len(providers) == 0 {
//		providers = []kyc.Provider{kyc.MockProvider{...}, kyc.MockProvider{...}}
//	}
//	if s, ok := ven.Sender(); ok { ... }
//	for _, line := range ven.Summary() { log.Printf("vendor %s", line) }
type Env struct {
	SmileID        *SmileID
	Dojah          *Dojah
	Termii         *Termii
	Remita         *Remita
	AfricasTalking *AfricasTalking
	WhatsApp       *Client

	// Warnings reports partial or invalid configuration (e.g. only one of a
	// required credential pair set). Values are human-readable and never
	// contain credentials.
	Warnings []string
}

// Default API base URLs (overridable via the *_BASE_URL env vars, e.g. to
// point at sandbox hosts — see INTEGRATION-vendors.md).
const (
	DefaultSmileIDBaseURL = "https://api.smileidentity.com/v2"
	DefaultDojahBaseURL   = "https://api.dojah.io"
	DefaultTermiiBaseURL  = "https://v3.api.termii.com"
	// Remita's demo environment; production is https://www.remita.net.
	DefaultRemitaBaseURL = "https://remitademo.net/remita/exapp/api/v1/send/api"
	DefaultATBaseURL     = "https://api.africastalking.com"
	DefaultWhatsAppURL   = "https://graph.facebook.com/v21.0"
)

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// FromEnv reads the environment and constructs every fully-configured
// vendor adapter. Required pairs:
//
//	Smile ID:          SMILE_ID_PARTNER_ID + SMILE_ID_API_KEY
//	Dojah:             DOJAH_APP_ID + DOJAH_SECRET_KEY
//	Termii:            TERMII_API_KEY
//	Remita:            REMITA_MERCHANT_ID + REMITA_API_KEY
//	Africa's Talking:  AT_API_KEY + AT_USERNAME
//	WhatsApp:          WHATSAPP_ACCESS_TOKEN + WHATSAPP_PHONE_NUMBER_ID
func FromEnv() Env {
	var e Env
	warn := func(format string, args ...any) {
		// Warnings must never include credential values — only var names.
		e.Warnings = append(e.Warnings, fmt.Sprintf(format, args...))
	}

	// Smile ID (INT-02/03/04): partner id + api key are a required pair.
	partnerID, smileKey := os.Getenv("SMILE_ID_PARTNER_ID"), os.Getenv("SMILE_ID_API_KEY")
	switch {
	case partnerID != "" && smileKey != "":
		e.SmileID = &SmileID{PartnerID: partnerID, APIKey: smileKey,
			BaseURL: getenv("SMILE_ID_BASE_URL", DefaultSmileIDBaseURL)}
	case partnerID != "" || smileKey != "":
		warn("smileid: SMILE_ID_PARTNER_ID and SMILE_ID_API_KEY must both be set — adapter disabled")
	}

	// Dojah (INT-02/03/04 fallback KYC aggregator): app id + secret key pair.
	appID, dojahSecret := os.Getenv("DOJAH_APP_ID"), os.Getenv("DOJAH_SECRET_KEY")
	switch {
	case appID != "" && dojahSecret != "":
		e.Dojah = &Dojah{AppID: appID, SecretKey: dojahSecret,
			BaseURL: getenv("DOJAH_BASE_URL", DefaultDojahBaseURL)}
	case appID != "" || dojahSecret != "":
		warn("dojah: DOJAH_APP_ID and DOJAH_SECRET_KEY must both be set — adapter disabled")
	}

	// Termii (INT-07 SMS): single API key; sender id has a platform default.
	if key := os.Getenv("TERMII_API_KEY"); key != "" {
		e.Termii = &Termii{APIKey: key,
			SenderID: getenv("TERMII_SENDER_ID", "PFAEngine"),
			BaseURL:  getenv("TERMII_BASE_URL", DefaultTermiiBaseURL)}
	}

	// Remita (INT-06 payments): merchant id + api key pair.
	merchantID, remitaKey := os.Getenv("REMITA_MERCHANT_ID"), os.Getenv("REMITA_API_KEY")
	switch {
	case merchantID != "" && remitaKey != "":
		e.Remita = &Remita{MerchantID: merchantID, APIKey: remitaKey,
			BaseURL: getenv("REMITA_BASE_URL", DefaultRemitaBaseURL)}
	case merchantID != "" || remitaKey != "":
		warn("remita: REMITA_MERCHANT_ID and REMITA_API_KEY must both be set — adapter disabled")
	}

	// Africa's Talking (INT-08 USSD/SMS): api key + username pair.
	atKey, atUser := os.Getenv("AT_API_KEY"), os.Getenv("AT_USERNAME")
	switch {
	case atKey != "" && atUser != "":
		e.AfricasTalking = &AfricasTalking{APIKey: atKey, Username: atUser,
			ShortCode: os.Getenv("AT_SHORT_CODE"),
			BaseURL:   getenv("AT_BASE_URL", DefaultATBaseURL)}
	case atKey != "" || atUser != "":
		warn("africastalking: AT_API_KEY and AT_USERNAME must both be set — adapter disabled")
	}

	// WhatsApp Cloud API (INT-07): access token + phone number id pair;
	// WHATSAPP_APP_SECRET is only needed for inbound webhook verification.
	waToken, waPhoneID := os.Getenv("WHATSAPP_ACCESS_TOKEN"), os.Getenv("WHATSAPP_PHONE_NUMBER_ID")
	switch {
	case waToken != "" && waPhoneID != "":
		e.WhatsApp = &Client{AccessToken: waToken, PhoneNumberID: waPhoneID,
			AppSecret: os.Getenv("WHATSAPP_APP_SECRET"),
			BaseURL:   getenv("WHATSAPP_BASE_URL", DefaultWhatsAppURL)}
	case waToken != "" || waPhoneID != "":
		warn("whatsapp: WHATSAPP_ACCESS_TOKEN and WHATSAPP_PHONE_NUMBER_ID must both be set — adapter disabled")
	}

	return e
}

// KYCProviders returns the configured KYC aggregators in priority order —
// Smile ID first, Dojah second — ready to pass to kyc.NewService. It
// returns an EMPTY slice when no KYC vendor is configured; the caller is
// responsible for falling back to kyc.MockProvider instances in that case.
func (e Env) KYCProviders() []kyc.Provider {
	var out []kyc.Provider
	if e.SmileID != nil {
		out = append(out, e.SmileID)
	}
	if e.Dojah != nil {
		out = append(out, e.Dojah)
	}
	return out
}

// Sender returns the Termii SMS adapter as a notify.Sender, or false when
// TERMII_API_KEY is not set (caller keeps notify.MockSender).
func (e Env) Sender() (notify.Sender, bool) {
	if e.Termii != nil {
		return e.Termii, true
	}
	return nil, false
}

// Summary lists the LIVE vendors for boot logging. Names and roles only —
// never credentials. Example:
//
//	["smileid (kyc)", "dojah (kyc)", "termii (sms)", "remita (payments)",
//	 "africastalking (ussd/sms)", "whatsapp (messaging)"]
func (e Env) Summary() []string {
	var out []string
	if e.SmileID != nil {
		out = append(out, "smileid (kyc)")
	}
	if e.Dojah != nil {
		out = append(out, "dojah (kyc)")
	}
	if e.Termii != nil {
		out = append(out, "termii (sms)")
	}
	if e.Remita != nil {
		out = append(out, "remita (payments)")
	}
	if e.AfricasTalking != nil {
		out = append(out, "africastalking (ussd/sms)")
	}
	if e.WhatsApp != nil {
		out = append(out, "whatsapp (messaging)")
	}
	return out
}
