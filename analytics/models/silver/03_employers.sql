-- silver_employers: conformed employer dimension with remittance-schedule
-- rollups (schedules uploaded vs posted) folded in for remittance health.
CREATE OR REPLACE TABLE silver_employers AS
WITH emp AS (
    SELECT * EXCLUDE (rn) FROM (
        SELECT *, row_number() OVER (PARTITION BY id ORDER BY dt DESC) AS rn
        FROM read_parquet('{{LAKE}}/bronze/employer/dt=*/*.parquet',
                          hive_partitioning = 1, union_by_name = true)
    ) WHERE rn = 1
),
sched AS (
    SELECT * EXCLUDE (rn) FROM (
        SELECT *, row_number() OVER (PARTITION BY id ORDER BY dt DESC, uploaded_at DESC) AS rn
        FROM read_parquet('{{LAKE}}/bronze/contribution_schedule/dt=*/*.parquet',
                          hive_partitioning = 1, union_by_name = true)
    ) WHERE rn = 1
),
sched_stats AS (
    SELECT
        employer_id,
        count(*)                                   AS schedules_total,
        count(*) FILTER (posted_at IS NOT NULL)    AS schedules_posted,
        max(posted_at)                             AS last_posted_at,
        max(uploaded_at)                           AS last_uploaded_at
    FROM sched
    GROUP BY employer_id
)
SELECT
    e.id                                AS employer_id,
    e.tenant_id                         AS tenant_id,
    e.rc_number                         AS rc_number,
    e.name                              AS employer_name,
    e.compliance_state                  AS compliance_state,
    CAST(e.registered_at AS DATE)       AS registered_date,
    COALESCE(s.schedules_total, 0)      AS schedules_total,
    COALESCE(s.schedules_posted, 0)     AS schedules_posted,
    s.last_posted_at                    AS last_posted_at
FROM emp e
LEFT JOIN sched_stats s ON s.employer_id = e.id;
