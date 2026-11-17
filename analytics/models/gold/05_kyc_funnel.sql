-- gold_kyc_funnel: cumulative conversion through the eKYC state machine.
-- One row per funnel stage with an explicit ordering for dashboards:
--   1 started   — any session created
--   2 identity  — NIN or BVN verified
--   3 biometric — liveness passed (score present) 
--   4 decided   — reached DECIDED state
--   5 approved  — decision AUTO_APPROVE
--   6 manual    — routed to MANUAL_REVIEW (ops queue)
--   7 rejected  — decision REJECT
-- Counts are cumulative stage pass-throughs (a decided session also
-- counts at stages 1-3 if it cleared them), which is what a funnel chart
-- expects.
CREATE OR REPLACE TABLE gold_kyc_funnel AS
WITH s AS (SELECT * FROM silver_kyc_sessions)
SELECT stage_order, stage, sessions FROM (
    SELECT 1 AS stage_order, 'started'  AS stage, count(*) AS sessions FROM s
    UNION ALL
    SELECT 2, 'identity_verified', count(*) FROM s WHERE nin_verified OR bvn_verified
    UNION ALL
    SELECT 3, 'biometric_passed',  count(*) FROM s WHERE liveness_score IS NOT NULL
    UNION ALL
    SELECT 4, 'decided',           count(*) FROM s WHERE state = 'DECIDED'
    UNION ALL
    SELECT 5, 'auto_approved',     count(*) FROM s WHERE decision = 'AUTO_APPROVE'
    UNION ALL
    SELECT 6, 'manual_review',     count(*) FROM s WHERE decision = 'MANUAL_REVIEW'
    UNION ALL
    SELECT 7, 'rejected',          count(*) FROM s WHERE decision = 'REJECT'
)
ORDER BY stage_order;
