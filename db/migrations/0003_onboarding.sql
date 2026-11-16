-- Tranche 14 migration (purely additive): durable persistence for the
-- unified onboarding framework (FR-520) and member preferences (REG-06).
-- Conventions match 0001/0002: tenant_id on every scoped table, WORM
-- enforcement on append-only evidence. onboarding_case/onboarding_step are
-- updatable (the status machine mutates them); onboarding_document is WORM.

CREATE TABLE IF NOT EXISTS onboarding_case (
    id              TEXT NOT NULL,
    tenant_id       TEXT NOT NULL,
    stakeholder     TEXT NOT NULL,             -- MEMBER | MICRO_MEMBER | EMPLOYER | RETIREE | STAFF | TENANT | NOK
    status          TEXT NOT NULL,             -- DRAFT | DOCS_PENDING | KYC_PENDING | APPROVAL_PENDING | ACTIVE | REJECTED | SUSPENDED
    ref_id          TEXT,                      -- linked entity (member, employer, tenant...)
    reject_reason   TEXT,
    submitted_by    TEXT NOT NULL,
    approved_by     TEXT,                      -- maker-checker approver
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Tenant-scoped PK: case IDs are per-service sequential (onb-NNNNNN),
    -- so uniqueness must be per tenant to prevent cross-tenant collisions.
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_onboarding_case_tenant ON onboarding_case(tenant_id, stakeholder, status);

CREATE TABLE IF NOT EXISTS onboarding_step (
    tenant_id       TEXT NOT NULL,
    case_id         TEXT NOT NULL,
    name            TEXT NOT NULL,             -- pipeline step (e.g. nin_verify)
    required        BOOLEAN NOT NULL,
    done            BOOLEAN NOT NULL DEFAULT FALSE,
    done_at         TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, case_id, name),
    FOREIGN KEY (tenant_id, case_id) REFERENCES onboarding_case(tenant_id, id)
);

CREATE TABLE IF NOT EXISTS onboarding_document ( -- WORM evidence artefacts
    id              TEXT NOT NULL,
    tenant_id       TEXT NOT NULL,
    case_id         TEXT NOT NULL,
    kind            TEXT NOT NULL,             -- e.g. nin_slip, glip_policy, pencom_licence
    uri             TEXT NOT NULL,             -- object-storage reference
    uploaded_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, case_id) REFERENCES onboarding_case(tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_onboarding_document_case ON onboarding_document(tenant_id, case_id);
-- WORM enforcement: documents are immutable once uploaded; corrections are
-- new documents. REVOKE follows the 0001/0002 style; the trigger
-- additionally binds the table owner (whom REVOKE ... FROM PUBLIC does not
-- affect) so UPDATE/DELETE are rejected for every role.
REVOKE UPDATE, DELETE ON onboarding_document FROM PUBLIC;
CREATE OR REPLACE FUNCTION reject_onboarding_document_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'onboarding_document is WORM: UPDATE and DELETE are not allowed';
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS onboarding_document_worm ON onboarding_document;
CREATE TRIGGER onboarding_document_worm BEFORE UPDATE OR DELETE ON onboarding_document
    FOR EACH ROW EXECUTE FUNCTION reject_onboarding_document_mutation();

CREATE TABLE IF NOT EXISTS member_prefs (      -- member preferences (REG-06)
    tenant_id       TEXT NOT NULL,
    member_id       TEXT NOT NULL,
    locale          TEXT NOT NULL DEFAULT 'en',
    phone_e164      TEXT,                      -- normalised: digits only
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, member_id)
);
CREATE INDEX IF NOT EXISTS idx_member_prefs_phone ON member_prefs(tenant_id, phone_e164);
