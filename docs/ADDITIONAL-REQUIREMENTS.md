# Additional Business & Technical Requirements — PenCom/PRA 2014 Gap Analysis

**Version:** 1.0 (Tranche 4) — **Date:** 2026-11-16
**Sources:** https://www.pencom.gov.ng/pension-fund-administrators/, PRA 2014 (ss.2,4,7,8,10,11,13,51,55-91), Revised Regulation on Investment of Pension Fund Assets (Sept 2025), Revised Regulation for the Transfer of RSA (Feb 2024), Guidelines for Micro Pension Plan, Guidelines on Voluntary Contributions (rev. June 2024), PenCom circulars 2025-2026 (recapitalisation, BVN mandate, I-Am-Alive, PSSP remittance, delegated benefit approvals).

This document extends the base specification (PFA-as-a-Service-Specification) with requirements derived from direct analysis of the PenCom PFA framework. IDs continue existing schemes (BR-, FR-, REG-, SCM-, NFR-). Each item maps to a primary regulation.

---

## 1. Business Requirements

| ID | Requirement | Source | Priority |
|----|-------------|--------|----------|
| BR-11 | **Late-remittance penalty recovery.** Employers must remit within 7 working days of salary payment; penalty = >=2% of unpaid contributions per month or part thereof, recoverable as debt and credited to the employee RSA (not PFA revenue). | PRA 2014 s.11(3),(6),(7) | P0 |
| BR-12 | **14-day employer-default reporting.** PFA must report to PenCom any employer whose remittance remains due >14 days. | PRA 2014 s.69(d) | P0 |
| BR-13 | **PFA fee engine.** Admin fee cap N100/month per active RSA holder (N50 retiree), deducted pre-unitization; asset-based fees accrued daily on portfolio valuation, charged within 5 working days of month end; no fees after 3 consecutive months without contributions; per-fund rate schedule (Fund I 1.755% total, II 1.65%, III 1.50%, IV/VI-R income-based 7.5%, VI-A 1.65%). Micro-pension: N80/month if monthly contributions >= N4,000 else N20; zero if no contribution. | PenCom Regulation on Fees Structure + circulars | P0 |
| BR-14 | **Seven-fund multi-fund structure (2025).** Fund I (opt-in <=49), II (default <=49), III (default >=50), IV (retirees), V-Conservative & V-Growth (micro/PPP), VI Active/Retiree (non-interest), VII (USD, diaspora/FX earners). Age assignment keyed to age at last birthday. | Revised Investment Regulation 2025 s.8 | P0 |
| BR-15 | **Fund movement rules.** One free move per 12 months; additional moves charged (fee deducted from RSA); III->I prohibited; II->III prohibited (except return path for members previously moved III->II); V<->V switch by request; VI moves by consent form. | Rev. Inv. Reg. 2025 s.8.3-8.5 | P0 |
| BR-16 | **Variable-income exposure caps (2025).** Fund I 95%, II 75%, III 20%, IV 10%, V-Conservative 5% (supersede 2018 75/55/20/10). Investment compliance engine must flag/block breaches. | Rev. Inv. Reg. 2025 s.8.1.1(v) | P1 |
| BR-17 | **Micro-pension / Personal Pension Plan.** Eligibility age >=18 informal sector/self-employed/<3-employee orgs; flexible daily/weekly/ad-hoc contributions; each contribution split 40% contingent / 60% retirement; contingent access first at 3 months, then once weekly, payout <=2 working days; retirement at >=50 by declaration; auto-conversion to CPS after 1 month of formal-sector remittances; mandatory->micro conversion prohibited. | Micro Pension Guidelines 6.x / PPP Guidelines Oct 2025 | P0 |
| BR-18 | **Voluntary Contributions (2024 revision).** VC split 50% contingent / 50% fixed; contingent accessible after 1 year; income taxable if withdrawn <5 years (PFC remits WHT within 21 days); single lodgments >=N5m reported to EFCC; monthly VC reporting to PenCom. | VC Guidelines rev. June 2024; PRA s.10(4) | P0 |
| BR-19 | **Benefit access rules.** Retirement at later of retirement or age 50; lump sum 25-50% via SRBC subject to residual pension >= 50% of final salary; en-bloc where monthly pension < 1/3 of national minimum wage (N70,000 => N23,333.33); 25% job-loss withdrawal if <50, disengaged and unemployed >=4 months; programmed withdrawal vs retiree annuity (annuity->PW barred, PW->annuity allowed). | PRA s.7; Revised Reg. on Administration of Retirement & Terminal Benefits 2022 | P0 |
| BR-20 | **Death benefits.** RSA balance to beneficiaries under valid Will admitted to probate or Letter of Administration; docs: death certificate, burial warrant, NOK KYC. | PRA s.8 | P1 |
| BR-21 | **Mortgage equity contribution.** Up to 25% of RSA balance as residential mortgage equity via CBN-licensed mortgage lender (exception to s.89 prohibition). | PRA s.89(2) + guidelines | P1 |
| BR-22 | **Foreign-currency RSA (Fund VII).** USD contributions via Non-Resident Nigerian Ordinary Account; 60% retirement / 40% contingent split; withdrawals max 2x/year, first only after 6 months; contributions >$10,000 reported to NFIU within 24 hours; uncreditable funds returned within 48 hours; tax-free if held >=5 years else PFC remits tax within 21 days. | PenCom FCY framework Sept 2025 | P1 |
| BR-23 | **Approved Existing Schemes (AES) support.** Pre-June-2004 schemes: fully funded, segregated assets, licensed custodian + PFA, non-unitised funds with TWR performance reporting, income-based 7.5% fees. | PRA s.51 | P2 |
| BR-24 | **Additional Benefit Scheme (ABS).** Employer top-up schemes: Trust Deed approved by PenCom, annual actuarial valuations, consolidated monthly valuation reporting. | PenCom ABS circular | P2 |
| BR-25 | **PFA recapitalisation monitoring.** Minimum capital N20bn (N20bn + 1% of AUM above N500bn for large PFAs); tracked vs audited shareholders' funds; compliance deadline June 2027; shortfall cure 90 days. Platform should track tenant capital adequacy. | PenCom/INSP/Surv/2025/1255 | P2 |
| BR-26 | **Group Life Insurance evidence.** Employer must maintain GLIP >= 3x annual total emolument; evidence required for PenCom Pension Clearance Certificate. | PRA s.4(5) | P1 |
| BR-27 | **Nominal RSA.** If employee has not opened RSA within 6 months of duty assumption, employer requests PFA to open a nominal RSA. | PRA s.11(5) | P1 |
| BR-28 | **NSSITF legacy recognition.** Pre-2004 NSITF contributions recognized as distinct RSA benefit type. | PRA transitional provisions | P2 |

## 2. Technical / Functional Requirements

| ID | Requirement | Source | Priority |
|----|-------------|--------|----------|
| FR-501 | **Penalty accrual engine.** Compute per-employer late penalty: 2% x unpaid principal x months (or part thereof) overdue from the 7-working-day deadline; post penalty receivable as member-RSA credit (double-entry: Dr employer-penalty-receivable / Cr member RSA); expose aging and PenCom-reportable (>14 days) set. | PRA s.11(6)-(7), s.69(d) | P0 |
| FR-502 | **Fee accrual engine.** Daily asset-based accrual per fund rate table; monthly N100/N50 admin fee pre-unitization; 3-month inactivity fee holiday; micro-pension tiered fee; fee postings to ledger; tenant-configurable rate packs. | Fees Regulation | P0 |
| FR-503 | **Fund assignment & movement engine.** Assign default fund by age at last birthday; validate movement requests (12-month rule, prohibited paths, retiree gating); price movements at same-day unit price (balance-neutral); ledger entries for unit redemption/purchase. | Rev. Inv. Reg. s.8 | P0 |
| FR-504 | **Micro-pension service.** Contribution intake (any amount/frequency), 40/60 split into contingent/retirement sub-balances, weekly contingent withdrawal with 3-month seasoning and 2-day payout SLA, conversion workflow to CPS fund. | Micro/PPP Guidelines | P0 |
| FR-505 | **Voluntary contribution service.** 50/50 contingent/fixed split, 1-year contingent lock, WHT computation on income when held <5 years, N5m EFCC reporting queue, monthly PenCom VC return. | VC Guidelines 2024 | P0 |
| FR-506 | **Benefit computation (SRBC).** Implement PenCom Standard Retirement Benefit Computation: inputs age, gender, final salary, RSA balance; lump sum bounded 25-50% with residual-pension >= 50% final-salary test; en-bloc threshold N23,333.33/month; programmed-withdrawal schedule by expected lifespan. | 2022 Benefits Regulation | P0 |
| FR-507 | **Job-loss 25% withdrawal.** Eligibility: age <50, disengagement evidence, >=4 months continuous unemployment; one-time; block second access until retirement. | PRA s.7(2) | P0 |
| FR-508 | **Fund VII / FCY service.** USD-denominated sub-ledger (integer cents — DA-03 analog), 60/40 split, 2-withdrawal/year cap with 6-month seasoning, NFIU >$10k report queue, 48-hour return of uncreditable items. | FCY framework 2025 | P1 |
| FR-509 | **RTS transfer ETD correction.** Requests received in 3rd month of a quarter roll to next quarter's Effective Transfer Date (31 Mar/30 Jun/30 Sep/31 Dec); compute Global Net Transfer Position per counterparty PFA for settlement; 365-day re-transfer gate; transfers free of charge; NIMC biometric/vNIN precondition flag. | RSA Transfer Reg. Feb 2024 s.3.1 | P0 |
| FR-510 | **Investment compliance engine.** Per-fund variable-income cap check; per-asset-class limits (global/per-issuer/per-issue); rating gates ('A' min, 20% BBB basket, 10-working-day downgrade exit); prohibited-transaction screen (related parties, margin, borrowing except repo/sec-lending). | Rev. Inv. Reg. ss.3,6,9 | P1 |
| FR-511 | **Regulatory returns.** Daily valuation report; monthly/quarterly RMAS operational returns; 36-month rolling annualized return + Sharpe (risk-free = 3-yr avg 10-yr FGN yield) published by 10th monthly; quarterly RSA statements; AES TWR reporting. | Inv. Reg. s.11 + circulars | P1 |
| FR-512 | **Death-benefit workflow.** Claim intake with Will/LoA validation, NOK KYC, probate status, maker-checker payout, audit trail. | PRA s.8 | P1 |
| FR-513 | **I-Am-Alive 2026 alignment.** Annual cycle for all CPS retirees on monthly pensions from 1 Dec 2026; digital + physical channels; recordings retained >=5 years; 12-month + 3-month grace -> suspension + NOK notification; spot-check support. (Existing retiree service covers; add NOK notification + evidence retention.) | PenCom circular 3 Jul 2026 | P0 |
| FR-514 | **BVN/NIN mandatory validation.** BVN mandatory for registration and recapture (from 1 Feb 2025); 11-digit checksum validation; NIMC fingerprint/iris/vNIN match precondition for transfers and benefit processing. | PenCom circular 9 Jan 2025 | P0 |
| FR-515 | **GLIP tracking.** Employer record of group life policy (insurer, policy no., cover multiple >=3x emolument, expiry); clearance-certificate evidence bundle. | PRA s.4(5) | P1 |
| FR-516 | **Nominal RSA workflow.** Employer-initiated nominal RSA after 6 months; member claim/merge flow when member later registers. | PRA s.11(5) | P1 |
| FR-517 | **Tenant capital adequacy dashboard.** Tenant (PFA) capital vs AUM threshold tracking, 90-day cure countdown, alerts. | Circular 2025/1255 | P2 |

## 3. Non-Functional / Compliance Additions

| ID | Requirement |
|----|-------------|
| NFR-20 | All new monetary flows reuse integer-kobo rule (DA-03); USD flows use integer cents with dedicated currency tag — never floats. |
| NFR-21 | All regulator-facing outputs (returns, penalty reports, NFIU/EFCC queues) are derivable from the WORM audit chain and reproducible via ledger replay. |
| NFR-22 | Every new rule parameter (rates, thresholds, deadlines) lives in `rules/ng/pencom.json` (or new packs) — no hard-coded regulatory constants in service code. |
| NFR-23 | Penalty/fee computations must be deterministic and replay-safe; Temporal workflows used for multi-day SLAs (2-day benefit payout, 48-hr FCY return, 90-day capital cure). |

## 4. Tranche 4 Implementation Scope (this delivery)

Implemented in code (Tranche 4):
- FR-501 penalty engine (`internal/penalty`) + BR-11/BR-12 (14-day report set)
- FR-502 fee engine (`internal/fees`) + BR-13 incl. micro-pension tiers
- FR-503 fund assignment/movement (`internal/fund`) + BR-14/BR-15/BR-16 caps in rules pack
- FR-504 micro-pension (`internal/micropension`) + BR-17
- FR-505 voluntary contributions (`internal/vc`) + BR-18
- FR-506/FR-507 benefit computation: SRBC lump sum, en-bloc threshold, job-loss 25% (`internal/benefit` extensions)
- FR-508 Fund VII FCY (`internal/fcy`) + BR-22
- FR-509 RTS ETD correction + GNTP netting (`internal/transfer` extensions)
- FR-513 NOK notification + 5-year evidence retention fields (`internal/retiree` extension)
- Rules pack `rules/ng/pencom.json` v2: 2025 fund table, fee rates, penalty rate, SRBC bounds, en-bloc threshold, VC/micro splits — with golden tests.

Deferred to Tranche 5+: FR-510 investment compliance, FR-511 full RMAS returns, FR-512 death workflow, FR-515/516 GLIP/nominal RSA, FR-517 capital dashboard, BR-23/24 AES/ABS modules.
