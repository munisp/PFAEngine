# Gap 9 — Challenge-Response (Active) Liveness / Anti-Spoofing

Status: implemented (`internal/liveness`, wired into `internal/ekyc` and
`cmd/member-svc`). Vendor endpoint spec is **assumed** — see caveats.

## Threat model

Passive liveness (MiniFASNet frame scoring in the kyc-service sidecar)
analyzes selfie frames the client uploads. It is necessary but not
sufficient for PenCom-grade high-assurance flows (account opening, benefit
withdrawal) because an attacker can feed the capture pipeline:

1. **Replay attacks** — a recorded video of the victim played back to the
   camera (or injected past it). Texture-based passive PAD models lose to
   high-quality replays on modern screens.
2. **Printed-photo / mask attacks** — a still photograph or 2D mask
   presented to the camera; cheap and effective against single-frame
   passive checks.
3. **Deepfake / injection attacks** — synthetic video pushed through a
   virtual camera or a compromised/tampered client, bypassing the physical
   camera entirely. Passive analysis runs on attacker-controlled pixels.

**Challenge-response (active liveness)** binds the session to a live human
*right now*: the server issues a randomized, nonce-bound, short-lived
sequence of actions (`BLINK`, `TURN_LEFT`, `TURN_RIGHT`, `SMILE`, `NOD`)
that the user must perform. A replayed video cannot anticipate the random
action sequence, and the nonce + single-use + TTL semantics make
replay-of-response worthless.

## Passive vs active

| | Passive (existing) | Active (Gap 9) |
|---|---|---|
| Signal | MiniFASNet texture/moiré scoring of frames | Randomized action sequence bound to nonce |
| Defeats | printed photo (low quality), static screen | replay video, printed photo, scripted injection |
| UX | frictionless | 5-15s guided action sequence |
| Endpoint | `POST /v1/ekyc/sessions/{id}/biometric` | `POST .../challenge` + `POST .../challenge/respond` |
| Required when | default | `EKYC_REQUIRE_CHALLENGE=true` or a challenge was issued |

The two compose: passive scoring still runs when the sidecar is available;
active liveness adds the anti-replay guarantee. **Fail-closed invariants:**

- When `EKYC_REQUIRE_CHALLENGE=true`, `SubmitBiometric` rejects with
  `ErrChallengeRequired` — the passive path is closed.
- Once a challenge is issued for a session, the passive path is closed for
  that session (state guard).
- `Decide` never auto-approves a session whose required challenge is
  unpassed (`challenge_incomplete` flag → manual review).
- A failed challenge flags `challenge_failed` (weight 60 → REJECT).
- Handlers return `503 LIVENESS_UNCONFIGURED` when no engine is wired.

## Protocol

```
client                                member-svc
  |-- POST /v1/ekyc/sessions/{id}/challenge ------------->|
  |<- 201 {id, session_id, actions, nonce, expires_at} ---|
  |   (user performs actions on camera; client-side       |
  |    action detection or vendor SDK produces results)   |
  |-- POST .../challenge/respond ------------------------>|
  |    {challenge_id, nonce, results:[{action,passed,score}]}
  |<- 200 Session (challenge_passed, challenge_score) ----|
```

Verdict rule (in `internal/liveness`): **every** challenged action must be
reported and passed, AND the mean action score must be ≥ threshold
(default `0.60`, env `LIVENESS_THRESHOLD`). The challenge is consumed on
any response — there is no partial-probe retry against the same nonce.

Challenge properties: 2-3 distinct actions sampled with `crypto/rand`;
nonce = 16 bytes `crypto/rand` hex; TTL 120s; one active challenge per
session (re-issue invalidates the prior one); nonces compared in constant
time; single-use.

## Client UX contract (Flutter / PWA)

- **Issue** the challenge when the session reaches the biometric step
  (state `BIOMETRIC`, or call directly from `IDENTITY_CHECK` — issuing
  transitions the session to `BIOMETRIC`).
- **Render** `actions` in order. Suggested per-action guidance:
  `BLINK` ("blink slowly"), `TURN_LEFT`/`TURN_RIGHT` ("turn your head"),
  `SMILE` ("smile"), `NOD` ("nod once").
- **Timing**: the whole sequence must complete within 120 seconds
  (`expires_at`). Show a countdown; on expiry request a fresh challenge
  (`410 CHALLENGE_EXPIRED`).
- **Action detection** is the client's responsibility (e.g. on-device ML
  Kit / vendor SDK). Report one `{action, passed, score∈[0,1]}` per
  challenged action, in any order.
- **Retry policy**: a failed or incomplete response consumes the
  challenge — issue a new one and retry the full sequence. Do not retry
  the same `challenge_id`/`nonce` (`400 CHALLENGE_INVALID`). After a
  failure the session carries the `challenge_failed` risk flag, which
  forces REJECT at decision time; staff override via the KYC_REVIEW case
  remains the recovery path.
- **Never** upload raw frames to these endpoints; they accept no imagery.

## Privacy / persistence

No selfie frames or base64 imagery are stored, persisted or logged anywhere
in this flow. Session snapshots (`ekyc.session`) carry only verdict
metadata: `challenge_required`, `challenge_passed`, `challenge_score`,
`challenge_id`. The vendor path (`RespondWithProvider`) streams the selfie
to the vendor over TLS and discards it.

## Configuration

| Env var | Default | Meaning |
|---|---|---|
| `EKYC_REQUIRE_CHALLENGE` | unset (off) | `true` mandates active liveness for all sessions (high-assurance tenants) |
| `LIVENESS_THRESHOLD` | `0.60` | minimum mean action score for a PASSED verdict |
| `SMILE_ID_PARTNER_ID` + `SMILE_ID_API_KEY` | unset | both required to enable the Smile ID SmartSelfie provider |
| `SMILE_ID_BASE_URL` | `https://api.smileidentity.com` | override for sandbox |

## Smile ID pairing

`liveness.FromEnv()` returns a `SmileIDProvider` when both Smile ID
credentials are set, else `nil` (local/mock scoring mode — the challenge
engine works standalone with client-reported action results). The provider
signs requests exactly like the existing `internal/vendors` Smile ID
adapter (HMAC-SHA256 over `timestamp+partner_id+"sid_request"`, base64 of
hex) and POSTs:

```
POST {SMILE_ID_BASE_URL}/v2/smart-selfie
{partner_id, timestamp, signature, session_id, job_type: "smart_selfie_active",
 actions: [...], image_b64: <selfie>}
→ {ResultCode, ResultText, ConfidenceValue (0-100), LivenessPassed}
```

The adapter is fail-closed: vendor 5xx, transport errors, or malformed
responses are errors (never a pass); non-success result codes yield
`passed=false`.

## Honest caveats

- **ASSUMED-SPEC**: the `/v2/smart-selfie` endpoint path and the
  request/response field names above are extrapolated from the existing
  Smile ID v2 adapter conventions, NOT from official SmartSelfie docs.
  Reconcile with Smile Identity's documentation before production use;
  only the signature scheme is known-good (mirrors `internal/vendors`).
- **Client-side capture integrity is out of scope server-side.** Action
  detection happens on the client (or vendor SDK); a fully compromised
  client can lie about `passed`/`score`. Server-side we mitigate with
  randomization, nonce binding, single-use and TTL, but true device
  attestation (Play Integrity / App Attest) and vendor-side active
  verification (Smile ID SmartSelfie enrollment) are the production-grade
  answers.
- A failed challenge flags `challenge_failed` (weight 60) and that flag is
  sticky within the session — a later pass does not clear it (consistent
  with the existing risk-flag model). Recovery is ops override on the
  KYC_REVIEW case.
- Challenges are in-memory only; a process restart invalidates in-flight
  challenges (clients re-issue). This is deliberate: challenge state
  contains no PII and re-issue is cheap.
