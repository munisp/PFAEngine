-- Geospatial capability (Tranche: geospatial). PostGIS-backed spatial store.
-- geo_point is the generic spatial index for any tenant-scoped entity
-- (branches today; members/employers/ATMs later). kind='branch' rows are
-- write-through mirrored from the branch registry by internal/geo on every
-- branch add/update, keyed (kind, tenant_id, entity_id) like state_snapshot.
--
-- Nigeria bounding-box sanity: domestic branches must fall inside
-- lat 4..14, lng 2..15 (roughly Chad Basin..Gulf of Guinea). Points outside
-- that box are almost always lat/lng swaps or unit errors; internal/geo
-- soft-flags them (diaspora use cases may legitimately be outside NG), while
-- impossible coordinates (|lat|>90, |lng|>180) are hard-rejected.

CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE IF NOT EXISTS geo_point (
    kind       text        NOT NULL,           -- 'branch' today
    tenant_id  text        NOT NULL,
    entity_id  text        NOT NULL,
    geog       geography(Point, 4326) NOT NULL, -- lon/lat WGS84, meter-accurate distances
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, tenant_id, entity_id)
);

-- GiST spatial index powers the KNN operator (geog <-> point) used by
-- geo.Engine.Nearest; the tenant index keeps per-tenant coverage scans cheap.
CREATE INDEX IF NOT EXISTS geo_point_geog_gist ON geo_point USING gist (geog);
CREATE INDEX IF NOT EXISTS geo_point_tenant_idx ON geo_point (tenant_id, kind);
