-- Tranche 16a migration (purely additive): PenCare retiree-healthcare
-- registry (Pension Industry Healthcare Plan for Retirees, launched with
-- the MPG at the Presidential Pension Security Initiative, Nov 2025).
-- Enrollee registry + health-access-card lifecycle are mutable state;
-- premium payments and fund contributions are WORM (append-only).
-- Conventions match 0001–0004: tenant_id on every scoped table,
-- composite (tenant_id, id) primary keys.

CREATE TABLE IF NOT EXISTS pencare_enrollee (
    id                   TEXT NOT NULL,
    tenant_id            TEXT NOT NULL,
    rsa_id               TEXT NOT NULL,
    monthly_pension_kobo BIGINT NOT NULL,
    floor_kobo           BIGINT NOT NULL,    -- MPG floor at enrolment
    status               TEXT NOT NULL,      -- ACTIVE | SUSPENDED | EXITED
    enrolled_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, rsa_id)
);
CREATE INDEX IF NOT EXISTS idx_pencare_enrollee_tenant ON pencare_enrollee(tenant_id, status);

CREATE TABLE IF NOT EXISTS pencare_card (
    id           TEXT NOT NULL,
    tenant_id    TEXT NOT NULL,
    enrollee_id  TEXT NOT NULL,
    card_number  TEXT,
    status       TEXT NOT NULL,              -- REQUESTED | ISSUED | ACTIVE | SUSPENDED
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    issued_at    TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, enrollee_id)
        REFERENCES pencare_enrollee(tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_pencare_card_enrollee ON pencare_card(tenant_id, enrollee_id, status);

CREATE TABLE IF NOT EXISTS pencare_premium ( -- WORM ledger
    id          TEXT NOT NULL,
    tenant_id   TEXT NOT NULL,
    enrollee_id TEXT NOT NULL,
    insurer_ref TEXT NOT NULL,               -- HMO / insurer reference
    amount_kobo BIGINT NOT NULL CHECK (amount_kobo > 0),
    period      TEXT NOT NULL,               -- YYYY-MM coverage period
    paid_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, enrollee_id)
        REFERENCES pencare_enrollee(tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_pencare_premium_tenant ON pencare_premium(tenant_id, enrollee_id, period);

CREATE TABLE IF NOT EXISTS pencare_fund_contribution ( -- WORM ledger
    id              TEXT NOT NULL,
    tenant_id       TEXT NOT NULL,
    source          TEXT NOT NULL,           -- INDUSTRY | PFA | PENCOM | DONOR
    contributor_ref TEXT NOT NULL,
    amount_kobo     BIGINT NOT NULL CHECK (amount_kobo > 0),
    period          TEXT NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_pencare_fund_tenant ON pencare_fund_contribution(tenant_id, source);

CREATE OR REPLACE FUNCTION reject_pencare_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION '% is append-only (WORM)', TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS pencare_premium_worm ON pencare_premium;
CREATE TRIGGER pencare_premium_worm
    BEFORE UPDATE OR DELETE ON pencare_premium
    FOR EACH ROW EXECUTE FUNCTION reject_pencare_mutation();

DROP TRIGGER IF EXISTS pencare_fund_worm ON pencare_fund_contribution;
CREATE TRIGGER pencare_fund_worm
    BEFORE UPDATE OR DELETE ON pencare_fund_contribution
    FOR EACH ROW EXECUTE FUNCTION reject_pencare_mutation();

REVOKE UPDATE, DELETE ON pencare_premium FROM PUBLIC;
REVOKE UPDATE, DELETE ON pencare_fund_contribution FROM PUBLIC;
