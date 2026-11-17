-- gold_retirement_pipeline: forward view of members approaching
-- retirement (PRA 2014: RSA retirement benefits accessible from age 50)
-- cross-tabbed with retirement benefit-claim states. Grain: age band.
CREATE OR REPLACE TABLE gold_retirement_pipeline AS
WITH members AS (
    SELECT
        rsa_id,
        CASE
            WHEN age_years >= 50 THEN '50+ (retirement-eligible)'
            WHEN age_years >= 45 THEN '45-49 (5y runway)'
            WHEN age_years >= 40 THEN '40-44'
            ELSE '<40'
        END AS age_band,
        CASE
            WHEN age_years >= 50 THEN 1
            WHEN age_years >= 45 THEN 2
            WHEN age_years >= 40 THEN 3
            ELSE 4
        END AS band_order
    FROM silver_members
    WHERE age_years IS NOT NULL
),
claims AS (
    SELECT * EXCLUDE (rn) FROM (
        SELECT *, row_number() OVER (
            PARTITION BY id ORDER BY dt DESC, submitted_at DESC) AS rn
        FROM read_parquet('{{LAKE}}/bronze/benefit_claim/dt=*/*.parquet',
                          hive_partitioning = 1, union_by_name = true)
    ) WHERE rn = 1 AND upper(type) LIKE '%RETIRE%'
),
claim_bands AS (
    SELECT
        m.age_band,
        count(*)                                        AS retirement_claims,
        count(*) FILTER (cl.state NOT IN ('APPROVED', 'PAID', 'REJECTED')) AS pending_claims,
        count(*) FILTER (cl.state IN ('APPROVED', 'PAID'))                 AS approved_claims,
        sum(cl.amount_kobo)::BIGINT                     AS claim_value_kobo
    FROM claims cl
    JOIN members m ON m.rsa_id = cl.rsa_id
    GROUP BY 1
)
SELECT
    m.age_band                              AS age_band,
    count(*)                                AS members,
    COALESCE(c.retirement_claims, 0)        AS retirement_claims,
    COALESCE(c.pending_claims, 0)           AS pending_claims,
    COALESCE(c.approved_claims, 0)          AS approved_claims,
    COALESCE(c.claim_value_kobo, 0)         AS claim_value_kobo
FROM members m
LEFT JOIN claim_bands c ON c.age_band = m.age_band
GROUP BY m.age_band, m.band_order, c.retirement_claims, c.pending_claims,
         c.approved_claims, c.claim_value_kobo
ORDER BY m.band_order;
