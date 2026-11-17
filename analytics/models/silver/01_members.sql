-- silver_members: conformed member dimension.
-- Joins the operational RSA registry (rsa_account + person) with the
-- member registry (member) on the PenCom PIN. Dedupes bronze partitions
-- by primary key, keeping the newest partition (dt) — this is what makes
-- both full-refresh and watermark-incremental bronze loads correct.
CREATE OR REPLACE TABLE silver_members AS
WITH rsa AS (
    SELECT * EXCLUDE (rn) FROM (
        SELECT *, row_number() OVER (PARTITION BY id ORDER BY dt DESC, opened_at DESC) AS rn
        FROM read_parquet('{{LAKE}}/bronze/rsa_account/dt=*/*.parquet',
                          hive_partitioning = 1, union_by_name = true)
    ) WHERE rn = 1
),
person AS (
    SELECT * EXCLUDE (rn) FROM (
        SELECT *, row_number() OVER (PARTITION BY id ORDER BY dt DESC) AS rn
        FROM read_parquet('{{LAKE}}/bronze/person/dt=*/*.parquet',
                          hive_partitioning = 1, union_by_name = true)
    ) WHERE rn = 1
),
registry AS (
    SELECT * EXCLUDE (rn) FROM (
        SELECT *, row_number() OVER (PARTITION BY tenant_id, pin ORDER BY dt DESC) AS rn
        FROM read_parquet('{{LAKE}}/bronze/member/dt=*/*.parquet',
                          hive_partitioning = 1, union_by_name = true)
    ) WHERE rn = 1
)
SELECT
    r.id                                        AS rsa_id,
    r.tenant_id                                 AS tenant_id,
    r.pin                                       AS rsa_pin,
    COALESCE(p.full_name, '')                   AS full_name,
    p.dob                                       AS dob,
    date_diff('year', p.dob, current_date)      AS age_years,
    p.category                                  AS person_category,
    r.fund_id                                   AS fund_id,
    r.status                                    AS rsa_status,
    r.pin_status                                AS pin_status,
    reg.status                                  AS registry_status,
    r.contrib_rate_pct                          AS contrib_rate_pct,
    CAST(r.opened_at AS DATE)                   AS opened_date,
    r.opened_at                                 AS opened_at
FROM rsa r
LEFT JOIN person p   ON p.id = r.person_id
LEFT JOIN registry reg ON reg.tenant_id = r.tenant_id AND reg.pin = r.pin;
