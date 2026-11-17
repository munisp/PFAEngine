# Payroll Provider Integration (Gap 3)

New package `internal/payroll` adds first-class payroll-provider depth beyond
CSV remittance upload (`internal/employer`):

- `payroll.go` — domain types (`Employee`, `Contribution`, `Schedule`),
  YYYY-MM period validation, statutory PRA 2014 s.4(1) split
  (`PensionSplit`: employee 8% / employer 10% of pensionable base, integer
  kobo, round-half-up — no floats).
- `erpnext.go` — ERPNext / Frappe HR REST client (token auth).
- `pssp.go` — PSSP remittance-schedule CSV layouts (Pensphere and
  Interswitch Pension Central style), write + parse, line-numbered errors.
- `payroll_test.go` — httptest + CSV round-trip tests.

Imports `internal/money` only — no import cycles. No existing files touched.

## Suggested routes (cmd/member-svc, not yet wired)

| Route | Behaviour |
|---|---|
| `POST /v1/payroll/erpnext/sync` | Body `{"payroll_entry_id":"HR-PR-..."}`; builds client via `NewERPNextFromEnv()` (503 when ok=false), calls `FetchPayrollEntry`, persists the resulting `Schedule`. |
| `POST /v1/payroll/schedules/{id}/push` | Loads a stored schedule, calls `PushContributionSchedule`, stores the returned Frappe document name. |
| `POST /v1/payroll/pssp/import` | Multipart CSV upload + `format=pensphere\|pension-central` query param; `ParsePensphere` / `ParsePensionCentral` (400 on line-numbered parse errors). |
| `GET /v1/payroll/pssp/export?format=pensphere\|pension-central` | Loads a schedule, streams `FormatPensphere` / `FormatPensionCentral` as `text/csv`. |

## Environment variables

| Var | Purpose |
|---|---|
| `ERPNEXT_BASE_URL` | Frappe site base URL, e.g. `https://erp.acme.ng` (trailing slash trimmed). |
| `ERPNEXT_API_KEY` | Frappe API key (token auth: `Authorization: token key:secret`). |
| `ERPNEXT_API_SECRET` | Frappe API secret. |
| `ERPNEXT_SCHEDULE_DOCTYPE` | Doctype used by `PushContributionSchedule`; default `Contribution Schedule` (set `Journal Entry` for a JE-style draft). |

`NewERPNextFromEnv()` returns `ok=false` when any of the first three is
missing — handlers should treat that as "integration disabled".

## ERPNext mapping notes

- `FetchPayrollEntry` reads `Payroll Entry/{id}`, lists `Salary Slip`
  documents filtered on `payroll_entry`, then fetches each slip.
- Pensionable base = sum of earnings whose component name contains
  basic / housing / transport (case-insensitive); falls back to
  `gross_pay` when no structured earnings exist.
- RSA PIN is read from `custom_rsa_pin` on the Salary Slip (Frappe site
  customisation; empty when the field is absent).
- Period comes from the entry's `posting_date` (fallback `start_date`).
- Fail-closed: any HTTP >= 300 or decode error aborts the whole fetch/push;
  no partial schedules.

## CSV layout decisions

- Amounts in the files are NAIRA with 2dp; converted via
  `money.ParseKobo` / `Kobo.Naira()` so write→parse round-trips exactly in
  kobo.
- `Contribution` carries no name/salary columns, so writers reconstruct:
  Pensphere `MONTHLY_SALARY` and Pension Central `BASIC` are derived from
  the 8% employee share (`derivePensionBase`, exact for values produced by
  `PensionSplit` on whole-naira bases); `HOUSING`/`TRANSPORT` are written
  `0.00`; name columns are blank. Enrich at the handler layer if `Employee`
  records are available.
- Pensphere has no schedule-ID column; parse synthesises
  `ID = EMPLOYER_CODE + "/" + PERIOD`. `EmployeeID` is set to the PIN (only
  stable key in that layout).
- Pension Central writer recomputes `PENSION_EE`/`PENSION_ER` with
  `PensionSplit` (8%/10% of basic+housing+transport); the parser trusts the
  given columns.
- Pensphere parser enforces `TOTAL == EMPLOYEE_CONTRIB + EMPLOYER_CONTRIB`.
  Both parsers reject negative amounts and zero/negative line totals, and
  report all row problems as `line N:` errors (header = line 1).

## Why ERPNext / Frappe HR (not Odoo / HR.my)

1. Fully open source (GPLv3) — self-hostable, no per-employee SaaS fee.
2. REST-first API (token auth, JSON) maps cleanly onto our HTTP handlers.
3. Payroll Entry → Salary Slip model mirrors PRA 2014 contribution runs.
4. Large Nigerian/African SME footprint; local partners and Frappe Cloud.
5. Frappe's custom-doctype model lets us add a Contribution Schedule doc
   without forking core payroll.
