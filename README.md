# PFAEngine — Pension Fund Administration as a Service

Multi-tenant, cloud-agnostic SaaS platform for licensed Pension Fund
Administrators (PFAs), implementing the PFA-as-a-Service Architecture
Report v1.1 and Specification v1.0 (PenCom digital-onboarding mandate
eff. 1 Jan 2027; "I Am Alive" liveness eff. 1 Dec 2026).

## What this repo is

The core administration engine: **15 microservices** (12 Phase 1 + benefit,
transfer, fund-pricing for Phase 2), an immutable double-entry ledger with a
Rust hot-path mirror, an event backbone, a versioned business-rules engine,
and a regulator adapter framework covering Nigeria (PenCom), Ghana, Kenya,
South Africa, and Egypt.

```
cmd/                 15 service binaries (§4 of the Architecture Report)
internal/            domain packages — all business logic, unit-tested
ledger-core/         Rust hot path: deterministic double-entry (AR-11)
api/openapi.yaml     API contract (§6 API inventory)
db/migrations/       PostgreSQL schema — tenant-scoped, no floats (§5, DA-01..07)
deploy/              docker-compose full stack + k8s base (§3.2, NFR-11)
rules/               versioned rule packs (NFR-19, SCM-15, REG-08)
docs/                traceability matrix and completion report
```

## Quick start

```bash
go test ./...            # all domain logic incl. golden rule tests
go build ./...           # 16 services
(cd ledger-core && cargo test)   # Rust hot path
docker compose -f deploy/docker-compose.yml up   # full stack
```

## Run locally (no Docker)

```bash
sh scripts/bootstrap-toolchain.sh   # restores Go/Rust toolchains (idempotent)
sh scripts/smoke.sh                 # builds api-gateway + member-svc, exercises
                                    # onboarding -> contribution -> balance -> recon,
                                    # probes, tenant enforcement, graceful shutdown
sh scripts/verify-dockerfile.sh     # static Dockerfile hardening checks
```

Dev-mode example (single service):

```bash
go run ./cmd/contribution &   # PORT=8084
curl -X POST localhost:8084/v1/employers/emp-1/schedules?period=2026-11 \
  -H 'X-Tenant-ID: demo-pfa' -H 'Content-Type: text/csv' \
  --data-binary $'rsa_pin,gross\nPEN100,80000000'
```

## Architecture decisions implemented

| Decision (§3.1) | Where |
|---|---|
| Double-entry immutable ledger | `internal/ledger` + `ledger-core/` (Rust) |
| Event-sourced core, Kafka backbone | `internal/events`, deploy/kafka |
| Tamper-evident audit (hash-chained WORM) | `internal/audit` |
| Per-tenant isolation + branding/flags | `internal/tenancy` |
| Money = integer kobo; units 8-dec; no floats | `internal/money` |
| Rules as configuration, versioned | `internal/rules` + `rules/` |
| Regulator adapters, not forks (REG-01) | `internal/regx` |
| NIN tokenized / BVN hash-only (DA-02) | `internal/kyc` |
| Maker-checker on claims (FR-401) | `internal/benefit` |
| RTS 365-day rule + quarterly cycles | `internal/transfer` |
| "I Am Alive" 12mo + 3mo grace (FR-108) | `internal/retiree` |

## Golden tests (SCM-15 / NFR-21)

`internal/rules/golden_test.go` reproduces the four §13.2 worked examples
(Nigeria DC Fund II, DB final-salary, CARE hybrid, Sharia) bit-for-bit.
Any rules-engine change that alters these numbers fails CI.

## Production toggles (v2)

| Env var | Effect |
|---|---|
| `DATABASE_URL` | Services persist to PostgreSQL (`internal/store`) |
| `KAFKA_BROKERS` | Event backbone binds to Kafka (`internal/stream`) |
| `SMILEID_API_KEY` / `DOJAH_SECRET_KEY` | Real KYC aggregators (`internal/vendors`) |
| `TERMII_API_KEY` | Real SMS delivery via Termii |
| `REMITA_*` | Employer remittance instructions via Remita |

## Tranche 3: durable workflows + front ends

- `internal/workflow` + `cmd/worker`: Temporal durable workflows with
  in-memory test-env coverage — RTS transfer (quarterly timer, 30-day SLA
  alarm), benefit claim (docs SLA, maker-checker signal, compensation),
  I-Am-Alive 12-month campaign with continue-as-new.
- `apps/member_flutter`: member app (Dart) — onboarding wizard
  (NIN→BVN→liveness→consent→RSA), dashboard with QR statement
  verification, I-Am-Alive, per-tenant white-label theming (PLT-14).
- `apps/staff-portal`: React+TS staff ops portal (Vite build verified) —
  reconciliation exception queue with aging, claims maker-checker,
  dual-control audit export, TOTP login.

## Tranche 5: remaining PenCom gap items

- `internal/investment` (FR-510/BR-16): variable-income caps from the rules
  pack, per-issuer/per-issue limits, 'A' rating gate + 20% BBB basket,
  prohibited-transaction screen (related parties, margin, borrowing).
- `internal/returns` (FR-511): daily valuation, rolling annualized return,
  Sharpe, TWR, monthly/quarterly RMAS returns.
- `internal/death` + `DeathClaimWorkflow` (FR-512/BR-20): Will/LoA claims,
  NOK KYC, maker-checker payout, 7-day docs SLA escalation.
- `internal/employer` GLIP (FR-515/BR-26), `internal/member` nominal RSA
  (FR-516/BR-27), `internal/capital` adequacy with 90-day cure
  (FR-517/BR-25), `internal/benefit` mortgage equity 25% (BR-21),
  `internal/legacy` AES/CPFA/ABS + NSITF credits (BR-23/24/28).
- `internal/kyc`: BVN/NIN 11-digit format validation wired into
  registration (FR-514); `FCYReturnWorkflow` enforces the 48-hour FCY
  return SLA (BR-22/NFR-23).

## Tranche 6: billing, i18n, pension increases, annuities

- `internal/store` + `db/migrations/0002_tranche6.sql`: Postgres repos for
  the member registry, WORM KYC evidence, statement metadata, and report
  artifacts (tenant-scoped, TEST_DATABASE_URL-gated integration tests).
- `internal/billing` (BR-08): SaaS tenant invoicing (base + per-active
  member with free allowance) and dunning status ladder with
  suspension gating platform access.
- `internal/i18n` + `i18n/*.json` (REG-06): member-facing message bundles
  (English + Nigerian Pidgin shipped; yo/ha/ig slots ready) with
  never-panic key fallback.
- `internal/benefit/increase.go` (SCM-09): basis-point pension increases
  gated on sustained surplus and policy caps.
- `internal/annuity` (SCM-11): NAICOM-licensed quote ranking and a
  maker-checker purchase state machine (Requested→Approved→Instructed).

## Tranche 7: lakehouse/MI, chatbot, M&A merge, wind-up, TB gate

- `internal/lakehouse` (BR-07/BR-09): analytics event sink (thread-safe
  `MemorySink`, JSONL `ParquetLine` staging for parquet export) and pure
  MI aggregations — contributions-by-month, employer delinquency, cohort
  retention, and `BuildDashboard` with tenant filtering.
- `internal/chatbot` (BR-09): deterministic, no-LLM intent classifier
  (longest-keyword-first, English + Nigerian Pidgin keywords) with
  localized canned replies via `internal/i18n` and USSD menu parity.
- `internal/merger` (BR-10): M&A roster re-tenanting with PIN-conflict
  detection, ordered `PlanMerge` steps through PenCom approval, and a
  per-member + total `BalanceProof`.
- `internal/windup` (SCM-14): strict forward-only wind-up lifecycle,
  statutory distribution order, and integer pro-rata residual allocation
  (remainder to the largest balance; sums exactly).
- `internal/tbgate` (AC-05): TigerBeetle ops-maturity gate — adoption
  requires all six capabilities (3-node replication, failover drill,
  backup-restore drill, 72h soak test, dual-control export, ledger
  replay parity) with evidence; Postgres stays the default ledger.

## Tranche 8: production hardening

- `internal/httpsrv`: shared HTTP service layer — `/healthz` liveness
  (always 200), `/readyz` readiness via a `Ready` hook, graceful shutdown
  on SIGTERM/SIGINT with a 10s drain, the `{error, code}` JSON error
  envelope, request-ID propagation (`X-Request-ID`), tenant middleware
  (`X-Tenant-ID`, 400 when missing), and an authn-backed bearer-token
  middleware hook (tokens are only issued after password + TOTP).
- `cmd/api-gateway`: path-prefix reverse-proxy routing (`/api/v1/members`
  → member service, `/api/v1/contributions`, `/api/v1/recon`, …) with
  env-driven service discovery (`MEMBER_SERVICE_URL`, …) and aggregated
  readiness across all configured backends; upstream failures surface as
  502 in the canonical envelope.
- `cmd/member-svc`: hardened member service wiring the domain packages
  over HTTP — KYC-gated onboarding (`kyc.VerifyNIN`/`ValidateBVN`),
  `GET /api/v1/members/{id}/balance` from the ledger, CSV contribution
  upload (`contribution.ParseCSV` → `recon.Engine` → ledger post), and
  the recon exception queue.
- `deploy/Dockerfile`: multi-stage `golang:1.23` → `alpine:3.20`,
  CGO-free static build, non-root user, `EXPOSE 8080`, `/healthz`
  healthcheck, one `SERVICE` build arg for every binary;
  `scripts/verify-dockerfile.sh` statically asserts the hardening.
- `scripts/smoke.sh`: self-contained end-to-end smoke (build → start →
  probes → onboard → upload → balance → recon → SIGTERM drain).
- `.github/workflows/ci.yml`: Go 1.23 build/vet/test + gofmt gate, Rust
  `cargo test` for ledger-core, Dockerfile static checks.

## Tranche 9: performance, vendor conformance, deploy validation

- Hot-path benchmarks with recorded numbers in
  [docs/BENCHMARKS.md](docs/BENCHMARKS.md): ledger post/balance/
  trial-balance/replay (`internal/ledger/bench_test.go`), 10k×10k
  reconciliation at the BR-03 ≥95% auto-match floor
  (`internal/recon/bench_test.go`), and 10k-row CSV schedule ingestion
  (`internal/contribution/bench_test.go`). Run:
  `go test -bench=. -benchtime=1x ./internal/ledger/... ./internal/recon/... ./internal/contribution/...`
- Vendor conformance suite (`internal/vendors/conformance_test.go`):
  reusable `ConformanceCase`/`RunConformance` harness over httptest
  servers; every adapter's auth/signature scheme is recomputed
  server-side (Smile ID HMAC-SHA256, Remita SHA-512 hash-key ordering,
  Termii `api_key`, Dojah `Authorization`, Africa's Talking `apiKey` +
  form encoding).
- Deploy validation (`sh scripts/validate-deploy.sh`): enforces k8s
  probes, non-root securityContext, resource requests/limits, pinned
  image tags, and compose healthcheck/depends_on for every app service
  plus all 15 microservices present — currently PASS.
- Kafka integration test (`internal/stream/kafka_integration_test.go`)
  gated on `TEST_KAFKA_BROKERS`: 100 keyed messages round-tripped with
  per-key hash-partition ordering and payload integrity asserted.

## Tranche 10: E2E lifecycle harness, security hardening, API contract validation

- `scripts/e2e.sh`: full member lifecycle against the real built binaries —
  onboarding (KYC-gated) → contribution CSVs from two employers (one line
  short-paid) → recon exception queue → maker-checker resolve
  (`POST /api/v1/recon/exceptions/{id}/resolve`) → balance → statement
  payload + HMAC verify-code round-trip (`GET /api/v1/members/{id}/statement`,
  `POST /api/v1/statements/verify`, good code accepted / tampered rejected) →
  tenant-missing 400 and bad-bearer 401 probes → dual-control audit export
  (`GET /api/v1/audit/export`, 403 without/with-same approver, 200 with a
  verified chain) → SIGTERM drain. PASS/FAIL per step, non-zero on failure.
- Security middleware (`internal/httpsrv/middleware.go`, stdlib only):
  `RateLimit(rps, burst)` per-client-IP token bucket (429 + `Retry-After` +
  JSON envelope), `MaxBody(n)` request cap (413), `SecurityHeaders`
  (nosniff, `X-Frame-Options: DENY`, `Cache-Control: no-store` on /api), and
  `OptionalAuth` (dev-mode bearer validation: a presented-but-invalid token
  is 401). All wired into both the api-gateway and member-svc chains.
- OpenAPI contract validation (`internal/httpsrv/openapi_check.go`): parses
  `api/openapi.yaml` and diffs documented operations against the exported
  route tables (`Routes() []string`) of member-svc and the gateway; the test
  fails on any documented-but-unimplemented or implemented-but-undocumented
  route, with a whitelisted owner named for each documented exception.
  `api/openapi.yaml` was reconciled to the shipped surface (balance,
  statement + verify, contributions/upload, recon exception resolve, GET
  audit export).
- `docs/SECURITY.md`: middleware inventory, auth flow, WORM audit and
  dual-control, tenant isolation.

## Tranche 11: WhatsApp channel (Meta Cloud API)

- `internal/vendors/whatsapp.go` — Meta WhatsApp Cloud API adapter
  (INT-07): `SendTemplate`/`SendText` POST to
  `/{phoneNumberId}/messages` with Bearer auth, `VerifyWebhookSignature`
  (X-Hub-Signature-256 HMAC-SHA256, constant-time compare, fail-closed on
  an unset app secret), `VerifyChallenge` for the subscription handshake,
  and `ParseInbound` extracting inbound messages while ignoring status
  updates. Conformance tests recompute the credential server-side and
  assert the request shape, like the other vendor adapters.
- `internal/chatbot` — `Responder.HandleWhatsApp` bridges inbound
  WhatsApp text to the existing deterministic intent engine (English +
  Nigerian Pidgin keywords) and returns the localized canned reply.
- `cmd/notification` — `GET /webhooks/whatsapp` answers the Meta
  challenge; `POST /webhooks/whatsapp` verifies the signature (401 on
  mismatch), classifies each inbound text and replies via `SendText`.
  The webhook bypasses the tenant header middleware (Meta carries none)
  but is gated by the per-tenant `whatsapp` feature flag (opt-out,
  default on); API endpoints stay tenant-scoped and health probes are
  public.
- `internal/notify` — `Service.SendWhatsApp` hook routes WHATSAPP-channel
  templates through the adapter (sorted var keys → ordered Meta template
  params) without an import cycle; falls back to the generic sender when
  unwired.
- `apps/member-pwa` — member-facing PWA (React+TS, Vite build verified):
  RSA PIN + phone sign-in and KYC-gated onboarding against member-svc,
  balance dashboard with naira formatting and fund badge, statement with
  HMAC verify-code QR round-trip, I-Am-Alive selfie capture (getUserMedia
  + file-input fallback), EN ↔ Nigerian Pidgin locale switcher, per-tenant
  theming via CSS custom properties, and a hand-rolled service worker
  (cache-first shell, network-first /api with last-known balance cached
  for offline, offline fallback page).

## Tranche 13: unified onboarding framework (FR-520)

- `internal/onboarding` — one state machine for every platform
  stakeholder. A `Case` walks DRAFT → DOCS_PENDING → KYC_PENDING →
  APPROVAL_PENDING → ACTIVE | REJECTED | SUSPENDED; `Pipeline(stakeholder)`
  returns the ordered step template for MEMBER (nin_verify, bvn_validate,
  consent_record, liveness_enroll, rsa_open), MICRO_MEMBER (id_capture,
  pin_request, optional first contribution), EMPLOYER (rc_validate,
  glip_evidence, mandate_form, pssp_link, approval marker), RETIREE
  (identity_verify, employment_history, bank_details, fund_iv_assignment,
  first_payroll), STAFF (rbac_role_assign, totp_enroll, approval marker),
  TENANT (license_evidence, capital_check, branding_config,
  admin_provision) and NOK (identity_verify, relationship_evidence,
  member_link). Steps complete strictly in order; EMPLOYER/STAFF/TENANT
  require maker-checker approval (`Approve` rejects approver == submitter)
  while person-facing pipelines auto-approve to ACTIVE. The thread-safe
  in-memory `Store` appends every mutation to the hash-chained audit store
  (SC-02). Full table-driven tests cover every pipeline, out-of-order /
  unknown / duplicate / optional-step rejections, maker-checker
  violations, auto-approve vs gated flows and audit-chain integrity.
- `cmd/member-svc` — `POST /api/v1/onboarding`,
  `GET /api/v1/onboarding/{id}`, `POST /api/v1/onboarding/{id}/steps/{step}`,
  `POST /api/v1/onboarding/{id}/approve`, `POST .../reject`. KYC steps run
  through the existing kyc facade (NIN/BVN verify, NDPA consent, PAD
  liveness); on ACTIVE a MEMBER case provisions the member via the
  create-member internals (registry + ledger accounts), MICRO_MEMBER opens
  a micro-pension account, RETIREE registers in the retiree register. All
  five routes are in `api/openapi.yaml` (contract test stays green).
- `cmd/employer-portal` — `POST /v1/employers` now opens an EMPLOYER
  onboarding case instead of a bare create; the employer starts PENDING
  and becomes COMPLIANT only when the case reaches ACTIVE (GLIP 3x
  emolument evidence validated via `internal/employer`, plus maker-checker
  approval at `/v1/employers/{id}/approve`).
- `cmd/identity` — `POST /v1/tenants` opens a TENANT case (PenCom licence
  evidence + capital adequacy via `internal/capital`); the tenant is
  provisioned only after approval. `POST /v1/staff` opens a STAFF case
  with a login-disabled account; the `totp_enroll` step enrolls MFA
  (RFC 6238) and approval enables login (`authn.SetDisabled`,
  `ErrAccountDisabled`).
- `scripts/e2e-onboarding.sh` — full-lifecycle curl harness against the
  built binaries: member (5 steps → ACTIVE → balance works), employer
  (GLIP 3x gate, same-approver 403, distinct-approver approval →
  COMPLIANT), staff (pre-approval login blocked, TOTP enroll → approve →
  TOTP login works).

## Tranche 14: durable persistence for onboarding + preferences

- `db/migrations/0003_onboarding.sql` (additive) — `onboarding_case` +
  `onboarding_step` (updatable: the status machine mutates them),
  `onboarding_document` (WORM: REVOKE UPDATE/DELETE plus a BEFORE
  UPDATE/DELETE trigger so even the table owner cannot mutate evidence),
  `member_prefs` (PK (tenant_id, member_id); phone lookup index on
  (tenant_id, phone_e164)). Index on (tenant_id, stakeholder, status) for
  case listing.
- `internal/store/onboarding_repo.go` — `OnboardingRepo` (Save upserts
  case + steps + new documents in one `WithTenant` transaction; Load
  rebuilds pipeline step order from the stakeholder template; List filters
  by stakeholder/status) and `PrefsRepo` (Set/Get locale, LinkPhone with
  `prefs.NormalizePhone` E.164 normalisation, MemberForPhone,
  LocaleForPhone). Both implement the new `onboarding.Repo` / `prefs.Repo`
  interfaces — the domain packages never import `store`.
- `internal/onboarding` + `internal/prefs` — `PersistentStore`
  write-through wrappers: every mutation applies the in-memory state
  machine (audit appends intact) and persists; a repo error fails the
  mutation and rolls memory back. Reads prefer the repo and degrade to
  memory on transient errors; a nil repo is pure in-memory (backward
  compatible). Repo-loaded cases hydrate memory so the state machine can
  mutate them after a restart, and Create skips IDs already persisted by
  earlier process lifetimes.
- `cmd/member-svc` — when `DATABASE_URL` is set, the onboarding store and
  prefs store are Postgres write-through; otherwise in-memory (unchanged).
- Tests: unit write-through/rollback/fallback/load-through suites with
  fake repos; `TEST_DATABASE_URL`-gated integration round-trips (full
  EMPLOYER lifecycle create → steps → approve → reload, WORM UPDATE/DELETE
  rejection, prefs locale + normalised phone round-trip, tenant
  isolation), idempotent across reruns via unique run suffixes.

## Honest caveats (v3 scope)

- Flutter app is source-scaffolded (Flutter SDK not available here);
  `flutter pub get && flutter run` on any machine with the SDK.
- Remaining Postgres repos: transfer/recon/ledger hot path.

See `docs/TRACEABILITY.md` for requirement-ID → code mapping and
`docs/COMPLETION.md` for the completion score.
