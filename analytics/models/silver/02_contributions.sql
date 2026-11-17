-- silver_contributions: one row per contribution line, enriched with the
-- employer and remittance period from contribution_schedule. Money stays
-- integer kobo (DA-03); naira formatting is a presentation-layer concern.
CREATE OR REPLACE TABLE silver_contributions AS
WITH contrib AS (
    SELECT * EXCLUDE (rn) FROM (
        SELECT *, row_number() OVER (PARTITION BY id ORDER BY dt DESC, created_at DESC) AS rn
        FROM read_parquet('{{LAKE}}/bronze/contribution/dt=*/*.parquet',
                          hive_partitioning = 1, union_by_name = true)
    ) WHERE rn = 1
),
sched AS (
    SELECT * EXCLUDE (rn) FROM (
        SELECT *, row_number() OVER (PARTITION BY id ORDER BY dt DESC, uploaded_at DESC) AS rn
        FROM read_parquet('{{LAKE}}/bronze/contribution_schedule/dt=*/*.parquet',
                          hive_partitioning = 1, union_by_name = true)
    ) WHERE rn = 1
)
SELECT
    c.id                                AS contribution_id,
    c.tenant_id                         AS tenant_id,
    c.rsa_pin                           AS rsa_pin,
    s.employer_id                       AS employer_id,
    s.period                            AS period,          -- YYYY-MM
    s.status                            AS schedule_status,
    CAST(c.created_at AS DATE)          AS contribution_date,
    date_trunc('month', CAST(c.created_at AS TIMESTAMP))::DATE AS contribution_month,
    c.gross_kobo                        AS gross_kobo,
    c.employee_kobo                     AS employee_kobo,
    c.employer_kobo                     AS employer_kobo,
    c.voluntary_kobo                    AS voluntary_kobo,
    c.is_ppp                            AS is_ppp,
    c.created_at                        AS created_at
FROM contrib c
LEFT JOIN sched s ON s.id = c.schedule_id;
