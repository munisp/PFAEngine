-- Tranche 16b migration (purely additive): infrastructure-investment
-- mandate registry (e.g. the FSD Africa–backed $750m pension
-- infrastructure platform). Mandates are mutable (status lifecycle);
-- commitments and deployments are WORM (append-only) financial records.
-- Multi-currency: amount_minor is integer minor units in `currency`
-- (kobo for NGN, cents for USD). Conventions match 0001–0005.

CREATE TABLE IF NOT EXISTS inv_mandate (
    id           TEXT NOT NULL,
    tenant_id    TEXT NOT NULL,
    name         TEXT NOT NULL,
    asset_class  TEXT NOT NULL,              -- INFRASTRUCTURE_FUND | INFRASTRUCTURE_BOND
    partner      TEXT NOT NULL DEFAULT '',
    currency     TEXT NOT NULL,              -- ISO 4217: NGN | USD
    target_minor BIGINT NOT NULL CHECK (target_minor > 0),
    cap_bps      BIGINT NOT NULL CHECK (cap_bps > 0 AND cap_bps <= 10000),
    status       TEXT NOT NULL,              -- OPEN | CLOSED
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_inv_mandate_tenant ON inv_mandate(tenant_id, status);

CREATE TABLE IF NOT EXISTS inv_commitment ( -- WORM ledger
    id           TEXT NOT NULL,
    tenant_id    TEXT NOT NULL,
    mandate_id   TEXT NOT NULL,
    fund_id      TEXT NOT NULL,              -- RSA multi-fund: I | II | III | IV | ...
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency     TEXT NOT NULL,
    ref          TEXT NOT NULL DEFAULT '',   -- investment-committee reference
    committed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, mandate_id)
        REFERENCES inv_mandate(tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_inv_commitment_mandate ON inv_commitment(tenant_id, mandate_id, fund_id);

CREATE TABLE IF NOT EXISTS inv_deployment ( -- WORM ledger
    id           TEXT NOT NULL,
    tenant_id    TEXT NOT NULL,
    mandate_id   TEXT NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency     TEXT NOT NULL,
    vehicle      TEXT NOT NULL DEFAULT '',   -- project / SPV / tranche
    deployed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, mandate_id)
        REFERENCES inv_mandate(tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_inv_deployment_mandate ON inv_deployment(tenant_id, mandate_id);

CREATE OR REPLACE FUNCTION reject_inv_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION '% is append-only (WORM)', TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS inv_commitment_worm ON inv_commitment;
CREATE TRIGGER inv_commitment_worm
    BEFORE UPDATE OR DELETE ON inv_commitment
    FOR EACH ROW EXECUTE FUNCTION reject_inv_mutation();

DROP TRIGGER IF EXISTS inv_deployment_worm ON inv_deployment;
CREATE TRIGGER inv_deployment_worm
    BEFORE UPDATE OR DELETE ON inv_deployment
    FOR EACH ROW EXECUTE FUNCTION reject_inv_mutation();

REVOKE UPDATE, DELETE ON inv_commitment FROM PUBLIC;
REVOKE UPDATE, DELETE ON inv_deployment FROM PUBLIC;
