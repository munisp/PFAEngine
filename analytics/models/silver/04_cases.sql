-- silver_cases: unified ops case queue (casemgmt tranche 20), deduped by
-- (tenant_id, id) — ops_case is mutable state, so the latest partition
-- wins. Resolution time is derived from created_at -> updated_at for
-- terminal states (the engine stores the event log in case_json; columnar
-- updated_at is adequate for SLA analytics).
CREATE OR REPLACE TABLE silver_cases AS
WITH cases AS (
    SELECT * EXCLUDE (rn) FROM (
        SELECT *, row_number() OVER (
            PARTITION BY tenant_id, id ORDER BY dt DESC, updated_at DESC) AS rn
        FROM read_parquet('{{LAKE}}/bronze/ops_case/dt=*/*.parquet',
                          hive_partitioning = 1, union_by_name = true)
    ) WHERE rn = 1
)
SELECT
    id                                          AS case_id,
    tenant_id                                   AS tenant_id,
    type                                        AS case_type,
    subject                                     AS subject,
    member_id                                   AS member_id,
    employer_id                                 AS employer_id,
    status                                      AS status,
    priority                                    AS priority,
    queue                                       AS queue,
    assignee                                    AS assignee,
    source                                      AS source,
    due_at                                      AS due_at,
    sla_breached                                AS sla_breached,
    status IN ('RESOLVED', 'CLOSED')            AS is_resolved,
    status NOT IN ('RESOLVED', 'CLOSED')        AS is_open,
    CASE WHEN status IN ('RESOLVED', 'CLOSED')
         THEN epoch(updated_at - created_at) / 3600.0
    END                                         AS resolution_hours,
    date_trunc('month', CAST(created_at AS TIMESTAMP))::DATE AS created_month,
    CAST(created_at AS DATE)                    AS created_date,
    created_at                                  AS created_at,
    updated_at                                  AS updated_at
FROM cases;
