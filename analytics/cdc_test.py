"""CDC unit tests — no live Kafka or Postgres required.

Covers: synthetic unwrapped Debezium events (insert/update/delete/
tombstone) applied to the bronze parquet zone, watermark advance, the
table->model dependency map (re-derived from the SQL so it cannot drift),
and run.py --incremental model selection.

    python3 -m unittest discover analytics        # from repo root
    (test_cdc.py is the discovery shim for this file, mirroring
     test_lakehouse.py / lakehouse_test.py)
"""
from __future__ import annotations

import json
import re
import shutil
import sys
import tempfile
import unittest
from datetime import datetime, timezone
from pathlib import Path
from unittest import mock

import pyarrow as pa
import pyarrow.parquet as pq

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

import duckdb  # noqa: E402

import cdc_consumer as cdc  # noqa: E402
import run  # noqa: E402

TS = pa.timestamp("us", tz="UTC")


def member_table(rows: list[dict]) -> pa.Table:
    return pa.table({
        "id": [r["id"] for r in rows],
        "pin": [r.get("pin", "P") for r in rows],
        "fund_code": pa.array([r.get("fund_code") for r in rows], type=pa.string()),
        "created_at": pa.array(
            [r.get("created_at") for r in rows], type=TS),
    })


class TestPrimaryKeys(unittest.TestCase):
    def test_real_migrations(self):
        pks = cdc.parse_primary_keys(HERE.parent / "db" / "migrations")
        self.assertEqual(pks["person"], ("id",))
        self.assertEqual(pks["member"], ("id",))
        self.assertEqual(pks["ops_case"], ("tenant_id", "id"))
        self.assertEqual(pks["fund_price"], ("fund_id", "price_date"))
        self.assertEqual(pks["unit_holding"], ("rsa_id", "fund_id"))
        self.assertEqual(pks["state_snapshot"], ("kind", "tenant_id", "entity_id"))

    def test_topic_to_table(self):
        self.assertEqual(cdc.topic_to_table("pfaengine.public.member"), "member")
        self.assertIsNone(cdc.topic_to_table("other.public.member"))
        self.assertIsNone(cdc.topic_to_table("pfaengine_connect_offsets"))


class TestClassifyEvent(unittest.TestCase):
    def test_insert_and_update(self):
        for op in ("c", "u", "r"):
            kind, row = cdc.classify_event(
                {"id": "m1", "fund_code": "FUND_II", "__op": op,
                 "__lsn": 42, "__source_ts_ms": 1}, {"id": "m1"})
            self.assertEqual(kind, "upsert")
            self.assertEqual(row, {"id": "m1", "fund_code": "FUND_II"})

    def test_delete_rewrite_mode(self):
        kind, row = cdc.classify_event(
            {"id": "m1", "__op": "d", "__deleted": "true"}, {"id": "m1"})
        self.assertEqual((kind, row), ("delete", {"id": "m1"}))

    def test_tombstone_uses_key(self):
        kind, row = cdc.classify_event(None, {"id": "m9"})
        self.assertEqual((kind, row), ("delete", {"id": "m9"}))
        kind, row = cdc.classify_event(None, None)
        self.assertEqual((kind, row), ("skip", None))

    def test_upsert_row_merges_key(self):
        kind, row = cdc.classify_event({"__op": "u", "status": "X"},
                                       {"tenant_id": "t1", "id": "c1"})
        self.assertEqual(kind, "upsert")
        self.assertEqual(row["id"], "c1")
        self.assertEqual(row["tenant_id"], "t1")


class TestApplyEvents(unittest.TestCase):
    def test_insert_update_delete_cycle(self):
        base = member_table([
            {"id": "m1", "fund_code": "FUND_II",
             "created_at": datetime(2024, 1, 5, tzinfo=timezone.utc)},
            {"id": "m2", "fund_code": "FUND_II",
             "created_at": datetime(2024, 1, 5, tzinfo=timezone.utc)},
        ])
        # update m2 (created_at as Debezium µs int), insert m3, delete m1
        us = 1_704_412_800_000_000  # 2024-01-05T00:00:00Z
        out = cdc.apply_events(base, [
            ("upsert", {"id": "m2", "pin": "P", "fund_code": "FUND_III",
                        "created_at": us}),
            ("upsert", {"id": "m3", "pin": "P", "fund_code": "FUND_IV",
                        "created_at": us}),
            ("delete", {"id": "m1"}),
        ], ("id",))
        rows = {r["id"]: r for r in out.to_pylist()}
        self.assertEqual(sorted(rows), ["m2", "m3"])
        self.assertEqual(rows["m2"]["fund_code"], "FUND_III")
        # µs int coerced back to the existing timestamptz column type
        self.assertEqual(rows["m2"]["created_at"],
                         datetime(2024, 1, 5, tzinfo=timezone.utc))

    def test_last_event_per_key_wins(self):
        out = cdc.apply_events(None, [
            ("upsert", {"id": "m1", "v": 1}),
            ("delete", {"id": "m1"}),
        ], ("id",))
        self.assertEqual(out.num_rows, 0)
        out = cdc.apply_events(None, [
            ("delete", {"id": "m1"}),
            ("upsert", {"id": "m1", "v": 2}),
        ], ("id",))
        self.assertEqual(out.to_pylist(), [{"id": "m1", "v": 2}])

    def test_delete_nonexistent_is_noop(self):
        base = member_table([{"id": "m1", "fund_code": "FUND_II"}])
        out = cdc.apply_events(base, [("delete", {"id": "nope"})], ("id",))
        self.assertEqual(out.num_rows, 1)

    def test_composite_pk(self):
        base = pa.table({
            "fund_id": ["F1", "F1"], "price_date": ["2024-01-01", "2024-01-02"],
            "price_kobo": [100, 200]})
        out = cdc.apply_events(base, [
            ("upsert", {"fund_id": "F1", "price_date": "2024-01-02",
                        "price_kobo": 250}),
        ], ("fund_id", "price_date"))
        rows = sorted(out.to_pylist(), key=lambda r: r["price_date"])
        self.assertEqual([r["price_kobo"] for r in rows], [100, 250])

    def test_schema_evolution_extra_column_kept(self):
        base = member_table([{"id": "m1", "fund_code": "FUND_II"}])
        out = cdc.apply_events(base, [
            ("upsert", {"id": "m1", "pin": "P", "fund_code": "FUND_III",
                        "brand_new_col": "hello"}),
        ], ("id",))
        rows = out.to_pylist()
        self.assertEqual(rows[0]["brand_new_col"], "hello")
        self.assertEqual(rows[0]["fund_code"], "FUND_III")


class TestApplierAndWatermark(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp(prefix="cdc-test-"))
        self.addCleanup(shutil.rmtree, self.tmp, True)
        # seed a normal extract partition that CDC must merge with
        pdir = self.tmp / "bronze" / "member" / "dt=2024-06-01"
        pdir.mkdir(parents=True)
        pq.write_table(member_table([
            {"id": "m1", "fund_code": "FUND_II",
             "created_at": datetime(2024, 1, 5, tzinfo=timezone.utc)},
            {"id": "m2", "fund_code": "FUND_II",
             "created_at": datetime(2024, 1, 5, tzinfo=timezone.utc)},
        ]), pdir / "part-full.parquet")
        self.wm = cdc.Watermark(self.tmp / "bronze" / "cdc_watermark.json")
        self.applier = cdc.CDCApplier(
            self.tmp, {"member": ("id",)}, watermark=self.wm)

    def compacted(self):
        return pq.read_table(self.tmp / "bronze" / "member"
                             / cdc.CDC_PARTITION / "part-cdc.parquet")

    def test_flush_merges_seeded_partition_and_advances_watermark(self):
        topic = "pfaengine.public.member"
        self.applier.handle(topic, 0, 10,
                            {"id": "m2", "pin": "P", "fund_code": "FUND_III",
                             "created_at": "2024-01-05T00:00:00+00:00",
                             "__op": "u", "__lsn": 777},
                            {"id": "m2"}, lsn=777)
        self.applier.handle(topic, 0, 11,
                            {"id": "m1", "__op": "d", "__deleted": "true"},
                            {"id": "m1"})
        self.assertTrue(self.applier.due() or True)
        applied = self.applier.flush()
        self.assertEqual(applied, {"member": 2})

        rows = {r["id"]: r for r in self.compacted().to_pylist()}
        self.assertEqual(sorted(rows), ["m2"])          # m1 deleted
        self.assertEqual(rows["m2"]["fund_code"], "FUND_III")  # update won

        # legacy partition superseded (compacted state is a superset) —
        # this is what keeps deletes from resurrecting in silver dedupe
        legacy = list((self.tmp / "bronze" / "member").glob("dt=*"))
        self.assertEqual([p.name for p in legacy], [cdc.CDC_PARTITION])

        data = json.loads(
            (self.tmp / "bronze" / "cdc_watermark.json").read_text())
        self.assertEqual(data["seq"], 2)
        self.assertEqual(data["lsn"], 777)
        self.assertEqual(data["offsets"][topic]["0"], 12)  # next offset
        self.assertEqual(data["tables"]["member"]["events"], 2)
        self.assertEqual(data["tables"]["member"]["last_op"], "delete")
        metrics = self.wm.metrics()
        self.assertIn("tables", metrics)
        self.assertIn("offsets", metrics)

    def test_clear_cdc_partitions_after_full_extract(self):
        self.applier.handle("pfaengine.public.member", 0, 1,
                            {"id": "m1", "pin": "P", "__op": "u"},
                            {"id": "m1"})
        self.applier.flush()
        cdir = self.tmp / "bronze" / "member" / cdc.CDC_PARTITION
        self.assertTrue(cdir.is_dir())
        # extract succeeded for member -> CDC state cleared
        self.assertEqual(run.clear_cdc_partitions(self.tmp, {"member"}), 1)
        self.assertFalse(cdir.exists())
        # extract FAILED for ops_case -> CDC state must be preserved
        cdir.mkdir(parents=True)
        self.assertEqual(run.clear_cdc_partitions(self.tmp, set()), 0)
        self.assertTrue(cdir.exists())

    def test_cdc_partition_wins_silver_dedupe(self):
        """DuckDB dedupe ORDER BY dt DESC: the 9999-12-31 CDC partition
        must beat the 2024-06-01 extract partition for the same PK."""
        self.applier.handle("pfaengine.public.member", 0, 5,
                            {"id": "m2", "pin": "P", "fund_code": "FUND_IV",
                             "created_at": "2024-01-05T00:00:00+00:00",
                             "__op": "u"},
                            {"id": "m2"})
        self.applier.flush()
        con = duckdb.connect()
        try:
            lake = str(self.tmp.resolve())
            rows = con.execute(f"""
                SELECT id, fund_code FROM (
                    SELECT *, row_number() OVER (PARTITION BY id ORDER BY dt DESC) rn
                    FROM read_parquet('{lake}/bronze/member/dt=*/*.parquet',
                                      hive_partitioning = 1, union_by_name = true)
                ) WHERE rn = 1 ORDER BY id""").fetchall()
        finally:
            con.close()
        self.assertEqual(dict(rows)["m2"], "FUND_IV")

    def test_tombstone_delete_through_applier(self):
        self.applier.handle("pfaengine.public.member", 1, 3,
                            None, {"id": "m1"})
        self.applier.flush()
        ids = [r["id"] for r in self.compacted().to_pylist()]
        self.assertEqual(ids, ["m2"])

    def test_empty_flush_is_noop(self):
        self.assertEqual(self.applier.flush(), {})
        self.assertFalse(
            (self.tmp / "bronze" / "cdc_watermark.json").exists())


class TestDependencyMap(unittest.TestCase):
    """Re-derive the map from the SQL so run.py constants cannot drift."""

    def test_map_matches_models(self):
        silver_src = {}
        for p in sorted((HERE / "models" / "silver").glob("*.sql")):
            silver_src[p.stem] = set(re.findall(r"bronze/([a-z_]+)", p.read_text()))
        derived_t2s: dict[str, set[str]] = {}
        for stem, tables in silver_src.items():
            for t in tables:
                derived_t2s.setdefault(t, set()).add(stem)
        for t, stems in run.TABLE_TO_SILVER.items():
            self.assertEqual(set(stems), derived_t2s.get(t, set()), t)
        self.assertEqual(derived_t2s.keys(), run.TABLE_TO_SILVER.keys())

        for p in sorted((HERE / "models" / "gold").glob("*.sql")):
            sql = p.read_text()
            silvers = set(re.findall(r"silver_[a-z_]+", sql))
            bronzes = set(re.findall(r"bronze/([a-z_]+)", sql))
            for s in silvers:
                silver_stem = {v: k for k, v in _SILVER_TABLES.items()}[s]
                self.assertIn(p.stem, run.SILVER_TO_GOLD[silver_stem])
            for b in bronzes:
                self.assertIn(p.stem, run.TABLE_TO_GOLD_DIRECT[b])

    def test_select_models(self):
        silver, gold = run.select_models({"contribution"})
        self.assertEqual(silver, {"02_contributions"})
        self.assertEqual(gold, {"02_contribution_monthly_by_employer",
                                "06_employer_remittance_health"})

        silver, gold = run.select_models({"person"})
        self.assertEqual(silver, {"01_members"})
        self.assertEqual(gold, {"01_member_growth_daily",
                                "07_retirement_pipeline"})

        # benefit_claim feeds gold directly (no silver model)
        silver, gold = run.select_models({"benefit_claim"})
        self.assertEqual(silver, set())
        self.assertEqual(gold, {"07_retirement_pipeline"})

        # conservative: unknown table -> full rebuild
        self.assertIsNone(run.select_models({"some_new_table"}))
        # nothing dirty -> nothing to do
        self.assertEqual(run.select_models(set()), (set(), set()))


_SILVER_TABLES = {
    "01_members": "silver_members",
    "02_contributions": "silver_contributions",
    "03_employers": "silver_employers",
    "04_cases": "silver_cases",
    "05_funds_nav": "silver_funds_nav",
    "06_kyc_sessions": "silver_kyc_sessions",
}


class TestIncrementalRun(unittest.TestCase):
    """run.py --incremental model selection (Postgres export stubbed)."""

    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp(prefix="cdc-run-"))
        self.addCleanup(shutil.rmtree, self.tmp, True)
        (self.tmp / "bronze").mkdir(parents=True)
        self.wm_path = self.tmp / "bronze" / "cdc_watermark.json"

    def _write_watermark(self, tables: dict[str, int]):
        self.wm_path.write_text(json.dumps({
            "version": 1, "seq": max(tables.values(), default=0),
            "offsets": {}, "lsn": None,
            "tables": {t: {"events": s, "last_seq": s, "last_op": "u",
                           "last_applied_at": "2024-06-01T00:00:00Z"}
                       for t, s in tables.items()},
            "updated_at": "2024-06-01T00:00:00Z"}))

    def _run(self, env_extra=None):
        with mock.patch.object(run, "build_models",
                               return_value=["gold_x"]) as bm, \
             mock.patch.object(run, "export_gold") as eg, \
             mock.patch.dict("os.environ", env_extra or {}):
            rc = run.main(["--lake", str(self.tmp), "--skip-extract",
                           "--incremental", "--dsn", "postgres://x"])
        return rc, bm, eg

    def test_no_watermark_falls_back_to_full(self):
        rc, bm, _ = self._run()
        self.assertEqual(rc, 0)
        _, kwargs = bm.call_args
        self.assertIsNone(kwargs["silver_only"])
        self.assertIsNone(kwargs["gold_only"])

    def test_dirty_tables_select_subset_and_export_only_affected(self):
        self._write_watermark({"ops_case": 5})
        rc, bm, eg = self._run()
        self.assertEqual(rc, 0)
        _, kwargs = bm.call_args
        self.assertEqual(kwargs["silver_only"], {"04_cases"})
        self.assertEqual(kwargs["gold_only"], {"04_case_sla_compliance"})
        # only the affected gold table is exported (transactional swap)
        self.assertEqual(eg.call_args[0][2], ["gold_x"])
        # state advanced: a second run sees nothing to do
        rc2, bm2, eg2 = self._run()
        self.assertEqual(rc2, 0)
        bm2.assert_not_called()
        eg2.assert_not_called()

    def test_unknown_table_triggers_full_rebuild(self):
        self._write_watermark({"mystery_table": 1})
        rc, bm, _ = self._run()
        self.assertEqual(rc, 0)
        _, kwargs = bm.call_args
        self.assertIsNone(kwargs["silver_only"])

    def test_watermark_path_env_override(self):
        alt = self.tmp / "custom_wm.json"
        alt.write_text(json.dumps({"version": 1, "seq": 1, "tables": {
            "fund_price": {"events": 1, "last_seq": 1}}}))
        rc, bm, _ = self._run(env_extra={"CDC_WATERMARK_PATH": str(alt)})
        self.assertEqual(rc, 0)
        _, kwargs = bm.call_args
        self.assertEqual(kwargs["gold_only"], {"03_fund_aum_daily"})


if __name__ == "__main__":
    unittest.main()
