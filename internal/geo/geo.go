// Package geo implements the platform geospatial capability: nearest-branch
// lookup, per-state coverage analytics and PostGIS write-through persistence
// (db/migrations/0010_postgis.sql).
//
// Storage strategy: when a *store.DB is configured (DATABASE_URL set), the
// engine keeps a spatially-indexed mirror of branch coordinates in the
// geo_point table and answers Nearest with the PostGIS geography KNN operator
// (<->) — accurate meter-level distances over a GiST index. Without a
// database it degrades gracefully to an in-memory haversine scan over the
// branch registry, so the API contract never changes with deployment mode.
package geo

import (
	"context"
	"database/sql"
	"math"
	"sort"

	"github.com/munisp/pfaengine/internal/branch"
	"github.com/munisp/pfaengine/internal/store"
)

const (
	// earthRadiusKm is the mean Earth radius used by the haversine fallback.
	earthRadiusKm = 6371.0088

	// defaultK / maxK bound the nearest-branch result set.
	defaultK = 5
	maxK     = 50

	// Nigeria bounding box (approximate): lat 4..14, lng 2..15. Points
	// outside it are soft-flagged (likely lat/lng swaps) but not rejected —
	// diaspora members and correspondent offices legitimately sit abroad.
	ngMinLat, ngMaxLat = 4.0, 14.0
	ngMinLng, ngMaxLng = 2.0, 15.0
)

// BranchDistance pairs a branch with its distance from a query point.
type BranchDistance struct {
	Branch     branch.Branch `json:"branch"`
	DistanceKm float64       `json:"distance_km"`
}

// StateCoverage summarises the branch network within one state.
type StateCoverage struct {
	State    string `json:"state"`
	Branches int    `json:"branches"`
	HasGeo   int    `json:"has_geo"` // branches with non-zero lat/lng
}

// Engine resolves geospatial queries for a tenant.
type Engine struct {
	br *branch.Engine
	db *store.DB // nil → in-memory haversine path
}

// New returns the engine. db may be nil (in-memory fallback mode).
func New(br *branch.Engine, db *store.DB) *Engine { return &Engine{br: br, db: db} }

// ValidCoord reports whether lat/lng are physically possible coordinates.
func ValidCoord(lat, lng float64) bool {
	return !math.IsNaN(lat) && !math.IsNaN(lng) &&
		!math.IsInf(lat, 0) && !math.IsInf(lng, 0) &&
		lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180
}

// InNigeria reports whether the point falls inside the Nigerian bounding
// box (lat 4..14, lng 2..15). Used as a soft data-quality signal only.
func InNigeria(lat, lng float64) bool {
	return lat >= ngMinLat && lat <= ngMaxLat && lng >= ngMinLng && lng <= ngMaxLng
}

// clampK normalises the requested result count.
func clampK(k int) int {
	if k <= 0 {
		return defaultK
	}
	if k > maxK {
		return maxK
	}
	return k
}

// Nearest returns the k closest branches to (lat,lng), ascending by
// distance. With a database it uses the PostGIS KNN operator; otherwise an
// in-memory haversine scan. Un-geocoded branches are excluded.
func (e *Engine) Nearest(tenantID string, lat, lng float64, k int) []BranchDistance {
	k = clampK(k)
	if e.db != nil {
		if out, err := e.nearestPostGIS(tenantID, lat, lng, k); err == nil {
			return out
		}
		// Fall through to haversine on any PostGIS error (extension not
		// installed, migration pending) so reads stay available.
	}
	return e.nearestHaversine(tenantID, lat, lng, k)
}

// CoverageByState counts branches per state and how many carry coordinates.
func (e *Engine) CoverageByState(tenantID string) []StateCoverage {
	if e.br == nil {
		return []StateCoverage{}
	}
	byState := map[string]*StateCoverage{}
	for _, b := range e.br.Branches(tenantID, "") {
		sc := byState[b.State]
		if sc == nil {
			sc = &StateCoverage{State: b.State}
			byState[b.State] = sc
		}
		sc.Branches++
		if b.Lat != 0 || b.Lng != 0 {
			sc.HasGeo++
		}
	}
	out := make([]StateCoverage, 0, len(byState))
	for _, sc := range byState {
		out = append(out, *sc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].State < out[j].State })
	return out
}

// UpsertBranchGeo write-through mirrors a branch's coordinates into
// geo_point. No-op when the engine has no database, the branch carries no
// coordinates, or the coordinates are impossible. Returns any PostGIS error
// so callers can log it; callers in hot paths should treat it as advisory.
func (e *Engine) UpsertBranchGeo(tenantID string, b branch.Branch) error {
	if e.db == nil || b.ID == "" || !ValidCoord(b.Lat, b.Lng) || (b.Lat == 0 && b.Lng == 0) {
		return nil
	}
	return e.db.WithTenant(context.Background(), tenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `
			INSERT INTO geo_point (kind, tenant_id, entity_id, geog, updated_at)
			VALUES ('branch', $1, $2, ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography, now())
			ON CONFLICT (kind, tenant_id, entity_id)
			DO UPDATE SET geog = EXCLUDED.geog, updated_at = now()`,
			tenantID, b.ID, b.Lng, b.Lat) // ST_MakePoint takes (lng, lat)
		return err
	})
}

// nearestPostGIS runs the KNN query against geo_point and joins the ordered
// entity IDs back onto the branch registry (the geo_point mirror holds no
// branch attributes beyond the key).
func (e *Engine) nearestPostGIS(tenantID string, lat, lng float64, k int) ([]BranchDistance, error) {
	type hit struct {
		id string
		km float64
	}
	hits := []hit{}
	err := e.db.WithTenant(context.Background(), tenantID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(context.Background(), `
			SELECT entity_id,
			       ST_Distance(geog, ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography) / 1000.0 AS km
			FROM geo_point
			WHERE kind = 'branch' AND tenant_id = $1
			ORDER BY geog <-> ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography
			LIMIT $4`, tenantID, lng, lat, k)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var h hit
			if err := rows.Scan(&h.id, &h.km); err != nil {
				return err
			}
			hits = append(hits, h)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	byID := map[string]branch.Branch{}
	if e.br != nil {
		for _, b := range e.br.Branches(tenantID, "") {
			byID[b.ID] = b
		}
	}
	out := make([]BranchDistance, 0, len(hits))
	for _, h := range hits {
		if b, ok := byID[h.id]; ok {
			out = append(out, BranchDistance{Branch: b, DistanceKm: h.km})
		}
	}
	return out, nil
}

// nearestHaversine scans the in-memory branch registry (O(n); branch
// networks are small — hundreds of locations).
func (e *Engine) nearestHaversine(tenantID string, lat, lng float64, k int) []BranchDistance {
	if e.br == nil {
		return []BranchDistance{}
	}
	all := []BranchDistance{}
	for _, b := range e.br.Branches(tenantID, "") {
		if (b.Lat == 0 && b.Lng == 0) || !ValidCoord(b.Lat, b.Lng) {
			continue
		}
		all = append(all, BranchDistance{Branch: b, DistanceKm: haversineKm(lat, lng, b.Lat, b.Lng)})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].DistanceKm < all[j].DistanceKm })
	if len(all) > k {
		all = all[:k]
	}
	if all == nil {
		all = []BranchDistance{}
	}
	return all
}

// haversineKm returns the great-circle distance in kilometres.
func haversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const deg = math.Pi / 180
	dLat := (lat2 - lat1) * deg
	dLng := (lng2 - lng1) * deg
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*deg)*math.Cos(lat2*deg)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusKm * math.Asin(math.Sqrt(a))
}
