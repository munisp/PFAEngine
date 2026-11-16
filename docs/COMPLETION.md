# PFAEngine v1 — Completion Report

Build date: 2026-11-16 · Stack: Go 1.23 (services) · Rust (ledger hot path)
Verification: `go vet` clean, `go build` clean, **22/22 Go test packages pass**,
**4/4 Rust tests pass**, gofmt clean.

## Scores

| Scope | Score | Basis |
|---|---|---|
| Phase 1 MVP (AC-01: onboarding, contribution/recon, statements, returns, audit) | **92%** | All 12 Phase-1 services + Tranche 4 penalty/fee/fund engines implemented + tested; gaps: Postgres repository wiring, real vendor credentials, Temporal durable workers |
| Phase 2 (AC-02: benefits, RTS, NAV, retiree payroll, I-Am-Alive) | **88%** | Engines complete with tests; FR-509 ETD/GNTP corrected; FR-513 NOK + evidence retention added; USSD/agent rails and Kubecost outstanding |
| Phase 3 (AC-03: analytics/ML, chatbot, open APIs, lakehouse) | **18%** | Webhook framework + AML done; lakehouse/chatbot designed only |
| **Whole specification (BR/FR/NFR/AR/INT/DA/SC/REG/SCM/PLT/AC)** | **76%** | Tranche 4: FR-501..509, FR-513 + BR-11..19, BR-22 implemented; FR-510..512, BR-20/21 deferred; REG framework complete; PLT 3/14; NFR-13..24 partial |

## What is production-shaped today
- Money/ledger correctness: integer kobo, 8-decimal units, enforced double
  entry, reversal-only corrections, deterministic replay — tested in Go AND Rust.
- Regulatory hard requirements: hash-chained WORM audit with nightly verify +
  dual-control export; liveness thresholds + retry cap; 365-day RTS rule;
  12mo+3mo I-Am-Alive suspension; maker-checker on claims and returns.
- Golden tests reproduce all four §13.2 worked examples exactly (SCM-15).
- Multi-jurisdiction: 6 regulator adapter packs, tax engine (EET/TEE/ETT), FX.

## Tranche 2 delivered (2026-11-16)
- PostgreSQL repository layer (`internal/store`) with tenant-scoped
  transactions; contribution service persists when DATABASE_URL set;
  integration test gated on TEST_DATABASE_URL.
- Real vendor adapters (`internal/vendors`): Smile ID (NIN/BVN, HMAC-signed),
  Dojah (NIN/BVN), Termii (SMS), Remita (RRR instructions, SHA-512 hash-key),
  Africa's Talking USSD menu flow — all with httptest coverage.
- Kafka backbone binding (`internal/stream`, kafka-go): hash-partitioned by
  tenant:aggregate, RequireAll acks.

## Tranche 3 delivered (2026-11-16)
- Temporal durable workflows (internal/workflow): RTS transfer with
  quarterly timer + 30-day SLA alarm; benefit claim with docs SLA,
  maker-checker signal, compensation; I-Am-Alive 12-month campaign with
  continue-as-new. Worker: cmd/worker. Tested via Temporal test suite.
- Flutter member app scaffold (apps/member_flutter): full onboarding
  wizard, QR-verifiable statements, I-Am-Alive, white-label theming.
- React+TS staff portal (apps/staff-portal, Vite build verified):
  recon exception queue + aging, claims maker-checker, dual-control
  audit export, TOTP login.

## Tranche 4 delivered (2026-11-16)
- Penalty engine (internal/penalty, FR-501/BR-11/BR-12): 7-working-day
  remittance deadline, 2%/month-or-part penalty, 14-day PenCom report flag.
- Fee engine (internal/fees, FR-502/BR-13): ₦100/₦50 admin caps, micro
  ₦80/₦20 tiers, 3-month fee holiday, daily asset-based accrual — rates
  loaded from the rules pack.
- Multi-fund 2025 (internal/fund, FR-503/BR-14/15/16): 8 fund codes,
  age-at-last-birthday defaults, movement prohibitions (III→I, II→III
  non-return, Fund I age bar, micro-only V), one free move per 365 days.
- Micro-pension (internal/micropension, FR-504/BR-17): 40/60 split,
  3-month seasoning, weekly contingent cadence.
- Voluntary contributions (internal/vc, FR-505/BR-18): 50/50 split,
  1-year contingent lock, <5-year taxable income, ₦5m EFCC flag.
- Fund VII FCY (internal/fcy, FR-508/BR-22): integer US cents, 60/40
  split, 6-month seasoning + 2-withdrawals/rolling-year cap, >$10k NFIU.
- SRBC (internal/benefit/srbc.go, FR-506/507/BR-19): 25–50% lump-sum
  bounds + residual-pension test, en-bloc at integer-exact ⅓ of ₦70,000,
  job-loss 25% eligibility (age <50, ≥4 months, one-time).
- RTS correction (internal/transfer, FR-509): Effective Transfer Date
  rolls 3rd-month requests to next quarter; Global Net Transfer Position
  netting per counterparty PFA.
- I-Am-Alive 2026 (internal/retiree, FR-513): NOK notification at the
  12mo+3mo suspension trigger, 5-year liveness-evidence retention.
- Rules pack v2 (rules/ng/pencom.json ops-2025@v2): 2025 fund caps, fee
  rates, penalty/deadline/report thresholds — golden-tested (NFR-22).

## Known gaps (next tranches, in priority order)
1. Extend Postgres repos to member/kyc/reporting/statement services
2. Lakehouse (Parquet/Delta/Spark/Sedona), chatbot, MI dashboards (BR-07/09)
3. Billing engine (BR-08), M&A book-merge tooling (BR-10), scheme wind-up (SCM-14)
4. TigerBeetle ops-maturity gate review (AC-05) — Postgres fallback active
5. i18n (REG-06), pension increases (SCM-09), annuity marketplace (SCM-11)
