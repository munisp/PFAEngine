-- gold_employer_remittance_health: one row per employer with remittance
-- discipline signals and a derived health grade:
--   HEALTHY   — >= 80% of uploaded schedules posted
--   WATCH     — 40-79% posted
--   DEFAULTING— < 40% posted (or registered but never remitted)
-- The grade is analytics-side only; the operational compliance_state
-- (driven by member-svc rules) is carried through untouched.
CREATE OR REPLACE TABLE gold_employer_remittance_health AS
WITH contrib AS (
    SELECT
        employer_id,
        count(DISTINCT contribution_month)          AS months_remitted,
        sum(gross_kobo)::BIGINT                     AS total_remitted_kobo,
        max(contribution_date)                      AS last_contribution_date
    FROM silver_contributions
    WHERE employer_id IS NOT NULL
    GROUP BY employer_id
)
SELECT
    e.employer_id                                   AS employer_id,
    e.tenant_id                                     AS tenant_id,
    e.employer_name                                 AS employer_name,
    e.rc_number                                     AS rc_number,
    e.compliance_state                              AS compliance_state,
    e.schedules_total                               AS schedules_total,
    e.schedules_posted                              AS schedules_posted,
    CASE WHEN e.schedules_total > 0
         THEN round(10000.0 * e.schedules_posted / e.schedules_total)::BIGINT
    END                                             AS schedule_posted_bps,
    COALESCE(c.months_remitted, 0)                  AS months_remitted,
    COALESCE(c.total_remitted_kobo, 0)              AS total_remitted_kobo,
    c.last_contribution_date                        AS last_contribution_date,
    e.last_posted_at                                AS last_posted_at,
    CASE
        WHEN e.schedules_total = 0 THEN 'DEFAULTING'
        WHEN e.schedules_posted * 5 >= e.schedules_total * 4 THEN 'HEALTHY'
        WHEN e.schedules_posted * 5 >= e.schedules_total * 2 THEN 'WATCH'
        ELSE 'DEFAULTING'
    END                                             AS remittance_health
FROM silver_employers e
LEFT JOIN contrib c ON c.employer_id = e.employer_id
ORDER BY total_remitted_kobo DESC;
