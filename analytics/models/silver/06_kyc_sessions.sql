-- silver_kyc_sessions: eKYC sessions live as engine snapshots in
-- state_snapshot (kind = 'ekyc.session', payload = JSON session blob,
-- see internal/ekyc). Session state machine:
-- DOCUMENT_CAPTURE -> DOC_ANALYSIS -> IDENTITY_CHECK -> BIOMETRIC ->
-- DECIDED (decision AUTO_APPROVE | MANUAL_REVIEW | REJECT).
-- Defensive JSON extraction: missing keys become NULL, bad timestamps
-- become NULL (TRY_CAST), so a schema drift in the payload never breaks
-- the build.
CREATE OR REPLACE TABLE silver_kyc_sessions AS
WITH snaps AS (
    SELECT * EXCLUDE (rn) FROM (
        SELECT *, row_number() OVER (
            PARTITION BY kind, tenant_id, entity_id
            ORDER BY dt DESC, updated_at DESC) AS rn
        FROM read_parquet('{{LAKE}}/bronze/state_snapshot/dt=*/*.parquet',
                          hive_partitioning = 1, union_by_name = true)
    ) WHERE rn = 1 AND kind = 'ekyc.session'
)
SELECT
    entity_id                                               AS session_id,
    tenant_id                                               AS tenant_id,
    payload ->> '$.member_id'                               AS member_id,
    COALESCE(payload ->> '$.state', 'UNKNOWN')              AS state,
    COALESCE(payload ->> '$.decision', '')                  AS decision,
    COALESCE(TRY_CAST(payload ->> '$.nin_verified' AS BOOLEAN), false) AS nin_verified,
    COALESCE(TRY_CAST(payload ->> '$.bvn_verified' AS BOOLEAN), false) AS bvn_verified,
    TRY_CAST(payload ->> '$.liveness_score' AS DOUBLE)      AS liveness_score,
    TRY_CAST(payload ->> '$.face_score' AS DOUBLE)          AS face_score,
    TRY_CAST(payload ->> '$.risk_score' AS INTEGER)         AS risk_score,
    TRY_CAST(payload ->> '$.created_at' AS TIMESTAMPTZ)     AS started_at,
    TRY_CAST(payload ->> '$.decided_at' AS TIMESTAMPTZ)     AS decided_at,
    updated_at                                              AS snapshot_updated_at
FROM snaps;
