# Gap 10 — Pricing Product Surface & Distribution Partner Network

Competitive analysis vs Nigerian PFAs (Stanbic IBTC, ARM, Leadway) flagged
two missing commercial surfaces: **transparent published pricing** and a
**distribution partner network**. This document covers both.

## 1. Pricing (`internal/pricing`, `/api/v1/pricing/*`)

### Tier catalog

Four published tiers, code-seeded and overridable per deployment
(`UpsertTier`, persisted as `pricing.tier` snapshots):

| Tier       | Per member / month | AUM fee (annual) | Included members | Overage / member / month |
|------------|--------------------|------------------|------------------|--------------------------|
| STARTER    | ₦50.00             | 3 bps            | 500              | ₦60.00                   |
| GROWTH     | ₦45.00             | 2 bps            | 5,000            | ₦50.00                   |
| SCALE      | ₦40.00             | 1 bp             | 25,000           | ₦45.00                   |
| ENTERPRISE | ₦35.00             | 1 bp             | 100,000          | ₦40.00                   |

Every quote combines three integer components:

1. **Per-active-member charge** — `per_member_monthly × active_members`.
2. **Overage charge** — `overage_per_member × max(0, active − included)`.
   The included-member cap nudges growing tenants toward the next tier.
3. **AUM platform fee** — `aum_kobo / 12 × aum_bps / 10000` (monthly share
   of the annual bps rate, integer floor).

`Quote()` returns `[]billing.Line` — the same line-item model
`internal/billing` invoices use — so quotes feed invoices without forking
the invoice model. All arithmetic is integer kobo; rates are integer basis
points (DA-03, no floats).

### Benchmarking rationale

- **Per-member-month** is the SaaS-normal analogue of the per-account admin
  fees Nigerian PFAs historically charged (the old ₦100/account/month
  administrative-fee model PenCom sanctioned pre-2017). ₦35–₦50/member/month
  prices the *platform* at roughly half the legacy per-account fee a PFA
  used to collect — an easy cost-displacement story.
- **AUM bps** mirrors how PFAs themselves earn: PenCom caps the fund-
  management + admin fee charged to RSA holders (fund-dependent, on the
  order of ~1.5% p.a. for Fund I, lower for later funds). Our platform AUM
  fee is 1–3 bps p.a. — roughly 1% of what the PFA earns on the same AUM —
  deliberately far inside any PenCom fee-cap sensitivity because it is a
  **vendor fee paid by the PFA**, not a fee charged to RSA holders. It is
  NOT levied on member balances; it is platform pricing to the tenant.
- PenCom fee-cap awareness matters for positioning: tenants cannot pass
  platform costs onto RSA holders beyond regulated caps, so predictable,
  published, per-member pricing is itself the competitive feature.

### Plan assignment

`SetPlan` assigns a tier effective-dated (`YYYY-MM-DD`), append-only with
audit fields (`set_by`, `set_at`); `GetPlan(tenant, date)` resolves the
plan in force on any date so quotes for past periods stay reproducible.
Tenants with no assignment quote on the default tier (STARTER) with
`assumed: true`. `PUT /api/v1/pricing/plan` is staff-gated
(`X-Actor-Role: STAFF|ADMIN`).

## 2. Partner network (`internal/partners`, `/api/v1/partners/*`)

### Why a third channel

| Surface | Who | Mechanism |
|---|---|---|
| `internal/agents` | Individual field agents (person-level) | Accreditation, micro-pension enrolment, per-person commission ledger |
| `internal/embedded` | Programmatic integrators | API keys, scopes, webhooks — embedded finance |
| **`internal/partners`** | **Institutions: banks, telcos, fintechs, aggregators** | **Referral-code attribution + revenue-share ledger + settlement** |

Partners are the Stanbic-style distribution play: a bank or telco refers
its customer base into the tenant's PFA and earns a basis-point share of
attributed contributions.

### Lifecycle & attribution

`Onboard` (PENDING) → `Approve` (ACTIVE) → `Suspend` (SUSPENDED, blocks
new attributions AND accrual) → `Approve` reinstates. Every transition
records actor/timestamp/reason.

`AttributeJoin(tenant, member, referralCode)` binds a member to the ACTIVE
partner owning the referral code. It is **idempotent and first-write-wins**:
a member's first successful attribution is permanent; retries return the
original record, and later codes never rebind — matching how referral
attribution must behave for audit defensibility. Referral codes are
uppercase slugs, unique per tenant, reusable across tenants.

### Revenue share & settlement

`AccrueContribution(tenant, member, contributionKobo, period)` accrues
`contribution × rev_share_bps / 10000` (integer kobo, floor rounding) to
the partner ledger — only for attributed members whose partner is ACTIVE.
Unattributed members accrue nothing (`attributed=false`, no error).
`SettlementReport(tenant, partner, period)` itemises the period's accruals
with a total; `PartnerBalance` gives the lifetime accrued liability. The
intended workflow:

1. Contribution posting calls the accrual hook (`POST
   /api/v1/partners/accruals`, staff/system).
2. Month-end ops pulls `GET /api/v1/partners/{id}/settlement?period=`.
3. Finance pays out off-platform; the report is the audit record. (No
   payout rail is integrated — see caveats.)

### Persistence

All three engines write through to the snapshot sink (`pricing.tier`,
`pricing.assignment`, `partners.partner`, `partners.attribution`,
`partners.accrual`) and rehydrate via `Restore` with ID-sequence
continuation — same NFR-10 pattern as branch/ekyc/news.

## 3. Honest caveats

- **Benchmark numbers are a list-of-1 assumption set.** The tier prices
  above are seeded defaults derived from public Nigerian PFA fee norms and
  typical SaaS per-seat pricing — not from a priced deal. Override per
  deployment via `UpsertTier` once real contracts exist.
- **No live billing-provider integration.** Quotes and billing invoices are
  computed in-platform; there is no payment-gateway (Paystack/Flutterwave)
  collection path, no dunning automation beyond `billing.Status`, and no
  automated partner payout rail — settlement reports are records, payouts
  are off-platform.
- **AUM is caller-supplied.** `Quote` trusts the `aum_kobo` argument;
  wiring it to custody-reconciled AUM is a follow-up.
- **Rev-share base is contributions, not fees.** Accrual is computed on
  gross attributed contributions; if the commercial deal is "share of fees
  earned", compute the fee base before calling the accrual hook.
- **AuthZ is header-based** (`X-Actor-Role`) per the existing dev-mode
  convention — production must enforce this at the gateway/IAM layer.
- Attribution first-write-wins means a member referred by two partners
  credits the first; there is no re-attribution or clawback flow yet.
