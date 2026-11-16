-- Tranche 6 migration (purely additive): member registry, KYC evidence,
-- statement metadata, regulatory report artifacts.
-- Conventions match 0001: tenant_id on every table, integer money/scores
-- (no floats, DA-03), WORM enforcement via REVOKE on append-only tables.

CREATE TABLE IF NOT EXISTS member (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    pin             TEXT NOT NULL,             -- PenCom RSA PIN (FR-107)
    nin_hash        TEXT,                      -- salted hash only (FR-101)
    bvn_hash        TEXT,                      -- salted hash only (FR-102)
    status          TEXT NOT NULL DEFAULT 'ACTIVE',
    fund_code       TEXT NOT NULL,             -- e.g. FUND_II (BR-14)
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_member_tenant_pin ON member(tenant_id, pin);

CREATE TABLE IF NOT EXISTS kyc_record (        -- WORM evidence (FR-103/108)
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    member_id       TEXT NOT NULL REFERENCES member(id),
    kind            TEXT NOT NULL CHECK (kind IN ('ENROLL','IAM_ALIVE')),
    score_bps       INT NOT NULL CHECK (score_bps BETWEEN 0 AND 10000), -- 0.95 -> 9500
    outcome         TEXT NOT NULL,             -- PASS | MANUAL_REVIEW | FAIL
    at              TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_kyc_record_member ON kyc_record(tenant_id, member_id, kind, at DESC);
-- WORM enforcement: corrections are new records, never edits.
REVOKE UPDATE, DELETE ON kyc_record FROM PUBLIC;

CREATE TABLE IF NOT EXISTS statement_record (  -- WORM generation metadata (FR-501)
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    member_id       TEXT NOT NULL REFERENCES member(id),
    period          TEXT NOT NULL,             -- YYYY-MM or YYYY-Qn
    verify_code_hash TEXT NOT NULL,            -- hash of HMAC verify code
    generated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_statement_record_member ON statement_record(tenant_id, member_id, generated_at DESC);
-- WORM enforcement: statements are immutable once generated.
REVOKE UPDATE, DELETE ON statement_record FROM PUBLIC;

CREATE TABLE IF NOT EXISTS report_artifact (   -- generated returns metadata (FR-502)
    tenant_id       TEXT NOT NULL,
    report_type     TEXT NOT NULL,             -- PENCOM_QUARTERLY | RMAS_MONTHLY | ...
    period          TEXT NOT NULL,
    payload_hash    TEXT NOT NULL,
    generated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, report_type, period)
);
