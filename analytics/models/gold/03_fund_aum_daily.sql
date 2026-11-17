-- gold_fund_aum_daily: AUM per fund per NAV date plus total AUM across
-- the fund range. Amounts integer kobo; see silver_funds_nav header for
-- the units*NAV math and the holdings-snapshot caveat.
CREATE OR REPLACE TABLE gold_fund_aum_daily AS
SELECT
    price_date                                  AS dt,
    fund_id,
    fund_name,
    price_kobo,
    units_raw,
    aum_kobo,
    sum(aum_kobo) OVER (PARTITION BY price_date)::BIGINT AS total_aum_kobo
FROM silver_funds_nav
ORDER BY dt, fund_id;
