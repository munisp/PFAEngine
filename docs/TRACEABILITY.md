# Traceability matrix — requirement ID → implementation

| Req | Implementation | Status |
|---|---|---|
| BR-03 ≥95% auto-match | `internal/recon` (test: TestAutoMatch100OnCleanFiles) | Done |
| BR-05 tenant <1 day | `internal/tenancy.Registry.Provision` | Done |
| BR-06 PPP micro | `contribution.Line.IsPPP`, Fund V | Done |
| FR-101 NIN tokenized | `internal/kyc.VerifyNIN` | Done |
| FR-102 BVN salted hash | `internal/kyc.ValidateBVN` | Done |
| FR-103 liveness thresholds 0.80/0.60, cap 3 | `internal/kyc.SubmitLiveness` | Done |
| FR-104 agent-assisted enrollment | consent event w/ attestation (kyc.RecordConsent) | Partial |
| FR-105 cross-tenant dup detection | `kyc.identityIndex` (hash-based) | Done |
| FR-106 NDPA consent lifecycle | `internal/kyc` consents + identity svc endpoints | Done |
| FR-107 PenCom PIN issuance | `internal/member.OpenRSA/AckPIN` | Done |
| FR-108 I-Am-Alive 12+3 | `internal/retiree.Campaign` | Done |
| FR-109 physical verification event | audit + kyc evidence_ref | Partial |
| FR-201 RSA registry + nominees | `internal/member` | Done |
| FR-202/203 employer registry/portal | `cmd/employer-portal` | Done |
| FR-301 8/10 split + voluntary + PPP | `internal/contribution.SplitLine` | Done |
| FR-302 double-entry integer ledger | `internal/ledger`, `ledger-core` (Rust) | Done |
| FR-303 recon ≥95% auto-match, aging, suspense | `internal/recon` | Done |
| FR-304 unitization + daily NAV, funds I–VI | `internal/funds` | Done |
| FR-305 employer-default detection | `recon.DetectDefaults` | Done |
| FR-306 deterministic replay | `ledger.Replay`, audit hash chain | Done |
| FR-401 claims + maker-checker | `internal/benefit` | Done |
| FR-402 RTS 365-day + quarterly cycle | `internal/transfer` | Done |
| FR-403 retiree payroll + liveness suspension | `internal/retiree` | Done |
| FR-501 statements <5s, QR-verifiable | `internal/statement` (PDF + HMAC code) | Done |
| FR-502 statutory returns + sign-off | `internal/reporting` | Done |
| FR-503 templated notifications + audit | `internal/notify` | Done |
| NFR-05/SC-02 hash-chained WORM audit | `internal/audit` | Done |
| NFR-10 per-tenant isolation | `internal/tenancy`, migrations | Done |
| NFR-11 no cloud-proprietary core | OSS stack only (compose) | Done |
| NFR-19 rules externalization | `internal/rules`, `rules/` packs | Done |
| NFR-21 golden tests | `internal/rules/golden_test.go` | Done |
| NFR perf benchmarks (ledger post/balance/replay, recon 10k, CSV 10k) | `internal/ledger/bench_test.go`, `internal/recon/bench_test.go`, `internal/contribution/bench_test.go`, results in `docs/BENCHMARKS.md` | Implemented |
| REG-01 adapter framework | `internal/regx` (NG/GH/KE/ZA/EG + generic) | Done |
| REG-02 multi-currency + FX split | `internal/regx/fx.go` | Done |
| REG-07 tax engine EET/TEE/ETT | `internal/regx/tax.go` | Done |
| SCM-01 scheme typology registry | `internal/scheme` | Done |
| SCM-02 umbrella/multi-employer | `scheme.ParentScheme` sub-accounting | Done |
| SCM-03 lifecycle categories | `member.Category` | Done |
| SCM-04 contribution designs | `scheme.ContributionDesign` | Done |
| SCM-06 DB formula engine | `rules.EvalDBAccrual` + early factors | Done |
| SCM-07 25% pre-retirement access | `benefit.EarlyAccess25` rule | Done |
| SCM-12 Sharia fund | `rules.EvalSharia` golden test | Done |
| SCM-15 rule-version traceability | `rule_version` on claims/returns; golden tests | Done |
| PLT-05 AML/sanctions screening | `internal/aml` (hash lists, PEP, SAR) | Done |
| PLT-12 fraud: identity-reuse alerts | `kyc.identityIndex` cross-tenant | Done |
| INT-01 PenCom RTS/PCRS adapter | `regx.NigeriaAdapter` + `transfer` | Done |
| INT-02..04 aggregator facade + failover | `kyc.Service` (2 providers, queue) | Done |
| INT-09 signed webhooks | `internal/webhk` (HMAC-SHA256) | Done |
| AC-05 TigerBeetle gate | `internal/tbgate` (RequiredCapabilities, Assess, LedgerParity); Postgres ledger remains default until all 6 capabilities pass with evidence | Implemented |

## Tranche 7

| Req | Implementation | Status |
|---|---|---|
| BR-07 lakehouse analytics | `internal/lakehouse` (Event, Sink, MemorySink, ParquetLine JSONL staging) | Implemented |
| BR-09 MI dashboards + member chatbot | `internal/lakehouse` (ContributionsByMonth, EmployerDelinquency, CohortRetention, BuildDashboard); `internal/chatbot` (Classify, Responder, USSDCode) | Implemented |
| BR-10 M&A book merge | `internal/merger` (MergeRoster + ErrPINConflict, PlanMerge, BalanceProof) | Implemented |
| SCM-14 scheme wind-up | `internal/windup` (Advance state machine, DistributionOrder, ProRata exact-sum) | Implemented |

## Phase 3 items designed but not implemented here
ML models on top of the lakehouse, open partner APIs beyond
webhooks, ISO 27001 certification work, Flutter app, React portal.

## Tranches 4–5 — PenCom/PRA 2014 gap analysis (docs/ADDITIONAL-REQUIREMENTS.md)

FR-5xx IDs below are the *additional* scheme (Tranche 4), not the base
spec's FR-501..503.

| Req | Implementation | Status |
|---|---|---|
| BR-11 late-remittance penalty ≥2%/month | `internal/penalty` (Arrears.Penalty) | Implemented |
| BR-12 14-day employer-default report | `penalty.PenComReportable` | Implemented |
| BR-13 fee engine + micro tiers + fee holiday | `internal/fees` (RatePack, AdminFee, AccrueDaily) | Implemented |
| BR-14 seven-fund structure (2025) | `internal/fund` (codes, DefaultFund) | Implemented |
| BR-15 fund movement rules | `fund.ValidateMove` (free move/365d, III→I, II→III return path, Fund I age bar, micro-only V) | Implemented |
| BR-16 variable-income caps 2025 | `rules/ng/pencom.json` funds.variable_income_cap_bps + golden test; engine enforcement in FR-510 | Implemented (rules data) |
| BR-17 micro-pension 40/60 + contingent access | `internal/micropension` | Implemented |
| BR-18 voluntary contributions 50/50, tax, EFCC | `internal/vc` | Implemented |
| BR-19 benefit access rules (SRBC, en-bloc, job loss) | `internal/benefit/srbc.go` | Implemented |
| BR-20 death benefits (Will/LoA) | `internal/death` (Claim, RequiredDocs, maker-checker) + `workflow.DeathClaimWorkflow` | Implemented |
| BR-21 mortgage equity 25% | `benefit.MortgageEquityLimit/ValidateMortgageEquity` | Implemented |
| BR-22 Fund VII FCY USD | `internal/fcy` (Cents, 60/40, 2/yr cap, NFIU) | Implemented |
| FR-501 penalty engine | `internal/penalty` + tests | Implemented |
| FR-502 fee engine | `internal/fees` + tests | Implemented |
| FR-503 fund assignment/movement | `internal/fund` + tests | Implemented |
| FR-504 micro-pension service | `internal/micropension` + tests | Implemented |
| FR-505 voluntary contributions | `internal/vc` + tests | Implemented |
| FR-506 SRBC benefit computation | `benefit.LumpSumBounds/ValidateLumpSum/EnBloc` | Implemented |
| FR-507 job-loss 25% withdrawal | `benefit.JobLossEligible` | Implemented |
| FR-508 Fund VII FCY service | `internal/fcy` + tests | Implemented |
| FR-509 RTS ETD correction + GNTP | `transfer.EffectiveTransferDate`, `transfer.NetPositions`, corrected `SubmitToCycle` | Implemented |
| FR-510 investment compliance engine | `internal/investment` (CheckCompliance, Prohibited, LimitsForFund) | Implemented |
| FR-511 regulatory returns (RMAS) | `internal/returns` (DailyValuation, RollingReturn, Sharpe, TWR, RMASReturn) | Implemented |
| FR-512 death-benefit workflow | `internal/death` + `workflow.DeathClaimWorkflow` (7-day docs SLA, maker-checker, payout, NOK notify) | Implemented |
| FR-513 I-Am-Alive 2026 NOK + retention | `retiree.Retiree.NextOfKinID/EvidenceRetainedUntil`, `NOKNotifyDue`, `RecordLivenessEvidence` | Implemented |
| FR-514 BVN/NIN mandatory format validation | `kyc.ValidateBVNFormat/ValidateNINFormat` wired into `VerifyNIN`/`ValidateBVN` | Implemented |
| FR-515 GLIP tracking + clearance evidence | `internal/employer/glip.go` (GLIP.Valid, ClearanceEvidence) | Implemented |
| FR-516 nominal RSA workflow | `member.NominalDue`, `member.NominalRSA.Claim` | Implemented |
| FR-517 tenant capital adequacy | `internal/capital` (RequiredCapital, Assess 90-day cure) | Implemented |
| BR-23/24 AES/ABS support | `internal/legacy` (Scheme.Compliant, FeeModel, PerformanceMethod) | Implemented |
| BR-25 recapitalisation monitoring | `internal/capital` (₦20bn/NPF ₦30bn, ₦500bn AUM surcharge) | Implemented |
| BR-26 GLIP ≥3x emolument | `employer.GLIP.Valid` | Implemented |
| BR-27 nominal RSA after 6 months | `member.NominalDue` | Implemented |
| BR-28 NSITF legacy credits | `legacy.NSITFCredit` | Implemented |
| BR-16 engine enforcement | `investment.CheckCompliance` (caps from rules pack) | Implemented |
| BR-22 48-hr FCY return SLA | `workflow.FCYReturnWorkflow` + `RaiseFCYSLAAlarm` | Implemented |
| Rules pack v2 (funds, fees, penalty, thresholds) | `rules/ng/pencom.json` ops-2025@v2 + `internal/rules/ops_golden_test.go` | Implemented |

## Tranche 6

| Req | Implementation | Status |
|---|---|---|
| BR-08 SaaS tenant billing | `internal/billing` (Plan, Invoice, dunning Status.Next 15/31/61-day ladder, PlatformAccess) | Implemented |
| REG-06 i18n (en/yo/ha/ig/pcm-NG) | `internal/i18n` (Bundle.T fallback, LoadDir) + `i18n/en.json`, `i18n/pcm-NG.json` | Implemented |
| SCM-09 pension increases | `internal/benefit/increase.go` (ApplyIncrease bps, EligibleIncrease surplus gate + MaxBps cap) | Implemented |
| SCM-11 annuity marketplace | `internal/annuity` (RankQuotes NAICOM/expiry filter, maker-checker Purchase, ValidatePremium) | Implemented |

## Tranche 11

| Req | Implementation | Status |
|---|---|---|
| INT-07 WhatsApp channel (Meta Cloud API) | `internal/vendors/whatsapp.go` (SendTemplate/SendText with Bearer auth, VerifyWebhookSignature HMAC-SHA256 constant-time, VerifyChallenge handshake, ParseInbound); `chatbot.Responder.HandleWhatsApp` bridge; `cmd/notification` `/webhooks/whatsapp` GET/POST gated by the `whatsapp` tenant feature flag; `notify.Service.SendWhatsApp` hook wires WHATSAPP-channel templates to the adapter | Implemented |

## Tranche 13

| Req | Implementation | Status |
|---|---|---|
| FR-520 unified onboarding framework | `internal/onboarding` (Case state machine DRAFT→DOCS_PENDING→KYC_PENDING→APPROVAL_PENDING→ACTIVE/REJECTED/SUSPENDED; per-stakeholder `Pipeline` templates for MEMBER/MICRO_MEMBER/EMPLOYER/RETIREE/STAFF/TENANT/NOK; maker-checker Approve/Reject; audit-chained mutations); `cmd/member-svc` `/api/v1/onboarding*` (KYC facade-backed steps, activation hooks); `cmd/employer-portal` EMPLOYER pipeline gating COMPLIANT (GLIP 3x); `cmd/identity` TENANT (licence + `capital.Assess`) and STAFF (TOTP enroll, login disabled until approval) pipelines; `scripts/e2e-onboarding.sh` | Implemented |

## Tranche 14

| Req | Implementation | Status |
|---|---|---|
| FR-520 onboarding durable persistence | `db/migrations/0003_onboarding.sql` (`onboarding_case`, `onboarding_step`, WORM `onboarding_document` with REVOKE + BEFORE UPDATE/DELETE trigger); `internal/store.OnboardingRepo` (single-`WithTenant` Save, pipeline-ordered Load, filtered List); `internal/onboarding.PersistentStore` (write-through, rollback on repo error, repo-preferred reads, restart hydration); `cmd/member-svc` `DATABASE_URL` wiring | Implemented |
| REG-06 prefs durable persistence | `member_prefs` (PK (tenant_id, member_id), phone index); `internal/store.PrefsRepo` (Set/Get locale, E.164-normalised LinkPhone/MemberForPhone/LocaleForPhone via `prefs.NormalizePhone`); `internal/prefs.PersistentStore` write-through; `cmd/member-svc` `DATABASE_URL` wiring | Implemented |
