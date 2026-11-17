-- silver_funds_nav: daily NAV per fund joined to the latest unit-holding
-- snapshot. AUM math: units_raw is a bigint with 8 implied decimals
-- (1e8 = 1 unit, DA-03) and price_kobo is integer kobo per unit, so
-- aum_kobo = units_raw * price_kobo / 1e8. Computed in HUGEINT to avoid
-- bigint overflow, then cast back to BIGINT kobo.
--
-- NOTE: unit_holding has no history (it is a current snapshot), so
-- historical AUM uses *current* units x *historical* NAV. True historical
-- AUM needs a holdings snapshot table — see docs/ANALYTICS.md gaps.
CREATE OR REPLACE TABLE silver_funds_nav AS
WITH funds AS (
    SELECT * EXCLUDE (rn) FROM (
        SELECT *, row_number() OVER (PARTITION BY id ORDER BY dt DESC) AS rn
        FROM read_parquet('{{LAKE}}/bronze/fund/dt=*/*.parquet',
                          hive_partitioning = 1, union_by_name = true)
    ) WHERE rn = 1
),
prices AS (
    SELECT * EXCLUDE (rn) FROM (
        SELECT *, row_number() OVER (
            PARTITION BY fund_id, price_date ORDER BY dt DESC) AS rn
        FROM read_parquet('{{LAKE}}/bronze/fund_price/dt=*/*.parquet',
                          hive_partitioning = 1, union_by_name = true)
    ) WHERE rn = 1
),
holdings AS (
    SELECT * EXCLUDE (rn) FROM (
        SELECT *, row_number() OVER (
            PARTITION BY rsa_id, fund_id ORDER BY dt DESC) AS rn
        FROM read_parquet('{{LAKE}}/bronze/unit_holding/dt=*/*.parquet',
                          hive_partitioning = 1, union_by_name = true)
    ) WHERE rn = 1
),
fund_units AS (
    SELECT fund_id, sum(units_raw)::HUGEINT AS units_raw
    FROM holdings GROUP BY fund_id
)
SELECT
    f.id                                        AS fund_id,
    f.name                                      AS fund_name,
    p.price_date                                AS price_date,
    p.price_kobo                                AS price_kobo,
    COALESCE(u.units_raw, 0)::BIGINT            AS units_raw,
    ((COALESCE(u.units_raw, 0) * p.price_kobo::HUGEINT)
        / 100000000)::BIGINT                    AS aum_kobo
FROM prices p
JOIN funds f       ON f.id = p.fund_id
LEFT JOIN fund_units u ON u.fund_id = p.fund_id;
