-- gold_case_sla_compliance: monthly SLA compliance per case type/queue.
-- sla_compliance_bps is integer basis points (10000 = 100%), consistent
-- with the platform's no-floats convention for presentation-critical
-- numbers; avg_resolution_hours stays a double (derived, informational).
CREATE OR REPLACE TABLE gold_case_sla_compliance AS
SELECT
    created_month                               AS month,
    case_type,
    queue,
    count(*)                                    AS total_cases,
    count(*) FILTER (is_open)                   AS open_cases,
    count(*) FILTER (is_resolved)               AS resolved_cases,
    count(*) FILTER (sla_breached)              AS breached_cases,
    round(10000.0 * (1.0 - count(*) FILTER (sla_breached) / count(*)))::BIGINT
                                                AS sla_compliance_bps,
    avg(resolution_hours)                       AS avg_resolution_hours
FROM silver_cases
WHERE created_month IS NOT NULL
GROUP BY 1, 2, 3
ORDER BY 1, 2, 3;
