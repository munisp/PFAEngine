package main

// Geospatial handlers: nearest-branch lookup and per-state coverage. The geo
// engine is PostGIS-backed when DATABASE_URL is set and falls back to
// in-memory haversine otherwise (see internal/geo and docs/GEOSPATIAL.md).
// Branch coordinates are write-through mirrored into geo_point from the
// branchAdd handler via geoUpsertBranch.

import (
	"net/http"
	"strconv"

	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/branch"
	"github.com/munisp/pfaengine/internal/geo"
	"github.com/munisp/pfaengine/internal/httpsrv"
)

// geoBranchesNearest serves GET /api/v1/geo/branches/nearest?lat=&lng=&k=.
func (d *deps) geoBranchesNearest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lat, errLat := strconv.ParseFloat(q.Get("lat"), 64)
	lng, errLng := strconv.ParseFloat(q.Get("lng"), 64)
	if errLat != nil || errLng != nil || !geo.ValidCoord(lat, lng) {
		httpsrv.WriteError(w, http.StatusBadRequest, "VALIDATION",
			errInvalidCoords(q.Get("lat"), q.Get("lng")))
		return
	}
	k := 0 // engine default (5)
	if s := q.Get("k"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil {
			httpsrv.WriteError(w, http.StatusBadRequest, "VALIDATION",
				errBadK(s))
			return
		}
		k = v
	}
	tenant := httpsrv.TenantOf(r)
	var out []geo.BranchDistance
	if d.geo != nil {
		out = d.geo.Nearest(tenant, lat, lng, k)
	}
	if out == nil {
		out = []geo.BranchDistance{}
	}
	d.audit.Append(audit.Record{
		TenantID: tenant, Actor: actorOf(r), Action: "GEO_NEAREST",
		EntityType: "geo", EntityID: "",
		After: map[string]any{"lat": lat, "lng": lng, "k": k, "results": len(out)},
	})
	// Soft data-quality signal only: diaspora members legitimately query
	// from outside Nigeria, so it surfaces as a header, never a rejection.
	if !geo.InNigeria(lat, lng) {
		w.Header().Set("X-Geo-Outside-Nigeria", "1")
	}
	httpsrv.WriteJSON(w, http.StatusOK, out)
}

// geoCoverage serves GET /api/v1/geo/coverage.
func (d *deps) geoCoverage(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	states := []geo.StateCoverage{}
	if d.geo != nil {
		states = d.geo.CoverageByState(tenant)
	}
	d.audit.Append(audit.Record{
		TenantID: tenant, Actor: actorOf(r), Action: "GEO_COVERAGE",
		EntityType: "geo", EntityID: "",
		After: map[string]any{"states": len(states)},
	})
	httpsrv.WriteJSON(w, http.StatusOK, map[string]any{"states": states})
}

// geoUpsertBranch mirrors a branch's coordinates into PostGIS (geo_point).
// Nil-safe: no-op when the geo engine is not wired. Called from branchAdd
// (cmd/member-svc/regulatory.go) after a successful AddBranch — see
// INTEGRATION-geo.md.
func (d *deps) geoUpsertBranch(tenantID string, b branch.Branch) {
	if d.geo == nil {
		return
	}
	// Errors are advisory: the branch registry (source of truth) already
	// accepted the write; a PostGIS hiccup must not fail the request.
	_ = d.geo.UpsertBranchGeo(tenantID, b)
}

// ---- validation helpers ----------------------------------------------------

type validationError string

func (e validationError) Error() string { return string(e) }

func errInvalidCoords(lat, lng string) error {
	return validationError("lat/lng must be valid coordinates (lat -90..90, lng -180..180); got lat=" + lat + " lng=" + lng)
}

func errBadK(k string) error {
	return validationError("k must be an integer (default 5, max 50); got k=" + k)
}
