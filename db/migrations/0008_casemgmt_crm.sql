-- Tranche 20 migration (purely additive): unified case management engine
-- + CRM layer. One case table spanning all operational domains (fraud,
-- complaints, claims, service requests, KYC reviews, recon breaks, AML
-- alerts) plus the CRM interaction timeline and campaign registry.
-- Conventions match 0001–0007: tenant_id everywhere, composite
-- (tenant_id, id) PKs. The case row stores the full JSON snapshot
-- (comments, tasks, attachments, event log) so the engine can rehydrate
-- after restart; mutable state (status/assignee/sla) is columnar for
-- queue queries.

CREATE TABLE IF NOT EXISTS ops_case (
    id           TEXT NOT NULL,
    tenant_id    TEXT NOT NULL,
    type         TEXT NOT NULL,           -- FRAUD|COMPLAINT|CLAIM|DEATH_CLAIM|SERVICE_REQUEST|KYC_REVIEW|RECON_BREAK|AML_ALERT|GENERAL
    subject      TEXT NOT NULL,
    member_id    TEXT NOT NULL DEFAULT '',
    employer_id  TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL,           -- OPEN..CLOSED state machine
    priority     TEXT NOT NULL,           -- P1..P4
    queue        TEXT NOT NULL,
    assignee     TEXT NOT NULL DEFAULT '',
    source       TEXT NOT NULL,
    due_at       TIMESTAMPTZ NOT NULL,
    sla_breached BOOLEAN NOT NULL DEFAULT false,
    case_json    TEXT NOT NULL,           -- full Case snapshot incl. events/tasks/comments
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_ops_case_queue  ON ops_case(tenant_id, queue, status);
CREATE INDEX IF NOT EXISTS idx_ops_case_member ON ops_case(tenant_id, member_id);
CREATE INDEX IF NOT EXISTS idx_ops_case_type   ON ops_case(tenant_id, type, status);

CREATE TABLE IF NOT EXISTS crm_interaction (
    id         TEXT NOT NULL,
    tenant_id  TEXT NOT NULL,
    member_id  TEXT NOT NULL,
    channel    TEXT NOT NULL,             -- WHATSAPP|USSD|WEB|MOBILE|EMAIL|CALL|BRANCH|AGENT|SMS
    direction  TEXT NOT NULL,             -- INBOUND|OUTBOUND
    kind       TEXT NOT NULL DEFAULT 'ENQUIRY',
    summary    TEXT NOT NULL,
    case_id    TEXT NOT NULL DEFAULT '',
    staff_id   TEXT NOT NULL DEFAULT '',
    at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_crm_ix_member ON crm_interaction(tenant_id, member_id, at DESC);

CREATE TABLE IF NOT EXISTS crm_campaign (
    id          TEXT NOT NULL,
    tenant_id   TEXT NOT NULL,
    name        TEXT NOT NULL,
    segment     TEXT NOT NULL,
    channel     TEXT NOT NULL,
    message     TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'DRAFT',  -- DRAFT|ACTIVE|DONE
    recipients  INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    launched_at TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, id)
);
