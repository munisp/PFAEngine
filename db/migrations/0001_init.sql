-- PFAEngine canonical data model (Architecture Report §5, DA-01..07).
-- Every table carries tenant_id; per-tenant PostgreSQL schemas enforce
-- isolation in production (NFR-10). Money: integer kobo; units 8-decimal
-- (stored as bigint raw, 1e8 = 1 unit). No floats anywhere (DA-03).

CREATE TABLE IF NOT EXISTS person (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    full_name       TEXT NOT NULL,
    dob             DATE NOT NULL,
    phone           TEXT,
    email           TEXT,
    category        TEXT NOT NULL DEFAULT 'PROSPECTIVE', -- SCM-03 lifecycle
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS kyc_verification (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    person_id       TEXT NOT NULL REFERENCES person(id),
    kind            TEXT NOT NULL,              -- NIN | BVN | LIVENESS_ENROLL | LIVENESS_IAM_ALIVE
    provider        TEXT NOT NULL,
    provider_token  TEXT,                       -- NIN: token only, never raw (FR-101)
    identity_hash   TEXT,                       -- BVN: salted hash only (FR-102); dup detection (FR-105)
    score           NUMERIC(5,4),               -- liveness score; thresholds 0.80/0.60 (FR-103)
    status          TEXT NOT NULL,
    evidence_ref    TEXT NOT NULL,
    attempts        INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_kyc_identity_hash ON kyc_verification(identity_hash);

CREATE TABLE IF NOT EXISTS consent (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    person_id       TEXT NOT NULL REFERENCES person(id),
    purpose         TEXT NOT NULL,
    wording_version TEXT NOT NULL,              -- FR-106
    granted         BOOLEAN NOT NULL,
    withdrawn_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS rsa_account (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    person_id       TEXT NOT NULL REFERENCES person(id),
    pin             TEXT UNIQUE,                -- PenCom RSA PIN (FR-107)
    pin_status      TEXT NOT NULL DEFAULT 'REQUESTED',
    fund_id         TEXT NOT NULL,
    scheme_id       TEXT,
    status          TEXT NOT NULL DEFAULT 'ACTIVE',
    contrib_rate_pct INT NOT NULL DEFAULT 0,
    opened_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS nominee (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    rsa_id          TEXT NOT NULL REFERENCES rsa_account(id),
    name            TEXT NOT NULL,
    relationship    TEXT NOT NULL,
    share_percent   INT NOT NULL,
    evidence_ref    TEXT NOT NULL              -- FR-201
);

CREATE TABLE IF NOT EXISTS employer (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    rc_number       TEXT NOT NULL,             -- FR-202
    name            TEXT NOT NULL,
    contact         TEXT,
    remittance_terms TEXT,
    compliance_state TEXT NOT NULL DEFAULT 'COMPLIANT',
    registered_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS contribution_schedule (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    employer_id     TEXT NOT NULL REFERENCES employer(id),
    period          TEXT NOT NULL,             -- YYYY-MM
    status          TEXT NOT NULL DEFAULT 'VALIDATED',
    error_summary   TEXT,
    uploaded_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    posted_at       TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS contribution (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    schedule_id     TEXT NOT NULL REFERENCES contribution_schedule(id),
    rsa_pin         TEXT NOT NULL,
    gross_kobo      BIGINT NOT NULL,
    employee_kobo   BIGINT NOT NULL,           -- 8% (PRA 2014 s4)
    employer_kobo   BIGINT NOT NULL,           -- 10%
    voluntary_kobo  BIGINT NOT NULL DEFAULT 0,
    is_ppp          BOOLEAN NOT NULL DEFAULT FALSE, -- BR-06
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_contribution_pin ON contribution(tenant_id, rsa_pin);

CREATE TABLE IF NOT EXISTS fund (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    name            TEXT NOT NULL,
    currency        TEXT NOT NULL DEFAULT 'NGN'
);

CREATE TABLE IF NOT EXISTS fund_price (              -- daily NAV (FR-304)
    fund_id         TEXT NOT NULL REFERENCES fund(id),
    price_date      DATE NOT NULL,
    price_kobo      BIGINT NOT NULL,
    PRIMARY KEY (fund_id, price_date)
);

CREATE TABLE IF NOT EXISTS unit_holding (            -- 8-decimal units as raw bigint
    rsa_id          TEXT NOT NULL REFERENCES rsa_account(id),
    fund_id         TEXT NOT NULL REFERENCES fund(id),
    units_raw       BIGINT NOT NULL DEFAULT 0,       -- 1e8 = 1 unit (DA-03)
    PRIMARY KEY (rsa_id, fund_id)
);

CREATE TABLE IF NOT EXISTS benefit_claim (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    rsa_id          TEXT NOT NULL REFERENCES rsa_account(id),
    type            TEXT NOT NULL,
    amount_kobo     BIGINT NOT NULL,
    state           TEXT NOT NULL DEFAULT 'SUBMITTED',
    submitted_by    TEXT NOT NULL,
    decided_by      TEXT,                        -- maker-checker (FR-401)
    decision_reason TEXT,
    rule_version    TEXT NOT NULL,               -- SCM-15
    custodian_ref   TEXT,
    submitted_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at      TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS transfer_request (        -- RTS (FR-402)
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    rsa_id          TEXT NOT NULL,
    rsa_pin         TEXT NOT NULL,
    direction       TEXT NOT NULL,
    counterparty_pfa TEXT NOT NULL,
    value_kobo      BIGINT NOT NULL,
    state           TEXT NOT NULL DEFAULT 'INITIATED',
    quarter         TEXT,
    initiated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    settled_at      TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS liveness_check (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    person_id       TEXT NOT NULL,
    kind            TEXT NOT NULL,               -- ENROLL | IAM_ALIVE (FR-108)
    score           NUMERIC(5,4),
    status          TEXT NOT NULL,
    evidence_ref    TEXT,
    at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS ledger_entry (            -- append-only (FR-302)
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    memo            TEXT,
    reference       TEXT,
    reversal_of     TEXT,
    posted_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS ledger_posting (
    entry_id        TEXT NOT NULL REFERENCES ledger_entry(id),
    account_id      TEXT NOT NULL,
    debit_kobo      BIGINT NOT NULL DEFAULT 0,
    credit_kobo     BIGINT NOT NULL DEFAULT 0,
    CHECK (debit_kobo >= 0 AND credit_kobo >= 0),
    CHECK (NOT (debit_kobo > 0 AND credit_kobo > 0)),
    PRIMARY KEY (entry_id, account_id)
);

CREATE TABLE IF NOT EXISTS audit_event (             -- WORM hash chain (SC-02)
    seq             BIGSERIAL PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    actor           TEXT NOT NULL,
    action          TEXT NOT NULL,
    entity_type     TEXT NOT NULL,
    entity_id       TEXT NOT NULL,
    before          JSONB,
    after           JSONB,
    consent_ref     TEXT,
    kyc_ref         TEXT,
    at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    prev_hash       TEXT NOT NULL,
    hash            TEXT NOT NULL
);
-- WORM enforcement: no UPDATE/DELETE on audit_event.
REVOKE UPDATE, DELETE ON audit_event FROM PUBLIC;
