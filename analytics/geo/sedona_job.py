"""Apache Sedona batch job: per-state member density + branch coverage gap.

Reads the lakehouse bronze zone (see analytics/extract_full.py —
lake/bronze/<table>/dt=YYYY-MM-DD/*.parquet), spatially joins members and
branches to Nigeria state polygons, and writes a gold coverage table:

  lake/gold/geo_coverage/dt=YYYY-MM-DD/part-*.parquet

Inputs (bronze parquet):
  * member    — operational member registry; uses `state_code` when present,
                else a spatial join of (lat, lng) columns when the member
                feed carries coordinates.
  * geo_point — PostGIS branch mirror (db/migrations/0010_postgis.sql);
                kind='branch' rows carry WGS84 points. Because
                extract_full.py enumerates migrations, this table lands in
                bronze automatically once 0010 is applied.
  * ng_states.geo.json (this directory) — SIMPLIFIED placeholder polygons
                (octagons around state capitals). Swap for GADM/NGA-OSM
                boundaries in production; the job is unchanged.

Output columns (one row per state x tenant):
  tenant_id, state_code, state_name, members, branches, geocoded_branches,
  members_per_branch, area_km2, member_density_per_1000km2,
  coverage_gap_score   -- 0..1; 1 = severely under-served (many members,
                          few/no geocoded branches, large area)

Requirements (NOT installed at import time; see py_compile guard below):
  pip install apache-sedona pyspark==3.5.* pyarrow
  Sedona needs a JVM; spark.jars.packages resolves
  org.apache.sedona:sedona-spark-shaded-3.5_2.12 from Maven.

Run:
  LAKE=./lake SPARK_MASTER=local[*] python3 analytics/geo/sedona_job.py \
      --dt 2025-01-31

This module is import-safe without pyspark/sedona installed so that
`python3 -m py_compile analytics/geo/sedona_job.py` passes in CI and in the
platform repo's lightweight checks. All Spark/Sedona imports happen inside
run().
"""
from __future__ import annotations

import argparse
import json
import logging
import os
from datetime import date
from pathlib import Path

log = logging.getLogger("analytics.geo.sedona")

HERE = Path(__file__).resolve().parent
LAKE = Path(os.environ.get("LAKE", "lake"))


def states_geojson_path() -> Path:
    return HERE / "ng_states.geo.json"


def bronze_glob(table: str) -> str:
    """All partitions of a bronze table (Hive layout from extract_full.py)."""
    return str(LAKE / "bronze" / table / "dt=*" / "*.parquet")


def gold_out(dt: str) -> str:
    return str(LAKE / "gold" / "geo_coverage" / f"dt={dt}")


def latest_partition(path: Path) -> str | None:
    """Return the newest dt= partition directory for a bronze table."""
    root = path / "bronze"
    best: str | None = None
    for tbl in root.glob("*/dt=*"):
        dt = tbl.name.split("=", 1)[1]
        if best is None or dt > best:
            best = dt
    return best


def run(dt: str, lake: Path, master: str) -> None:
    # ---- lazy imports: keep module importable without Spark installed ----
    from pyspark.sql import SparkSession, functions as F  # noqa: PLC0415
    from sedona.spark import SedonaContext  # noqa: PLC0415

    spark = SedonaContext.builder() \
        .appName("pfaengine-geo-coverage") \
        .master(master) \
        .config("spark.sql.parquet.filterPushdown", "true") \
        .getOrCreate()
    SedonaContext.create(spark) if not hasattr(spark, "sedona") else None
    spark.sparkContext.setLogLevel("WARN")

    # ---- state polygons (simplified placeholders; see file metadata) ----
    states = spark.read.format("geojson") \
        .option("multiLine", "true") \
        .load(str(states_geojson_path())) \
        .select(
            F.col("properties.state_code").alias("state_code"),
            F.col("properties.state_name").alias("state_name"),
            F.col("geometry").alias("geom"),
        )
    states = states.withColumn("area_km2", F.expr("ST_Area(ST_Transform(geom, 'epsg:4326', 'epsg:32632')) / 1e6"))
    states.createOrReplaceTempView("states")

    # ---- branches from the PostGIS mirror (kind='branch') ----------------
    branches = spark.read.parquet(bronze_glob("geo_point")) \
        .where("kind = 'branch'")
    # geo_point.geog round-trips as WKB/WKT text via parquet; normalise.
    branches = branches.withColumn(
        "geom", F.expr("ST_GeomFromWKB(CAST(geog AS binary))"))
    branches.createOrReplaceTempView("branches")

    # Spatial join branches -> states.
    spark.sql("""
        SELECT b.tenant_id, s.state_code, s.state_name, b.entity_id AS branch_id
        FROM branches b JOIN states s ON ST_Contains(s.geom, b.geom)
    """).createOrReplaceTempView("branch_in_state")

    branch_counts = spark.sql("""
        SELECT tenant_id, state_code, state_name,
               count(*) AS branches, count(*) AS geocoded_branches
        FROM branch_in_state GROUP BY tenant_id, state_code, state_name
    """)
    branch_counts.createOrReplaceTempView("branch_counts")

    # ---- members: prefer declared state_code, else spatial join ----------
    members = spark.read.parquet(bronze_glob("member"))
    cols = set(members.columns)
    if {"lat", "lng"} <= cols:
        members = members.withColumn(
            "geom", F.expr("ST_Point(CAST(lng AS double), CAST(lat AS double))"))
        members.createOrReplaceTempView("members")
        spark.sql("""
            SELECT m.tenant_id,
                   coalesce(m.state_code, s.state_code) AS state_code,
                   m.pin AS member_id
            FROM members m LEFT JOIN states s
              ON m.geom IS NOT NULL AND ST_Contains(s.geom, m.geom)
        """).createOrReplaceTempView("member_in_state")
    else:
        members.createOrReplaceTempView("members")
        spark.sql("""
            SELECT m.tenant_id, m.state_code, m.pin AS member_id
            FROM members m WHERE m.state_code IS NOT NULL
        """).createOrReplaceTempView("member_in_state")

    member_counts = spark.sql("""
        SELECT tenant_id, state_code, count(*) AS members
        FROM member_in_state GROUP BY tenant_id, state_code
    """)
    member_counts.createOrReplaceTempView("member_counts")

    # ---- gold: density + coverage gap ------------------------------------
    gold = spark.sql("""
        SELECT
            coalesce(m.tenant_id, b.tenant_id)              AS tenant_id,
            s.state_code, s.state_name,
            coalesce(m.members, 0)                          AS members,
            coalesce(b.branches, 0)                         AS branches,
            coalesce(b.geocoded_branches, 0)                AS geocoded_branches,
            CASE WHEN coalesce(b.branches, 0) = 0 THEN m.members
                 ELSE m.members / b.branches END            AS members_per_branch,
            s.area_km2,
            CASE WHEN s.area_km2 > 0
                 THEN coalesce(m.members, 0) / s.area_km2 * 1000.0
                 ELSE 0 END                                 AS member_density_per_1000km2,
            -- Gap score: member pressure per geocoded branch, normalised by
            -- the fleet median; 1.0 = worst-served state in this run.
            CASE WHEN coalesce(b.geocoded_branches, 0) = 0 AND coalesce(m.members, 0) > 0
                 THEN 1.0
                 ELSE least(1.0,
                     (coalesce(m.members, 0) / greatest(b.geocoded_branches, 1))
                     / greatest(avg(coalesce(m.members, 0)) OVER (), 1))
            END                                             AS coverage_gap_score
        FROM states s
        LEFT JOIN member_counts m ON m.state_code = s.state_code
        LEFT JOIN branch_counts b ON b.state_code = s.state_code
                               AND b.tenant_id = m.tenant_id
    """)
    gold.coalesce(1).write.mode("overwrite").parquet(gold_out(dt))
    log.info("geo coverage written to %s", gold_out(dt))
    spark.stop()


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--dt", default=date.today().isoformat(),
                    help="gold partition date (default: today)")
    ap.add_argument("--lake", default=str(LAKE), help="lakehouse root")
    ap.add_argument("--master", default=os.environ.get("SPARK_MASTER", "local[*]"))
    args = ap.parse_args()
    run(args.dt, Path(args.lake), args.master)


def selftest() -> dict:
    """Spark-free sanity check used by CI: GeoJSON parses, 37 features."""
    data = json.loads(states_geojson_path().read_text())
    feats = data.get("features", [])
    assert len(feats) == 37, f"want 37 state features, got {len(feats)}"
    codes = {f["properties"]["state_code"] for f in feats}
    return {"features": len(feats), "codes": sorted(codes)}


if __name__ == "__main__":
    import sys
    logging.basicConfig(level=logging.INFO)
    if "--selftest" in sys.argv:
        print(json.dumps(selftest()))
    else:
        main()
