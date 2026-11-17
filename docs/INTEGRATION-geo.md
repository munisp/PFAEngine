# INTEGRATION-geo — lead wiring checklist

Geo capability delivered by the geospatial subagent. Files owned by that
agent (already in tree, do not rewrite):

- `db/migrations/0010_postgis.sql`
- `internal/geo/geo.go`, `internal/geo/geo_test.go`
- `cmd/member-svc/geo.go` (handlers + `geoUpsertBranch` helper)
- `analytics/geo/sedona_job.py`, `analytics/geo/ng_states.geo.json`
- `apps/staff-portal/src/views/BranchMap.tsx`
- `docs/GEOSPATIAL.md`

Status legend: ✅ already present in the tree at time of writing (verified);
⬜ still to apply.

## 1. deps field — ✅ present (cmd/member-svc/main.go ~line 165)

```go
// Geospatial engine: PostGIS-backed when DATABASE_URL is set, in-memory
// haversine otherwise. Wraps the branch registry.
geo *geo.Engine
```

Import (✅ present, line 42):

```go
"github.com/munisp/pfaengine/internal/geo"
```

## 2. Constructor — ✅ present

`geoDB *store.DB` is declared alongside the other repos (line ~193) and set
inside the existing `if dsn := httpsrv.Getenv("DATABASE_URL", ""); dsn != ""`
block (line ~200: `geoDB = db`). It stays nil when DATABASE_URL is unset —
the engine then runs the in-memory haversine path. Then (line ~315, after
`d.branch = branch.New()`):

```go
d.geo = geo.New(d.branch, geoDB)
```

## 3. apiRoutes — ✅ present (lines 607–608)

```go
{"GET", "/api/v1/geo/branches/nearest", func(d *deps) http.HandlerFunc { return d.geoBranchesNearest }},
{"GET", "/api/v1/geo/coverage", func(d *deps) http.HandlerFunc { return d.geoCoverage }},
```

## 4. api/openapi.yaml — ✅ present (lines 873–882), matches house style

```yaml
  /v1/geo/branches/nearest:
    get:
      summary: "Geo: nearest branches by lat/lng (PostGIS KNN, haversine fallback)"
      tags: [Geo]
      responses: { '200': { description: OK } }
  /v1/geo/coverage:
    get:
      summary: "Geo: branch coverage per state"
      tags: [Geo]
      responses: { '200': { description: OK } }
```

## 5. branchAdd write-through — ⬜ TO APPLY

In `cmd/member-svc/regulatory.go`, `func (d *deps) branchAdd` (line ~152),
insert one line between the audit append and the response:

```go
	d.auditReg(r, "branch.added", "branch", b.ID, b.Name)
	d.geoUpsertBranch(httpsrv.TenantOf(r), b) // PostGIS write-through (nil-safe; errors advisory)
	httpsrv.WriteJSON(w, 201, b)
```

`geoUpsertBranch` (defined in `cmd/member-svc/geo.go`) nil-guards `d.geo`
and ignores upsert errors, so it cannot fail the request.

## 6. Migration — ⬜ apply on next deploy

`db/migrations/0010_postgis.sql` follows the numbered sequence after
`0009_snapshots.sql`. Note it runs `CREATE EXTENSION IF NOT EXISTS postgis`,
which requires the PostGIS-enabled Postgres image (e.g.
`postgis/postgis:18-3.5`) or the extension package installed — flag for
`deploy/docker-compose.yml` if the db service uses vanilla `postgres:18`.

## 7. Staff portal nav — ⬜ optional (shared file, left to lead)

`apps/staff-portal/src/App.tsx`: add `'branchmap'` to the view union,
a nav button, and `{view === 'branchmap' && <BranchMap api={api} />}` with
`import BranchMap from './views/BranchMap';`.
