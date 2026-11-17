-- 0011_cdc.sql — CDC readiness for Debezium pgoutput (docs/CDC.md).
-- REPLICA IDENTITY FULL on update-heavy tables so the WAL carries the
-- full before-image (correct updates + deletes without a PK-change
-- corner case). Cost: slightly larger WAL for updates on these tables.
ALTER TABLE person                REPLICA IDENTITY FULL;
ALTER TABLE member                REPLICA IDENTITY FULL;
ALTER TABLE rsa_account           REPLICA IDENTITY FULL;
ALTER TABLE employer              REPLICA IDENTITY FULL;
ALTER TABLE contribution          REPLICA IDENTITY FULL;
ALTER TABLE contribution_schedule REPLICA IDENTITY FULL;
ALTER TABLE fund                  REPLICA IDENTITY FULL;
ALTER TABLE fund_price            REPLICA IDENTITY FULL;
ALTER TABLE unit_holding          REPLICA IDENTITY FULL;
ALTER TABLE ops_case              REPLICA IDENTITY FULL;
ALTER TABLE state_snapshot        REPLICA IDENTITY FULL;
ALTER TABLE benefit_claim         REPLICA IDENTITY FULL;

-- Publication the connector subscribes to (autocreate.mode=filtered
-- would also create it, but explicit DDL keeps it under migration
-- governance and survives connector deletion).
CREATE PUBLICATION pfaengine_pub FOR TABLE
    person, member, rsa_account, employer,
    contribution, contribution_schedule,
    fund, fund_price, unit_holding,
    ops_case, state_snapshot, benefit_claim;
