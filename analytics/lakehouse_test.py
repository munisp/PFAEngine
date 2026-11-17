"""Lakehouse unit tests.

Runs the real silver + gold SQL against a tiny fixture parquet lake using
an in-memory DuckDB — no Postgres needed. The Postgres gold-export test
is guarded by TEST_DATABASE_URL and skipped when unset.

    python3 -m unittest discover analytics        # from repo root
"""
from __future__ import annotations

import json
import os
import shutil
import sys
import tempfile
import unittest
from datetime import date, datetime, timezone
from pathlib import Path

import pyarrow as pa
import pyarrow.parquet as pq

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

import duckdb  # noqa: E402

import extract_full  # noqa: E402
import run  # noqa: E402

TS = pa.timestamp("us", tz="UTC")


def ts(y, m, d, hh=0):
    return datetime(y, m, d, hh, tzinfo=timezone.utc)


def write_bronze(lake: Path, table: str, data: dict, ds: str = "2024-06-01",
                 fname: str = "part-full.parquet") -> None:
    pdir = lake / "bronze" / table / f"dt={ds}"
    pdir.mkdir(parents=True, exist_ok=True)
    pq.write_table(pa.table(data), pdir / fname)


def write_fixture_lake(lake: Path) -> None:
    """Tiny but complete bronze lake: every table the silver models read."""
    write_bronze(lake, "person", {
        "id": ["p1", "p2", "p3"],
        "tenant_id": ["t1"] * 3,
        "full_name": ["Ada Lovelace", "Bola Tin", "Chidi Okoro"],
        "dob": pa.array([date(1968, 3, 1), date(1979, 7, 7), date(1995, 1, 1)], type=pa.date32()),
        "phone": ["0801", "0802", "0803"],
        "email": ["a@x.ng", "b@x.ng", "c@x.ng"],
        "category": ["ACTIVE", "ACTIVE", "PROSPECTIVE"],
        "created_at": pa.array([ts(2024, 1, 1)] * 3, type=TS),
    })
    # Two partitions of rsa_account: id r2 appears in both — the newer
    # dt must win (proves the dedupe contract).
    write_bronze(lake, "rsa_account", {
        "id": ["r1", "r2", "r3"],
        "tenant_id": ["t1"] * 3,
        "person_id": ["p1", "p2", "p3"],
        "pin": ["PIN1", "PIN2", "PIN3"],
        "pin_status": ["ISSUED"] * 3,
        "fund_id": ["FUND_II", "FUND_II", "FUND_IV"],
        "scheme_id": [None] * 3,
        "status": ["ACTIVE", "ACTIVE", "ACTIVE"],
        "contrib_rate_pct": [18, 18, 18],
        "opened_at": pa.array([ts(2024, 1, 5), ts(2024, 1, 6), ts(2024, 2, 1)], type=TS),
    }, ds="2024-05-01")
    write_bronze(lake, "rsa_account", {
        "id": ["r2"],
        "tenant_id": ["t1"],
        "person_id": ["p2"],
        "pin": ["PIN2"],
        "pin_status": ["ISSUED"],
        "fund_id": ["FUND_III"],              # moved funds in newer snapshot
        "scheme_id": [None],
        "status": ["ACTIVE"],
        "contrib_rate_pct": [18],
        "opened_at": pa.array([ts(2024, 1, 6)], type=TS),
    }, ds="2024-06-01")
    write_bronze(lake, "member", {
        "id": ["m1", "m2", "m3"],
        "tenant_id": ["t1"] * 3,
        "pin": ["PIN1", "PIN2", "PIN3"],
        "nin_hash": ["h1", "h2", "h3"],
        "bvn_hash": ["x1", "x2", "x3"],
        "status": ["ACTIVE"] * 3,
        "fund_code": ["FUND_II", "FUND_II", "FUND_IV"],
        "created_at": pa.array([ts(2024, 1, 5)] * 3, type=TS),
    })
    write_bronze(lake, "employer", {
        "id": ["emp-1", "emp-2"],
        "tenant_id": ["t1"] * 2,
        "rc_number": ["RC1", "RC2"],
        "name": ["Acme Ltd", "Ghost PLC"],
        "contact": [None, None],
        "remittance_terms": [None, None],
        "compliance_state": ["COMPLIANT", "WATCH"],
        "registered_at": pa.array([ts(2023, 12, 1)] * 2, type=TS),
    })
    write_bronze(lake, "contribution_schedule", {
        "id": ["sch-1"],
        "tenant_id": ["t1"],
        "employer_id": ["emp-1"],
        "period": ["2024-05"],
        "status": ["POSTED"],
        "error_summary": [None],
        "uploaded_at": pa.array([ts(2024, 5, 28)], type=TS),
        "posted_at": pa.array([ts(2024, 6, 1)], type=TS),
    })
    write_bronze(lake, "contribution", {
        "id": ["c1", "c2"],
        "tenant_id": ["t1"] * 2,
        "schedule_id": ["sch-1", "sch-1"],
        "rsa_pin": ["PIN1", "PIN2"],
        "gross_kobo": [180_000_00, 250_000_00],
        "employee_kobo": [80_000_00, 100_000_00],
        "employer_kobo": [100_000_00, 150_000_00],
        "voluntary_kobo": [0, 5_000_00],
        "is_ppp": [False, False],
        "created_at": pa.array([ts(2024, 5, 30), ts(2024, 5, 30)], type=TS),
    })
    write_bronze(lake, "fund", {
        "id": ["FUND_II"], "tenant_id": ["t1"], "name": ["Fund II"],
        "currency": ["NGN"],
    })
    write_bronze(lake, "fund_price", {
        "fund_id": ["FUND_II", "FUND_II"],
        "price_date": pa.array([date(2024, 5, 31), date(2024, 6, 1)], type=pa.date32()),
        "price_kobo": [150_00, 160_00],
    })
    write_bronze(lake, "unit_holding", {
        "rsa_id": ["r1", "r2"],
        "fund_id": ["FUND_II", "FUND_II"],
        "units_raw": [100_000_000, 100_000_000],       # 1 unit each
    })
    write_bronze(lake, "ops_case", {
        "id": ["case-1", "case-2"],
        "tenant_id": ["t1"] * 2,
        "type": ["COMPLAINT", "FRAUD"],
        "subject": ["late statement", "odd txn"],
        "member_id": ["m1", "m2"],
        "employer_id": ["", ""],
        "status": ["CLOSED", "OPEN"],
        "priority": ["P2", "P1"],
        "queue": ["service", "fraud"],
        "assignee": ["", ""],
        "source": ["WEB", "SYSTEM"],
        "due_at": pa.array([ts(2024, 6, 3), ts(2024, 6, 10)], type=TS),
        "sla_breached": [False, True],
        "case_json": ["{}", "{}"],
        "created_at": pa.array([ts(2024, 6, 1, 8), ts(2024, 6, 2, 9)], type=TS),
        "updated_at": pa.array([ts(2024, 6, 1, 20), ts(2024, 6, 2, 10)], type=TS),
    })
    write_bronze(lake, "state_snapshot", {
        "kind": ["ekyc.session", "ekyc.session"],
        "tenant_id": ["t1", "t1"],
        "entity_id": ["ky-1", "ky-2"],
        "payload": [
            json.dumps({"id": "ky-1", "member_id": "m1", "state": "DECIDED",
                        "decision": "AUTO_APPROVE", "nin_verified": True,
                        "bvn_verified": True, "liveness_score": 0.95,
                        "created_at": "2024-05-01T10:00:00Z",
                        "decided_at": "2024-05-01T10:05:00Z"}),
            json.dumps({"id": "ky-2", "member_id": "m3", "state": "BIOMETRIC",
                        "decision": "", "nin_verified": True,
                        "bvn_verified": False, "liveness_score": None,
                        "created_at": "2024-05-02T10:00:00Z"}),
        ],
        "updated_at": pa.array([ts(2024, 5, 1, 10), ts(2024, 5, 2, 10)], type=TS),
    })
    write_bronze(lake, "benefit_claim", {
        "id": ["bc-1"],
        "tenant_id": ["t1"],
        "rsa_id": ["r1"],
        "type": ["RETIREMENT"],
        "amount_kobo": [5_000_000_00],
        "state": ["APPROVED"],
        "submitted_by": ["m1"],
        "decided_by": ["staff-1"],
        "decision_reason": [None],
        "rule_version": ["v1"],
        "custodian_ref": [None],
        "submitted_at": pa.array([ts(2024, 5, 15)], type=TS),
        "decided_at": pa.array([ts(2024, 5, 20)], type=TS),
    })


class FixtureLake(unittest.TestCase):
    """Silver + gold SQL against the fixture lake (in-memory DuckDB)."""

    @classmethod
    def setUpClass(cls):
        cls.tmp = Path(tempfile.mkdtemp(prefix="lake-test-"))
        write_fixture_lake(cls.tmp)
        cls.con = duckdb.connect()
        cls.gold = run.build_models(cls.con, cls.tmp)

    @classmethod
    def tearDownClass(cls):
        cls.con.close()
        shutil.rmtree(cls.tmp, ignore_errors=True)

    def q(self, sql):
        return self.con.execute(sql).fetchall()

    def test_gold_model_set(self):
        self.assertEqual(self.gold, [
            "gold_member_growth_daily",
            "gold_contribution_monthly_by_employer",
            "gold_fund_aum_daily",
            "gold_case_sla_compliance",
            "gold_kyc_funnel",
            "gold_employer_remittance_health",
            "gold_retirement_pipeline",
        ])

    def test_silver_members_dedupe_latest_partition_wins(self):
        rows = self.q("SELECT rsa_id, fund_id FROM silver_members ORDER BY rsa_id")
        self.assertEqual(len(rows), 3)
        # dt=2024-06-01 partition must beat dt=2024-05-01 for r2.
        self.assertEqual(dict(rows)["r2"], "FUND_III")

    def test_member_growth(self):
        rows = self.q("SELECT dt, new_members, total_members "
                      "FROM gold_member_growth_daily ORDER BY dt")
        # openings on 3 distinct days: Jan 5, Jan 6, Feb 1
        self.assertEqual(len(rows), 3)
        self.assertEqual(rows[0][1:], (1, 1))
        self.assertEqual(rows[-1][1:], (1, 3))

    def test_contribution_monthly_by_employer(self):
        rows = self.q("SELECT employer_id, month, contributions, gross_kobo, voluntary_kobo "
                      "FROM gold_contribution_monthly_by_employer")
        self.assertEqual(len(rows), 1)
        emp, month, n, gross, vol = rows[0]
        self.assertEqual((emp, str(month)), ("emp-1", "2024-05-01"))
        self.assertEqual((n, gross, vol), (2, 430_000_00, 5_000_00))

    def test_fund_aum_kobo_math(self):
        # 2 units (2e8 raw) at 15000/16000 kobo per unit.
        rows = self.q("SELECT dt, aum_kobo, total_aum_kobo "
                      "FROM gold_fund_aum_daily ORDER BY dt")
        self.assertEqual(rows[0][1:], (2 * 150_00, 2 * 150_00))
        self.assertEqual(rows[1][1:], (2 * 160_00, 2 * 160_00))

    def test_case_sla(self):
        rows = self.q("SELECT case_type, total_cases, breached_cases, sla_compliance_bps "
                      "FROM gold_case_sla_compliance ORDER BY case_type")
        by_type = {r[0]: r[1:] for r in rows}
        self.assertEqual(by_type["COMPLAINT"], (1, 0, 10000))
        self.assertEqual(by_type["FRAUD"], (1, 1, 0))

    def test_kyc_funnel(self):
        rows = {s: n for _, s, n in self.q("SELECT stage_order, stage, sessions FROM gold_kyc_funnel")}
        self.assertEqual(rows["started"], 2)
        self.assertEqual(rows["identity_verified"], 2)
        self.assertEqual(rows["biometric_passed"], 1)
        self.assertEqual(rows["decided"], 1)
        self.assertEqual(rows["auto_approved"], 1)

    def test_employer_health(self):
        rows = self.q("SELECT employer_id, remittance_health, schedules_posted, total_remitted_kobo "
                      "FROM gold_employer_remittance_health ORDER BY employer_id")
        by_emp = {r[0]: r[1:] for r in rows}
        self.assertEqual(by_emp["emp-1"], ("HEALTHY", 1, 430_000_00))
        self.assertEqual(by_emp["emp-2"][0], "DEFAULTING")

    def test_retirement_pipeline(self):
        rows = self.q("SELECT age_band, members, retirement_claims, approved_claims, claim_value_kobo "
                      "FROM gold_retirement_pipeline ORDER BY age_band")
        bands = {r[0]: r[1:] for r in rows}
        self.assertEqual(bands["50+ (retirement-eligible)"], (1, 1, 1, 5_000_000_00))
        self.assertEqual(bands["<40"][0], 1)


class TestExtractHelpers(unittest.TestCase):
    def test_enumerate_tables_from_real_migrations(self):
        tables = extract_full.enumerate_tables(HERE.parent / "db" / "migrations")
        for expected in ("person", "rsa_account", "contribution", "fund_price",
                         "ops_case", "state_snapshot", "benefit_claim"):
            self.assertIn(expected, tables)
        self.assertIn("updated_at", tables["ops_case"]["columns"])
        self.assertIn("gross_kobo", tables["contribution"]["columns"])

    def test_table_name_guard(self):
        with self.assertRaises(ValueError):
            extract_full.extract_table(None, "x; DROP TABLE person", ["id"], "/tmp/x")


class TestGoldExport(unittest.TestCase):
    """Guarded Postgres round-trip: requires TEST_DATABASE_URL."""

    def test_export_roundtrip(self):
        dsn = os.environ.get("TEST_DATABASE_URL", "")
        if not dsn:
            self.skipTest("TEST_DATABASE_URL not set")
        tmp = Path(tempfile.mkdtemp(prefix="lake-export-"))
        self.addCleanup(shutil.rmtree, tmp, True)
        write_fixture_lake(tmp)
        con = duckdb.connect()
        gold = run.build_models(con, tmp)
        counts = run.export_gold(con, dsn, gold)
        con.close()
        self.assertEqual(counts["gold_fund_aum_daily"], 2)
        import psycopg2
        pg = psycopg2.connect(dsn)
        try:
            with pg.cursor() as cur:
                cur.execute("SELECT aum_kobo FROM analytics.gold_fund_aum_daily "
                            "ORDER BY price_date LIMIT 1")
                self.assertEqual(cur.fetchone()[0], 2 * 150_00)
                cur.execute("SELECT count(*) FROM information_schema.tables "
                            "WHERE table_schema='analytics'")
                self.assertGreaterEqual(cur.fetchone()[0], 7)
        finally:
            pg.close()


if __name__ == "__main__":
    unittest.main()
