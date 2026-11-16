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
go build ./...           # 15 services
(cd ledger-core && cargo test)   # Rust hot path
docker compose -f deploy/docker-compose.yml up   # full stack
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

## Honest caveats (v2 scope)

- Flutter member app and React web portal are not in this repo yet;
  API contracts are ready in `api/openapi.yaml`.
- Temporal durable-worker binding wraps the transfer/benefit state
  machines next (state machines are complete and tested).
- Remaining Postgres repos: reporting/statement/transfer persistence.

See `docs/TRACEABILITY.md` for requirement-ID → code mapping and
`docs/COMPLETION.md` for the completion score.
