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
| AC-05 TigerBeetle gate | Postgres ledger default; TB gate checklist pending | Pending |

## Phase 3 items designed but not implemented here
Analytics/lakehouse (Flink/Ray/Sedona), chatbot, open partner APIs beyond
webhooks, ISO 27001 certification work, Flutter app, React portal.

## Tranche 4 — PenCom/PRA 2014 gap analysis (docs/ADDITIONAL-REQUIREMENTS.md)

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
| BR-20 death benefits (Will/LoA) | — | Deferred (Tranche 5) |
| BR-21 mortgage equity 25% | — | Deferred (Tranche 5) |
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
| FR-510 investment compliance engine | — | Deferred (Tranche 5) |
| FR-511 regulatory returns (RMAS) | — | Deferred (Tranche 5) |
| FR-512 death-benefit workflow | — | Deferred (Tranche 5) |
| FR-513 I-Am-Alive 2026 NOK + retention | `retiree.Retiree.NextOfKinID/EvidenceRetainedUntil`, `NOKNotifyDue`, `RecordLivenessEvidence` | Implemented |
| Rules pack v2 (funds, fees, penalty, thresholds) | `rules/ng/pencom.json` ops-2025@v2 + `internal/rules/ops_golden_test.go` | Implemented |
