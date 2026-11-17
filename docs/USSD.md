# USSD Channel — Africa's Talking Callback

BR-09 / INT-08: members without smartphones or data reach the same
deterministic intent engine that powers the chatbot and WhatsApp surfaces,
through an Africa's Talking (AT) USSD short code. The HTTP endpoint is
`POST /api/v1/ussd/callback` on member-svc (`cmd/member-svc/ussd.go`).

## Flow

1. The member dials the short code (e.g. `*384*123#`). AT opens a session
   and POSTs `application/x-www-form-urlencoded` to the callback URL with
   the case-sensitive fields:
   - `sessionId` — AT session identifier (protocol metadata; unused by the
     stateless handler),
   - `serviceCode` — the dialled short code,
   - `phoneNumber` — the member's MSISDN (parsed for fidelity only; **never
     logged or persisted** — NDPA data minimisation),
   - `text` — the **cumulative**, `*`-joined input chain for the session
     (empty on the first request, then e.g. `1`, later `1*3`).
2. member-svc replies `200 OK` with `Content-Type: text/plain` and a body
   starting with:
   - `CON ` — keep the session open; the rest of the body is the next screen
     (the root menu or an error line + menu), or
   - `END ` — terminate the session; the rest of the body is the final
     reply (the selected intent's canned string).

### Menu (parity with `chatbot.USSDCode`)

```
Welcome to PFAEngine. Reply with a number:
1. Check RSA balance
2. Last contribution
3. Get statement
4. I-Am-Alive confirmation
5. RSA transfer status
6. Retirement projection
7. Grow my savings
8. Change fund (I-IV)
9. Mortgage equity (25% RSA)
10. Retirement claims
11. Next-of-kin / nominees
12. Learn about pensions
13. Raise a complaint
14. Track a case
```

Every selection is terminal: the digit maps 1:1 to a `chatbot.Intent` via
`chatbot.USSDCode`, and the member gets that intent's canned reply
(English default; `i18n/en.json` + `i18n/pcm-NG.json`, loaded once from
`I18N_DIR`, default `i18n` — the same convention as cmd/notification).

### Stateless design (why no session store)

The intent surface is a flat, single-level menu — there are no submenus to
remember — so the handler keeps **no per-session state**. The rule is: the
last `*`-separated segment of `text` is the answer to the screen currently
on display. That makes invalid-input recovery work naturally:

- dial → `text=""` → `CON <menu>`
- member types `99` → `text="99"` → `CON Invalid selection…` + menu
- member types `2` → `text="99*2"` → last segment `2` → `END <reply>`
- deep/test chains like `text="1*3"` resolve the last segment (`3`).

No in-memory session map means no TTL eviction to tune and no unbounded
memory growth under callback floods; the shared per-IP
`httpsrv.RateLimit(100, 200)` middleware fronts the route like every other.

## Africa's Talking dashboard configuration

1. Create an AT account (production) or use the sandbox
   (`https://account.africastalking.com`, username `sandbox`).
2. **USSD → Service Codes → Create channel**: register a callback URL of
   `https://<gateway-host>/api/v1/ussd/callback`. For sandbox, AT issues a
   shared test code you dial from the simulator or a registered test MSISDN.
3. Set env on member-svc (shared with the SMS adapter,
   `internal/vendors/env.go`):
   - `AT_API_KEY` + `AT_USERNAME` (required pair for the SMS leg),
   - `AT_SHORT_CODE` (your service code),
   - `AT_BASE_URL` (optional; sandbox is
     `https://api.sandbox.africastalking.com`),
   - `I18N_DIR` (optional; defaults to `i18n`).
4. Point the AT callback at the public ingress; the route sits behind the
   standard member-svc middleware chain (rate limit, body cap, security
   headers).

## Testing with curl

```sh
# New session -> CON menu
curl -s -X POST http://localhost:8096/api/v1/ussd/callback \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'sessionId=ATUid_1&serviceCode=*384*123%23&phoneNumber=%2B2348012345678&text='

# Pick "1. Check RSA balance" -> END canned reply
curl -s -X POST http://localhost:8096/api/v1/ussd/callback \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'sessionId=ATUid_1&serviceCode=*384*123%23&phoneNumber=%2B2348012345678&text=1'

# Invalid input then recovery (cumulative text)
curl -s -X POST http://localhost:8096/api/v1/ussd/callback \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'sessionId=ATUid_1&serviceCode=*384*123%23&phoneNumber=%2B2348012345678&text=99*2'
```

The AT sandbox also ships a web USSD simulator
(**USSD → Simulator**) — point it at a tunnelled (e.g. ngrok) callback URL
for an end-to-end dial experience.

## Honest caveats

- **Shortcode registration is manual.** A dedicated production short code
  (or a shared code + keyword) is provisioned by Africa's Talking or a local
  aggregator (e.g. through the MNOs); expect paperwork and lead time. The
  sandbox short code works only against the sandbox environment and cannot
  be dialled from real handsets on production networks.
- **Replies are canned pointers, not live account data.** Consistent with
  BR-09's deterministic engine, the END replies tell the member where to
  complete the action (app / `*789#`); they do not read balances from the
  ledger. There is a separate PIN-gated live-lookup menu in
  `internal/vendors/africastalking.go` (`AfricasTalking.Handle`) that is not
  wired to this HTTP endpoint.
- **No per-member personalisation.** `member.Registry` has no phone-number
  index (only `RSAByPIN`), so the handler cannot resolve the MSISDN to a
  member for a greeting or locale preference; replies default to English.
  Adding a phone index would unlock Pidgin-by-preference replies.
- **English menu chrome.** The menu labels are hardcoded English; only the
  canned replies go through the i18n bundles (English fallback chain).
- **Callback authentication.** AT does not sign callback requests; the
  endpoint is therefore read-only-by-construction (no mutations, no PII in
  responses or logs) and relies on the shared rate limiter for abuse
  resistance. Treat `phoneNumber` as untrusted input.
- **Sandbox vs production.** `AT_BASE_URL`/`AT_USERNAME` differ between
  sandbox and production (`sandbox` username); the USSD callback path itself
  is identical in both.
