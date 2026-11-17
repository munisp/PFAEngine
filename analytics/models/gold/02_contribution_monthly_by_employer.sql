-- gold_contribution_monthly_by_employer: remittance value/volume per
-- employer per month. All amounts integer kobo (DA-03).
CREATE OR REPLACE TABLE gold_contribution_monthly_by_employer AS
SELECT
    COALESCE(c.employer_id, '(unattributed)')   AS employer_id,
    COALESCE(e.employer_name, '(unattributed)') AS employer_name,
    c.contribution_month                        AS month,
    count(*)                                    AS contributions,
    count(DISTINCT c.rsa_pin)                   AS distinct_members,
    sum(c.gross_kobo)::BIGINT                   AS gross_kobo,
    sum(c.employee_kobo)::BIGINT                AS employee_kobo,
    sum(c.employer_kobo)::BIGINT                AS employer_kobo,
    sum(c.voluntary_kobo)::BIGINT               AS voluntary_kobo
FROM silver_contributions c
LEFT JOIN silver_employers e ON e.employer_id = c.employer_id
WHERE c.contribution_month IS NOT NULL
GROUP BY 1, 2, 3
ORDER BY 3, 1;
