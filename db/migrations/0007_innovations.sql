-- Tranche 17 migration (purely additive): persistence for the 15-
-- innovation tranche's stateful domains — fraud case queue, embedded
-- API keys + webhooks, agent network + WORM commission ledger,
-- succession nomination history (WORM), mortgage equity applications
-- (PRA s.89(2)) and escalation rules. Conventions match 0001–0006:
-- tenant_id everywhere, composite (tenant_id, id) PKs, WORM triggers on
-- financial ledgers.

CREATE TABLE IF NOT EXISTS fraud_case (
    id         TEXT NOT NULL,
    tenant_id  TEXT NOT NULL,
    member_id  TEXT NOT NULL,
    score      INT NOT NULL,
    signals    TEXT NOT NULL,              -- comma-joined signal codes
    tx_json    TEXT NOT NULL,              -- screened transaction snapshot
    status     TEXT NOT NULL,              -- OPEN | CONFIRMED_FRAUD | CLEARED
    opened_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_fraud_case_tenant ON fraud_case(tenant_id, status, member_id);

CREATE TABLE IF NOT EXISTS api_key (
    id         TEXT NOT NULL,
    tenant_id  TEXT NOT NULL,
    label      TEXT NOT NULL,
    hash       TEXT NOT NULL,              -- SHA-256 of the token; token never stored
    scopes     TEXT NOT NULL,              -- comma-joined scopes
    sandbox    BOOLEAN NOT NULL,
    revoked    BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_api_key_hash ON api_key(tenant_id, hash);

CREATE TABLE IF NOT EXISTS webhook_sub (
    id         TEXT NOT NULL,
    tenant_id  TEXT NOT NULL,
    url        TEXT NOT NULL,
    events     TEXT NOT NULL,              -- comma-joined event names
    secret     TEXT NOT NULL,
    active     BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

CREATE TABLE IF NOT EXISTS agent (
    id             TEXT NOT NULL,
    tenant_id      TEXT NOT NULL,
    name           TEXT NOT NULL,
    phone          TEXT NOT NULL,
    state_code     TEXT NOT NULL,
    commission_bps BIGINT NOT NULL CHECK (commission_bps >= 0),
    suspended      BOOLEAN NOT NULL DEFAULT false,
    accredited_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

CREATE TABLE IF NOT EXISTS agent_enrolment (
    id         TEXT NOT NULL,
    tenant_id  TEXT NOT NULL,
    agent_id   TEXT NOT NULL,
    rsa_id     TEXT NOT NULL,
    name       TEXT NOT NULL,
    phone      TEXT NOT NULL,
    trade      TEXT NOT NULL,
    at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, agent_id) REFERENCES agent(tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_agent_enrolment ON agent_enrolment(tenant_id, agent_id);

CREATE TABLE IF NOT EXISTS agent_commission ( -- WORM ledger
    id                TEXT NOT NULL,
    tenant_id         TEXT NOT NULL,
    agent_id          TEXT NOT NULL,
    contribution_kobo BIGINT NOT NULL CHECK (contribution_kobo > 0),
    amount_kobo       BIGINT NOT NULL CHECK (amount_kobo >= 0),
    period            TEXT NOT NULL,
    at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, agent_id) REFERENCES agent(tenant_id, id)
);

CREATE TABLE IF NOT EXISTS nomination ( -- WORM version history
    id                   TEXT NOT NULL,
    tenant_id            TEXT NOT NULL,
    member_id            TEXT NOT NULL,
    version              INT NOT NULL,
    beneficiaries_json   TEXT NOT NULL,
    confirmed_at         TIMESTAMPTZ NOT NULL,
    next_reconfirm_due   TIMESTAMPTZ NOT NULL,
    superseded           BOOLEAN NOT NULL DEFAULT false,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, member_id, version)
);

CREATE TABLE IF NOT EXISTS mortgage_application (
    id                TEXT NOT NULL,
    tenant_id         TEXT NOT NULL,
    member_id         TEXT NOT NULL,
    rsa_balance_kobo  BIGINT NOT NULL,
    amount_kobo       BIGINT NOT NULL CHECK (amount_kobo > 0),
    lender_ref        TEXT NOT NULL,
    status            TEXT NOT NULL,       -- SUBMITTED | APPROVED | DISBURSED | REJECTED
    reason            TEXT NOT NULL DEFAULT '',
    submitted_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at        TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_mortgage_app_member ON mortgage_application(tenant_id, member_id);

CREATE TABLE IF NOT EXISTS escalation_rule (
    id             TEXT NOT NULL,
    tenant_id      TEXT NOT NULL,
    member_id      TEXT NOT NULL,
    base_rate_bps  BIGINT NOT NULL,
    step_bps       BIGINT NOT NULL CHECK (step_bps > 0),
    max_bps        BIGINT NOT NULL,
    current_bps    BIGINT NOT NULL,
    opted_out      BOOLEAN NOT NULL DEFAULT false,
    opt_out_reason TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, member_id)
);

-- WORM enforcement on the two append-only ledgers + nomination history.
CREATE OR REPLACE FUNCTION reject_innovation_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION '% is append-only (WORM)', TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS agent_commission_worm ON agent_commission;
CREATE TRIGGER agent_commission_worm
    BEFORE UPDATE OR DELETE ON agent_commission
    FOR EACH ROW EXECUTE FUNCTION reject_innovation_mutation();

DROP TRIGGER IF EXISTS nomination_worm ON nomination;
CREATE TRIGGER nomination_worm
    BEFORE UPDATE OR DELETE ON nomination
    FOR EACH ROW EXECUTE FUNCTION reject_innovation_mutation();

REVOKE UPDATE, DELETE ON agent_commission FROM PUBLIC;
REVOKE UPDATE, DELETE ON nomination FROM PUBLIC;
