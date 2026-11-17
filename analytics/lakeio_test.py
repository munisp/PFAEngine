"""lakeio unit tests — OpenDAL-backed lake + unchanged local fallback.

Runs entirely offline: the OpenDAL side uses the memory:// and fs://
backends (skipped cleanly if the `opendal` wheel is not installed).

    python3 -m unittest discover analytics        # from repo root
    (test_lakeio.py is the discovery shim for this file, mirroring
     test_lakehouse.py / lakehouse_test.py)
"""
from __future__ import annotations

import shutil
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import pyarrow as pa

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

import cdc_consumer as cdc  # noqa: E402
import lakeio  # noqa: E402
import run  # noqa: E402

try:
    import opendal  # noqa: F401
    HAVE_OPENDAL = True
except ImportError:
    HAVE_OPENDAL = False

requires_opendal = unittest.skipUnless(HAVE_OPENDAL, "opendal package not installed")

TABLE = pa.table({"id": ["m1", "m2"], "v": [1, 2]})


def _roundtrip(lio: lakeio.LakeIO):
    """Shared round-trip contract for every backend."""
    lio.write_text("bronze/member/_watermark.json", '{"watermark": "x"}')
    assert lio.exists("bronze/member/_watermark.json")
    assert lio.read_text("bronze/member/_watermark.json") == '{"watermark": "x"}'

    lio.write_parquet(TABLE, "bronze/member/dt=2024-06-01/part-full.parquet")
    lio.write_parquet(TABLE, "bronze/member/dt=2024-06-02/part-full.parquet")
    got = lio.read_parquet("bronze/member/dt=2024-06-01/part-full.parquet")
    assert got.equals(TABLE)

    files = lio.glob("bronze/member/dt=*/*.parquet")
    assert files == ["bronze/member/dt=2024-06-01/part-full.parquet",
                     "bronze/member/dt=2024-06-02/part-full.parquet"], files
    assert lio.list_dirs("bronze/member") == ["dt=2024-06-01", "dt=2024-06-02"]
    assert lio.dir_exists("bronze/member/dt=2024-06-01")
    assert not lio.dir_exists("bronze/nope/dt=2024-06-01")

    # replace (tmp+rename contract used by CDC/watermark saves)
    lio.write_text("bronze/wm.tmp", "v2")
    lio.replace("bronze/wm.tmp", "bronze/wm.json")
    assert not lio.exists("bronze/wm.tmp")
    assert lio.read_text("bronze/wm.json") == "v2"

    lio.remove_dir("bronze/member/dt=2024-06-01")
    assert lio.glob("bronze/member/dt=*/*.parquet") == [
        "bronze/member/dt=2024-06-02/part-full.parquet"]
    lio.remove_dir("bronze/member/dt=2024-06-01")  # missing: no-op


class TestLocalFallback(unittest.TestCase):
    """LAKE_URI unset: identical local-FS behaviour as before."""

    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp(prefix="lakeio-local-"))
        self.addCleanup(shutil.rmtree, self.tmp, True)

    def test_roundtrip(self):
        with mock.patch.dict("os.environ", {}, clear=False):
            import os
            os.environ.pop("LAKE_URI", None)
            lio = lakeio.open_lake(self.tmp)
            self.assertIsInstance(lio, lakeio.LocalLake)
            self.assertFalse(lio.is_remote)
            _roundtrip(lio)
        # bytes really landed on the local disk at the same layout as before
        self.assertTrue(
            (self.tmp / "bronze" / "member" / "dt=2024-06-02"
             / "part-full.parquet").exists())

    def test_local_root(self):
        with mock.patch.dict("os.environ", {}, clear=False):
            import os
            os.environ.pop("LAKE_URI", None)
            lio = lakeio.open_lake(self.tmp)
            self.assertEqual(lio.local_root(), self.tmp)

    def test_traversal_guard(self):
        lio = lakeio.LocalLake(self.tmp)
        for bad in ("../x", "/abs", "a/../b", "a//b"):
            with self.assertRaises(ValueError):
                lio.write_bytes(bad, b"x")


@requires_opendal
class TestOpenDALMemory(unittest.TestCase):
    def test_roundtrip(self):
        lio = lakeio.open_lake("memory://")
        self.assertIsInstance(lio, lakeio.OpenDALLake)
        self.assertTrue(lio.is_remote)
        _roundtrip(lio)

    def test_local_root_raises(self):
        lio = lakeio.open_lake("memory://")
        with self.assertRaises(RuntimeError):
            lio.local_root()

    def test_lake_uri_env_selects_opendal(self):
        with mock.patch.dict("os.environ", {"LAKE_URI": "memory://"}):
            lio = lakeio.open_lake(Path("/nonexistent-local"))
            self.assertIsInstance(lio, lakeio.OpenDALLake)

    def test_bad_scheme_rejected(self):
        with self.assertRaises(ValueError):
            lakeio.open_lake("ftp://nope")


@requires_opendal
class TestOpenDALFs(unittest.TestCase):
    def test_roundtrip(self):
        tmp = Path(tempfile.mkdtemp(prefix="lakeio-fs-"))
        self.addCleanup(shutil.rmtree, tmp, True)
        lio = lakeio.open_lake(f"fs://{tmp}")
        self.assertIsInstance(lio, lakeio.OpenDALLake)
        _roundtrip(lio)
        # fs:// stays DuckDB-compatible: local_root maps back to the path
        self.assertEqual(lio.local_root(), tmp)
        self.assertTrue(
            (tmp / "bronze" / "member" / "dt=2024-06-02"
             / "part-full.parquet").exists())


@requires_opendal
class TestCDConMemoryLake(unittest.TestCase):
    """The CDC apply path works end-to-end against an OpenDAL lake."""

    def test_seed_apply_delete_compact(self):
        lio = lakeio.open_lake("memory://")
        # seed a legacy extract partition
        lio.write_parquet(TABLE, "bronze/member/dt=2024-06-01/part-full.parquet")

        wm = cdc.Watermark("bronze/cdc_watermark.json", lio)
        applier = cdc.CDCApplier(lio, {"member": ("id",)}, watermark=wm)
        applier.handle("pfaengine.public.member", 0, 10,
                       {"id": "m2", "v": 22, "__op": "u"}, {"id": "m2"})
        applier.handle("pfaengine.public.member", 0, 11,
                       {"id": "m1", "__op": "d"}, {"id": "m1"})
        applied = applier.flush()
        self.assertEqual(applied, {"member": 2})

        compacted = lio.read_parquet(
            f"bronze/member/{cdc.CDC_PARTITION}/part-cdc.parquet")
        rows = {r["id"]: r for r in compacted.to_pylist()}
        self.assertEqual(sorted(rows), ["m2"])      # m1 deleted
        self.assertEqual(rows["m2"]["v"], 22)       # update won

        # legacy partition superseded
        self.assertEqual(lio.list_dirs("bronze/member"), [cdc.CDC_PARTITION])

        # watermark lives inside the lake
        self.assertTrue(lio.exists("bronze/cdc_watermark.json"))
        import json
        wm_data = json.loads(lio.read_text("bronze/cdc_watermark.json"))
        self.assertEqual(wm_data["seq"], 2)

        # run.py helpers operate on the same lake
        self.assertEqual(run.dirty_tables_since_last_run(lio), {"member"})
        run.mark_incremental_run(lio)
        self.assertEqual(run.dirty_tables_since_last_run(lio), set())
        self.assertEqual(run.clear_cdc_partitions(lio, {"member"}), 1)
        self.assertFalse(lio.dir_exists(f"bronze/member/{cdc.CDC_PARTITION}"))


if __name__ == "__main__":
    unittest.main()
