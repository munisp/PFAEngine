# Tranche 20 — Unified Case Management Engine & CRM

## Audit verdict (before this tranche)

| Capability | Before T20 | Robust? |
|---|---|---|
| Fraud cases | `fraud.Case` — 3 statuses, no SLA/assignment/comments/tasks | Partial (single domain) |
| Complaints | **None existed** | No |
| Unified case engine | **None** — fraud, claims, recon, onboarding all siloed | No |
| Member-facing case visibility | **None** | No |
| CRM (360, timeline, segments, campaigns) | **None existed** | No |
| PenCom complaints register | **None** | No |

**Answer: NOT robust, NOT integrated.** This tranche closes the gap.

## What was built

### `internal/casemgmt` — unified case engine
- **9 case types**: FRAUD, COMPLAINT, CLAIM, DEATH_CLAIM, SERVICE_REQUEST, KYC_REVIEW, RECON_BREAK, AML_ALERT, GENERAL
- **State machine** (enforced, auditable): OPEN → TRIAGED → IN_PROGRESS → PENDING_MEMBER / PENDING_THIRD_PARTY → ESCALATED → RESOLVED → CLOSED, REOPENED; illegal transitions rejected
- **SLA matrix** per type × priority (P1–P4, e.g. P1 fraud 4h, complaints 48h–20d per PenCom complaint-handling expectations), `due_at` clock, `Sweep()` breach detection (fires once), manual + breach escalation
- **Queues + round-robin auto-assignment** (FRAUD_OPS, MEMBER_SERVICES, BENEFITS, COMPLIANCE, OPERATIONS); roster configurable via API
- **Dedup/merge**: same member + type + open case ⇒ report merges as a comment, no duplicate investigation
- **Comments, checklist tasks (resolution blocked until all done), attachments, case linking** (fraud rings), **CSAT 1–5** after resolution
- **Append-only event log** per case + `OnEvent` hook for notification fan-out
- **Stats**: open/breached/aging buckets/per-queue/per-type, avg resolution hours, CSAT average
- **Cross-domain helpers**: `OpenFraudAlert` (score→priority mapping), `OpenClaimCase`, `OpenReconBreak`, `OpenComplaint`

### `internal/crm` — CRM layer
- **Member 360**: facts (age, balance, tenure, micro-pension, diaspora) + segments + open/recent cases + timeline + CSAT + complaint counts — composed on demand via provider interfaces (no import cycles)
- **Interaction timeline**: omnichannel (WHATSAPP/USSD/WEB/MOBILE/EMAIL/CALL/BRANCH/AGENT/SMS), inbound/outbound, case-linked
- **Rule-based segmentation**: DORMANT, HIGH_VALUE (≥₦25m), MICRO_PENSION, DIASPORA, PRE_RETIREMENT (≥45), NEW_MEMBER, AT_RISK (open complaint or breached case), ACTIVE — case-derived facts computed live from the case engine
- **Campaigns**: draft → launch against live segment membership; outbound interactions logged per recipient
- **PenCom complaints register**: quarterly export with days-to-resolution, outcome codes, CSAT

### Service integration (member-svc, 25 new routes, all in openapi.yaml)
- Auto-case hooks: `fraudScreen` and `mlFraudScreen` open/merge FRAUD cases (rules + ML-flagged), `claimsOpen` opens CLAIM cases, `uploadContributions` opens RECON_BREAK cases per exception
- Staff: list/filter, open, transition, assign, comment, tasks, link, resolve, escalate, sweep, stats, roster
- Member: my cases, raise COMPLAINT/SERVICE_REQUEST/GENERAL, CSAT rating
- CRM: 360, interactions, timeline, segments, campaigns, complaints register
- **Postgres persistence** (`store.CaseRepo`, migration `0008_casemgmt_crm.sql`): write-through upsert with full JSON snapshot, boot rehydration (`CASEMGMT_TENANTS`)

### Surface coverage
| Surface | Addition |
|---|---|
| Staff portal | **Cases workbench** (queue filters, SLA sweep, detail panel with comments/tasks/assign/escalate/task-gated resolve) + **CRM view** (member 360, campaigns, PenCom register) |
| Member PWA | **Support view** (raise complaint/request, track cases with SLA state, CSAT star rating), More-menu entry, en+pcm i18n |
| Flutter | **Support screen** + dashboard chip + 3 API client methods |
| WhatsApp/USSD/chatbot | 2 new intents — complaint (13), case status (14) — en+pcm replies |

## Verification
- `go build ./...` ✅; `go test ./...` ✅ (casemgmt 12 tests, crm 5 tests, member-svc e2e: complaint lifecycle w/ task-gated resolution + dedup merge + CSAT + register; fraud-screen → auto-case linkage)
- OpenAPI contract test ✅ (25 new routes documented)
- staff-portal build: 245KB/gzip 75KB ✅; member-pwa build: 286KB/gzip 88KB ✅
- Dart parse-check (format gate): client.dart, support.dart, dashboard.dart ✅

## Honest caveats
1. CRM interactions/campaigns persist to Postgres when DATABASE_URL is set; the in-memory path loses them on restart (same documented tradeoff as other engines).
2. Member facts provider reads balance from the in-memory ledger — when member-svc runs with Postgres repos, ledger read-side still needs its Postgres adapter (previously documented gap).
3. SLA sweep is trigger-based (endpoint/worker) — a cron/worker ticker in cmd/worker should call it on a schedule; not yet wired there.
4. Flutter full `flutter analyze` remains CI-gated (SDK checkout limitation in this sandbox).
