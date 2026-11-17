# T21 — PenCom-parity regulatory surface

Gap analysis against https://www.pencom.gov.ng/pension-fund-administrators/
identified 8 features a PenCom-licensed PFA site must offer that the
platform was missing. All 8 are now implemented.

| # | Feature | Package / routes | Notes |
|---|---------|------------------|-------|
| 1 | Forms library (downloads) | `internal/docslib` · `GET /api/v1/forms`, `POST /forms/publish`, `GET /forms/{id}`, `GET /forms/versions` | 9 standard PenCom forms seeded per tenant (RSA-REG-01 etc.); versioned publish deactivates prior versions |
| 2 | KYC document upload | `internal/docupload` · `POST /documents`, `GET /documents/mine`, `GET /documents/pending`, `POST /documents/{id}/review` | 1B–10MB, 6 document kinds; every submission opens a `KYC_REVIEW` case; review resolves the linked case |
| 3 | Branch locator + appointment booking | `internal/branch` · `GET/POST /branches`, `GET /branches/{id}/availability`, `POST /appointments`, `GET /appointments`, `POST /appointments/{id}/transition` | Slot engine (30 min, 14/day defaults), no double-booking, BOOKED→CHECKED_IN→DONE / CANCELLED / NO_SHOW |
| 4 | Fund performance publication | `internal/funds/performance.go` · `POST /funds/{id}/nav`, `GET /funds/{id}/nav/series`, `GET /funds/performance` | Compounded 3m/6m/12m/36m/YTD returns in **bps** (integer kobo prices, no floats); NAV series ascending |
| 5 | Statutory disclosures | `internal/disclosure` · `GET /disclosures`, `POST /disclosures` | WORM: duplicate kind+period rejected (409) — audited RSA fund FS, audited company FS, annual reports, quarterly returns |
| 6 | News / education CMS | `internal/news` · `GET /news`, `GET /news/drafts`, `POST /news`, `POST /news/{id}/publish`, `POST /news/{id}/archive` | DRAFT→PUBLISHED→ARCHIVED, pinned-first feed, 4 categories incl. PENCOM_NOTICE |
| 7 | CRM relationship manager | `internal/crm` `AssignRM`/`RM` | Member 360 carries `relationship_manager`; RM assignment audited |
| 8 | Tenancy licence profile (PRA s.60) | `internal/tenancy` · `GET/PUT /api/v1/tenancy/license` | PenCom licence number, class (PFA/PFC/CPFA), status, issue/expiry dates |

## Architecture notes
- All engines are in-memory cores following the platform pattern (write-through repos can be added behind the existing `store` layer).
- `docupload` is wired into `casemgmt`: submission opens a `KYC_REVIEW` case (`NoDedup`), review resolves it with code `VERIFIED`/`REJECTED`.
- Fund performance is derived from the published NAV series — no separate
  analytics store. `Performance()` returns pointer fields; absent windows
  (insufficient history) are omitted from JSON.
- `funds.Registry.SeedStandard` registers the six PenCom multi-fund RSA
  funds (FUND_I..FUND_VI) per tenant at boot.

## Tests
- Package tests: `internal/docslib`, `internal/docupload`, `internal/branch`, `internal/disclosure`, `internal/news`, `internal/funds` (performance windows).
- End-to-end HTTP: `cmd/member-svc/regulatory_test.go::TestRegulatoryEndToEnd` — forms seeding, doc upload→review, branch booking + double-book rejection, WORM disclosure 409, news lifecycle, NAV publish → performance, licence profile get/put.

## Honest caveats
- Engines are in-memory; restart loses forms/news/appointments. Postgres write-through (as done for casemgmt in T20) is the follow-up if persistence is required before pilot.
- Document blobs are referenced (`ref`) not stored — plug object storage (S3/MinIO) at the upload edge.
- Branch availability has no holiday calendar; slots are uniform per day.
