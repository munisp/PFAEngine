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
