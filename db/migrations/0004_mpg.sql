-- Tranche 15 migration (purely additive): Minimum Pension Guarantee (MPG)
-- top-up assessments (PRA 2014 s.84; PenCom ₦52,500 MPG directive,
-- November 2025) and the Pension Protection Fund (PPF) contribution
-- ledger. Conventions match 0001–0003: tenant_id on every scoped table,
-- composite (tenant_id, id) primary keys, WORM enforcement on the
-- append-only PPF ledger.

CREATE TABLE IF NOT EXISTS ppf_contribution ( -- WORM ledger
    id              TEXT NOT NULL,
    tenant_id       TEXT NOT NULL,
    source          TEXT NOT NULL,           -- FG | INDUSTRY | STATE | EMPLOYER
    contributor_ref TEXT NOT NULL,           -- state code, employer id, 'FG', 'INDUSTRY'
    amount_kobo     BIGINT NOT NULL CHECK (amount_kobo > 0),
    period          TEXT NOT NULL,           -- YYYY-MM
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_ppf_contrib_tenant ON ppf_contribution(tenant_id, source, contributor_ref);

CREATE OR REPLACE FUNCTION reject_ppf_contribution_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'ppf_contribution is append-only (WORM)';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS ppf_contribution_worm ON ppf_contribution;
CREATE TRIGGER ppf_contribution_worm
    BEFORE UPDATE OR DELETE ON ppf_contribution
    FOR EACH ROW EXECUTE FUNCTION reject_ppf_contribution_mutation();

REVOKE UPDATE, DELETE ON ppf_contribution FROM PUBLIC;

CREATE TABLE IF NOT EXISTS mpg_assessment (
    id                    TEXT NOT NULL,
    tenant_id             TEXT NOT NULL,
    rsa_id                TEXT NOT NULL,
    sector                TEXT NOT NULL,     -- PRIVATE | FEDERAL | STATE
    state_code            TEXT,
    period                TEXT NOT NULL,     -- YYYY-MM
    computed_pension_kobo BIGINT NOT NULL,
    minimum_kobo          BIGINT NOT NULL,
    topup_kobo            BIGINT NOT NULL,
    eligible              BOOLEAN NOT NULL,
    gated                 BOOLEAN NOT NULL,  -- below floor but state not in PPF
    assessed_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_mpg_assessment_tenant ON mpg_assessment(tenant_id, period, rsa_id);
