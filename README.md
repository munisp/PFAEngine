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

## Honest caveats (v3 scope)

- Flutter app is source-scaffolded (Flutter SDK not available here);
  `flutter pub get && flutter run` on any machine with the SDK.
- Remaining Postgres repos: transfer/recon/ledger hot path.

See `docs/TRACEABILITY.md` for requirement-ID → code mapping and
`docs/COMPLETION.md` for the completion score.
