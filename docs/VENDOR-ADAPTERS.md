# Vendor Adapter Integration (Gap 2) — Env-Configurable Vendor Selection

`internal/vendors` now ships `env.go`: `vendors.FromEnv()` reads the process
environment, builds every fully-configured adapter, and reports partial
configurations via `Env.Warnings` (a vendor is **never** half-configured).
When nothing is configured the caller keeps the existing mocks — behaviour
with no env vars set is byte-for-byte identical to today.

API surface (internal/vendors/env.go):

```go
type Env struct {
    SmileID        *SmileID         // SMILE_ID_PARTNER_ID + SMILE_ID_API_KEY
    Dojah          *Dojah           // DOJAH_APP_ID + DOJAH_SECRET_KEY
    Termii         *Termii          // TERMII_API_KEY
    Remita         *Remita          // REMITA_MERCHANT_ID + REMITA_API_KEY
    AfricasTalking *AfricasTalking  // AT_API_KEY + AT_USERNAME
    WhatsApp       *Client          // WHATSAPP_ACCESS_TOKEN + WHATSAPP_PHONE_NUMBER_ID
    Warnings       []string         // partial-config notices, credential-free
}

func FromEnv() Env
func (Env) KYCProviders() []kyc.Provider      // smileid first, dojah second; EMPTY when none
func (Env) Sender() (notify.Sender, bool)     // Termii when configured
func (Env) Summary() []string                 // e.g. "smileid (kyc)" — never credentials
```

---

## 1. cmd/member-svc/main.go — KYC provider wiring (lines 193–195)

Current code:

```go
	d := &deps{
		kyc: kyc.NewService(salt,
			kyc.MockProvider{ProviderName: "mock-nimc"},
			kyc.MockProvider{ProviderName: "mock-dojah"}),
```

Replace with:

```go
	// Vendor selection (Gap 2): live KYC aggregators when the credential
	// pairs are present in the environment; deterministic mocks otherwise.
	ven := vendors.FromEnv()
	for _, w := range ven.Warnings {
		log.Printf("member-svc: vendor config warning: %s", w)
	}
	kycProviders := ven.KYCProviders()
	if len(kycProviders) == 0 {
		kycProviders = []kyc.Provider{
			kyc.MockProvider{ProviderName: "mock-nimc"},
			kyc.MockProvider{ProviderName: "mock-dojah"},
		}
		log.Printf("member-svc: no KYC vendor env credentials — using mock providers")
	} else {
		log.Printf("member-svc: LIVE vendors: %s", strings.Join(ven.Summary(), ", "))
	}

	d := &deps{
		kyc: kyc.NewService(salt, kycProviders...),
```

and add to the import block:

```go
	"github.com/munisp/pfaengine/internal/vendors"
```

(`log` and `strings` are already imported in this file.)

## 2. cmd/notification/main.go — notify.Sender + WhatsApp wiring (lines 49–65)

Note: the `notify.MockSender` wiring lives in **cmd/notification/main.go**,
not cmd/member-svc. Current code:

```go
	var sender notify.Sender = &notify.MockSender{}
	if os.Getenv("TERMII_API_KEY") != "" { // INT-07
		sender = &vendors.Termii{
			APIKey: os.Getenv("TERMII_API_KEY"), SenderID: appx.Env("TERMII_SENDER_ID", "PFAEngine"),
			BaseURL: appx.Env("TERMII_BASE_URL", "https://v3.api.termii.com"),
		}
	}
	svc := notify.NewService(sender)
	...
	wa := &vendors.Client{
		PhoneNumberID: os.Getenv("WHATSAPP_PHONE_NUMBER_ID"),
		AccessToken:   os.Getenv("WHATSAPP_ACCESS_TOKEN"),
		AppSecret:     os.Getenv("WHATSAPP_APP_SECRET"),
		BaseURL:       appx.Env("WHATSAPP_BASE_URL", "https://graph.facebook.com/v21.0"),
	}
```

Replace with:

```go
	ven := vendors.FromEnv()
	for _, w := range ven.Warnings {
		log.Printf("[notification] vendor config warning: %s", w)
	}
	var sender notify.Sender = &notify.MockSender{}
	if s, ok := ven.Sender(); ok { // TERMII_API_KEY set
		sender = s
	}
	svc := notify.NewService(sender)
	...
	wa := ven.WhatsApp // nil when the WHATSAPP_* pair is incomplete
	if wa == nil {
		wa = &vendors.Client{} // webhook handlers stay registered but reject everything
	}
```

The existing `if wa.AccessToken != ""` guard downstream already handles the
"not configured" case, so no further changes are needed there.

## 3. Environment variable reference (deploy/docker-compose.yml)

Add under the service's `environment:` block. **member-svc** needs the KYC
pairs; **notification** needs TERMII_* and WHATSAPP_*; Remita/Africa's
Talking adapters are constructed by whichever service consumes them
(contribution/USSD flows) — same env names apply everywhere.

| Variable | Required | Default | Used by | Notes |
|---|---|---|---|---|
| `SMILE_ID_PARTNER_ID` | pair with key | — | member-svc (KYC) | Smile ID partner id from the dashboard |
| `SMILE_ID_API_KEY` | required pair | — | member-svc (KYC) | HMAC-SHA256 signing key |
| `SMILE_ID_BASE_URL` | optional | `https://api.smileidentity.com/v2` | member-svc | Sandbox: `https://testapi.smileidentity.com/v1` |
| `DOJAH_APP_ID` | required pair | — | member-svc (KYC fallback) | Sent as `AppId` header |
| `DOJAH_SECRET_KEY` | required pair | — | member-svc | Sent as `Authorization` header (raw, no `Bearer`) |
| `DOJAH_BASE_URL` | optional | `https://api.dojah.io` | member-svc | Sandbox: `https://sandbox.dojah.io` |
| `TERMII_API_KEY` | required | — | notification (SMS) | Sole credential; sent in JSON payload |
| `TERMII_SENDER_ID` | optional | `PFAEngine` | notification | Registered sender id (max 11 chars) |
| `TERMII_BASE_URL` | optional | `https://v3.api.termii.com` | notification | — |
| `REMITA_MERCHANT_ID` | required pair | — | remittance flows | SHA-512 hash key convention |
| `REMITA_API_KEY` | required pair | — | remittance flows | Never logged |
| `REMITA_BASE_URL` | optional | `https://remitademo.net/remita/exapp/api/v1/send/api` (demo) | remittance flows | Production: `https://www.remita.net/...` |
| `AT_API_KEY` | required pair | — | USSD/SMS flows | `apiKey` header |
| `AT_USERNAME` | required pair | — | USSD/SMS flows | Sandbox account username is `sandbox` |
| `AT_BASE_URL` | optional | `https://api.africastalking.com` | USSD/SMS flows | Sandbox: `https://api.sandbox.africastalking.com` |
| `AT_SHORT_CODE` | optional | — | USSD flows | Service/USSD short code |
| `WHATSAPP_ACCESS_TOKEN` | required pair | — | notification (WhatsApp) | Meta system-user token |
| `WHATSAPP_PHONE_NUMBER_ID` | required pair | — | notification | From Meta app dashboard |
| `WHATSAPP_APP_SECRET` | optional | — | notification | Enables `X-Hub-Signature-256` webhook verification |
| `WHATSAPP_BASE_URL` | optional | `https://graph.facebook.com/v21.0` | notification | — |
| `WHATSAPP_VERIFY_TOKEN` | optional | — | notification | Meta subscription challenge token (consumed by existing code) |

Example compose snippet:

```yaml
  member-svc:
    environment:
      KYC_SERVICE_URL: http://kyc-service:8105
      SMILE_ID_PARTNER_ID: ${SMILE_ID_PARTNER_ID}
      SMILE_ID_API_KEY: ${SMILE_ID_API_KEY}
      SMILE_ID_BASE_URL: https://testapi.smileidentity.com/v1   # drop for live
      DOJAH_APP_ID: ${DOJAH_APP_ID}
      DOJAH_SECRET_KEY: ${DOJAH_SECRET_KEY}
      DOJAH_BASE_URL: https://sandbox.dojah.io                   # drop for live

  notification:
    environment:
      TERMII_API_KEY: ${TERMII_API_KEY}
      TERMII_SENDER_ID: PFAEngine
      WHATSAPP_ACCESS_TOKEN: ${WHATSAPP_ACCESS_TOKEN}
      WHATSAPP_PHONE_NUMBER_ID: ${WHATSAPP_PHONE_NUMBER_ID}
      WHATSAPP_APP_SECRET: ${WHATSAPP_APP_SECRET}
      WHATSAPP_VERIFY_TOKEN: ${WHATSAPP_VERIFY_TOKEN}
```

## 4. Sandbox vs live

- **Smile ID** — environments are fully isolated (separate keys per
  environment). Sandbox base URL `https://testapi.smileidentity.com/v1`
  with **sandbox** partner id/api key; only test identities from the Smile
  ID docs verify successfully. Live: unset `SMILE_ID_BASE_URL` (default
  `https://api.smileidentity.com/v2`) and swap in production keys.
- **Dojah** — sandbox `https://sandbox.dojah.io` returns mock data and never
  charges the wallet; deterministic test values include NIN `70123456789`
  and BVN `22222222222`. Live: unset `DOJAH_BASE_URL` (default
  `https://api.dojah.io`) and fund the wallet — live calls are billed per
  request (`402 Payment Required` on low balance).
- **Remita** — the adapter default points at the demo host
  (`remitademo.net`); production RRR generation/status requires
  `REMITA_BASE_URL=https://www.remita.net/remita/exapp/api/v1/send/api`
  plus production merchant id/api key.
- **Africa's Talking** — sandbox host `https://api.sandbox.africastalking.com`
  with username `sandbox`; no real SMS delivery.
- **Termii / WhatsApp** — no separate sandbox host; use a test sender id /
  Meta test number respectively.

## 5. Behaviour guarantees

- No env vars set → mocks everywhere, zero warnings, unchanged behaviour.
- Only one of a required pair set → that vendor is disabled **and** a
  warning naming the missing variable is emitted (boot log). Warnings and
  `Summary()` never contain credential values.
- `KYCProviders()` order is always Smile ID first, Dojah second — Dojah
  acts as the automatic failover inside `kyc.Service`.
