# Geospatial capability

Status: **implemented (v1)** — PostGIS storage + spatial SQL, Apache Sedona
lakehouse analytics spec, staff map view.

## "Geolibre" — interpretation note

The request asked for "geolibre" map visualization. No product by that exact
name exists in the platform's dependency set; we interpret it as the
**libre/open-source mapping stack**: [MapLibre GL](https://maplibre.org)
(the open fork of Mapbox GL) rendering **OpenStreetMap** tiles. Because
`maplibre-gl` is not yet a dependency of `apps/staff-portal`, the v1 view
(`apps/staff-portal/src/views/BranchMap.tsx`) renders a self-contained SVG
schematic of Nigeria with live data from the geo API — no new build
dependency. Upgrading to real tiles is a drop-in: `npm i maplibre-gl`,
render a `maplibregl.Map` with an OSM raster style, and reuse the view's
existing fetchers/marker model.

## Pre-change state (honest assessment)

- `internal/branch.Branch` already carried `Lat`/`Lng float64` fields, but
  they were **never read by any query** — no spatial logic existed anywhere.
- Zero spatial indexes, zero distance calculations, no PostGIS extension.
- The fraud engine had `Tx.StateCode` (string) for same-state heuristics,
  but nothing coordinate-based.
- The lakehouse (`analytics/`) had no spatial zone or job.

## Post-change capabilities

### 1. PostGIS storage + spatial SQL
- `db/migrations/0010_postgis.sql` — `CREATE EXTENSION IF NOT EXISTS postgis`,
  generic `geo_point(kind, tenant_id, entity_id, geog geography(Point,4326),
  updated_at)` table with a GiST index (powers the `<->` KNN operator).
  Because `analytics/extract_full.py` enumerates migrations, `geo_point`
  automatically lands in `lake/bronze/geo_point/` for analytics.
- `internal/geo` — `geo.New(branchEngine, db)`:
  - `Nearest(tenant, lat, lng, k)` — PostGIS KNN
    (`ORDER BY geog <-> ST_SetSRID(ST_MakePoint(lng,lat),4326)::geography`,
    `ST_Distance` for km) when a database is configured; **in-memory
    haversine** over the branch registry otherwise (and as an automatic
    fallback if PostGIS is unavailable). `k<=0 → 5`, capped at 50.
    Un-geocoded branches are excluded.
  - `CoverageByState(tenant)` — branches per state + geocoded count.
  - `UpsertBranchGeo(tenant, branch)` — write-through upsert into
    `geo_point` inside `store.DB.WithTenant`; nil-safe no-op without a DB.
- Validation: impossible coordinates (`|lat|>90`, `|lng|>180`, NaN/Inf) are
  **hard-rejected** (400 VALIDATION). Points outside the Nigeria bounding
  box (lat 4..14, lng 2..15) are **soft-flagged** only (`X-Geo-Outside-
  Nigeria: 1` response header) — diaspora members legitimately query from
  abroad.

### 2. HTTP surface (member-svc)
- `GET /api/v1/geo/branches/nearest?lat=&lng=&k=` → `200 []BranchDistance`
  (`{branch, distance_km}`, ascending). Audit: `GEO_NEAREST`.
- `GET /api/v1/geo/coverage` → `200 {"states": []StateCoverage}`.
  Audit: `GEO_COVERAGE`.
- Branch adds write through to PostGIS via `deps.geoUpsertBranch` (wired
  into `branchAdd`; errors are advisory and never fail the request).

### 3. Apache Sedona lakehouse analytics (spec)
- `analytics/geo/sedona_job.py` — Spark + Sedona batch job: reads bronze
  `member` and `geo_point` parquet, spatial-joins against
  `analytics/geo/ng_states.geo.json` (**simplified** octagonal placeholder
  polygons around state capitals — replace with GADM/NGA-OSM boundaries for
  production), computes per-state member density and a `coverage_gap_score`,
  writes `lake/gold/geo_coverage/dt=YYYY-MM-DD/`. Import-guarded so
  `python3 -m py_compile` passes without Spark; `--selftest` validates the
  GeoJSON (37 features) with no Spark dependency.
- Requirements: `pip install apache-sedona pyspark==3.5.* pyarrow` (JVM
  required; Sedona jars resolved from Maven via `spark.jars.packages`).

### 4. Staff map view
- `apps/staff-portal/src/views/BranchMap.tsx` — SVG schematic Nigeria map
  (lat/lng projected), branch markers, click-to-query nearest branches with
  distances, per-state coverage table with gap highlighting. No new
  dependencies; self-contained fetchers (`api.ts` untouched).

## Production roadmap

1. **Geocoding** — branches currently carry hand-entered lat/lng. Self-host
   [Nominatim](https://nominatim.org) (OSM) for address → coordinate
   resolution at branch registration; batch backfill existing branches
   (`CoverageByState.has_geo` tracks the gap).
2. **Routing** — distances are great-circle; drive-time nearest-branch
   needs the team's OpenRide/BRouter engine (OSRM-compatible profiles).
3. **Member home-branch assignment** — nearest-branch at onboarding from the
   member's verified address; feeds book-a-visit defaults.
4. **Fraud geo-velocity** — extend `fraud.Tx.StateCode` string checks with
   coordinate pairs: flag transactions whose implied speed between
   consecutive locations exceeds physical possibility.
5. **Real boundaries + tiles** — swap the simplified GeoJSON for GADM level-1
   state polygons; add `maplibre-gl` + an OSM tile source to the staff
   portal; consider a self-hosted tile server (Martin/pg_tileserv directly
   off `geo_point`).
6. **Sedona at scale** — schedule `sedona_job.py` daily after bronze
   extraction; add employer and agent layers to `geo_point`
   (`kind IN ('employer','agent')`).

## Testing

- `go test ./internal/geo/` — haversine sanity (Lagos Island → Ikeja ≈ 18 km),
  ordering, k default/cap, un-geocoded exclusion, tenant isolation, coverage
  counts, coordinate validation. PostGIS round-trip test runs when
  `TEST_DATABASE_URL` is set (skips cleanly otherwise, including when the
  extension can't be created).
- `python3 -m py_compile analytics/geo/sedona_job.py` and
  `python3 analytics/geo/sedona_job.py --selftest`.
- `tsc -b` + `oxlint` on the staff portal.
