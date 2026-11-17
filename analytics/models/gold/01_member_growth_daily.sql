-- gold_member_growth_daily: daily new-member acquisitions with cumulative
-- book size. Grain: one row per calendar day that had >= 1 account opening.
CREATE OR REPLACE TABLE gold_member_growth_daily AS
WITH daily AS (
    SELECT
        opened_date                     AS dt,
        count(*)                        AS new_members,
        count(*) FILTER (rsa_status = 'ACTIVE')   AS new_active_members
    FROM silver_members
    WHERE opened_date IS NOT NULL
    GROUP BY opened_date
)
SELECT
    dt,
    new_members,
    new_active_members,
    sum(new_members)        OVER (ORDER BY dt)  AS total_members,
    sum(new_active_members) OVER (ORDER BY dt)  AS total_active_members
FROM daily
ORDER BY dt;
