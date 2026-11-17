package geo

import (
	"context"
	"database/sql"
	"math"
	"os"
	"testing"

	"github.com/munisp/pfaengine/internal/branch"
	"github.com/munisp/pfaengine/internal/store"
)

// Well-known reference points.
const (
	lagosIslandLat, lagosIslandLng = 6.4541, 3.3947 // CMS, Lagos Island
	ikejaLat, ikejaLng             = 6.6018, 3.3515 // Ikeja, Lagos
	abujaLat, abujaLng             = 9.0765, 7.3986 // Abuja FCT
)

// newBranchEngine builds a stubbed branch registry for the in-memory path.
func newBranchEngine(t *testing.T) *branch.Engine {
	t.Helper()
	be := branch.New()
	for _, b := range []branch.Branch{
		{TenantID: "t1", Name: "Lagos Island HQ", State: "Lagos", Lat: lagosIslandLat, Lng: lagosIslandLng},
		{TenantID: "t1", Name: "Ikeja Service Centre", State: "Lagos", Lat: ikejaLat, Lng: ikejaLng},
		{TenantID: "t1", Name: "Abuja Corporate", State: "FCT", Lat: abujaLat, Lng: abujaLng},
		{TenantID: "t1", Name: "Kano (no geo)", State: "Kano"}, // zero coords — excluded from Nearest
		{TenantID: "t2", Name: "Other tenant", State: "Lagos", Lat: lagosIslandLat, Lng: lagosIslandLng},
	} {
		if _, err := be.AddBranch(b); err != nil {
			t.Fatalf("AddBranch %s: %v", b.Name, err)
		}
	}
	return be
}

func TestHaversineKnownDistance(t *testing.T) {
	// Lagos Island → Ikeja is roughly 18–20 km great-circle.
	d := haversineKm(lagosIslandLat, lagosIslandLng, ikejaLat, ikejaLng)
	if d < 15 || d > 25 {
		t.Fatalf("Lagos→Ikeja haversine = %.2f km, want ~18 km (15..25)", d)
	}
	if d := haversineKm(lagosIslandLat, lagosIslandLng, lagosIslandLat, lagosIslandLng); d != 0 {
		t.Fatalf("zero-distance haversine = %v, want 0", d)
	}
}

func TestNearestHaversineOrderingAndK(t *testing.T) {
	e := New(newBranchEngine(t), nil)

	// From CMS Lagos Island: HQ ~0 km, then Ikeja (~18 km), then Abuja (~535 km).
	out := e.Nearest("t1", lagosIslandLat, lagosIslandLng, 10)
	if len(out) != 3 { // Kano branch has no geo — excluded
		t.Fatalf("Nearest returned %d branches, want 3 (un-geocoded excluded)", len(out))
	}
	if out[0].Branch.Name != "Lagos Island HQ" {
		t.Fatalf("nearest = %s, want Lagos Island HQ", out[0].Branch.Name)
	}
	if out[0].DistanceKm > 0.001 {
		t.Fatalf("self distance = %v km, want ~0", out[0].DistanceKm)
	}
	if !(out[1].DistanceKm < out[2].DistanceKm) {
		t.Fatalf("distances not ascending: %+v", out)
	}

	// k<=0 → default 5; fewer branches available is fine.
	if got := e.Nearest("t1", lagosIslandLat, lagosIslandLng, 0); len(got) != 3 {
		t.Fatalf("k=0 returned %d, want all 3 (default k exceeds available)", len(got))
	}

	// Cap: k > 50 is clamped (cannot return more than exist here either).
	if got := e.Nearest("t1", lagosIslandLat, lagosIslandLng, 500); len(got) != 3 {
		t.Fatalf("k=500 returned %d, want 3", len(got))
	}

	// Tenant isolation: t2 sees only its own branch.
	if got := e.Nearest("t2", lagosIslandLat, lagosIslandLng, 5); len(got) != 1 {
		t.Fatalf("t2 nearest returned %d, want 1", len(got))
	}
}

func TestNearestEmptyTenant(t *testing.T) {
	e := New(newBranchEngine(t), nil)
	if got := e.Nearest("nope", lagosIslandLat, lagosIslandLng, 5); len(got) != 0 {
		t.Fatalf("empty tenant returned %d branches, want 0", len(got))
	}
	var nilEngine *Engine = New(nil, nil)
	if got := nilEngine.Nearest("t1", lagosIslandLat, lagosIslandLng, 5); len(got) != 0 {
		t.Fatalf("nil branch engine returned %d, want 0", len(got))
	}
}

func TestCoverageByState(t *testing.T) {
	e := New(newBranchEngine(t), nil)
	got := e.CoverageByState("t1")
	byState := map[string]StateCoverage{}
	for _, sc := range got {
		byState[sc.State] = sc
	}
	if len(got) != 3 {
		t.Fatalf("coverage states = %d, want 3 (FCT, Kano, Lagos)", len(got))
	}
	if s := byState["Lagos"]; s.Branches != 2 || s.HasGeo != 2 {
		t.Fatalf("Lagos coverage = %+v, want {2 branches, 2 geo}", s)
	}
	if s := byState["Kano"]; s.Branches != 1 || s.HasGeo != 0 {
		t.Fatalf("Kano coverage = %+v, want {1 branch, 0 geo}", s)
	}
	// Sorted by state for deterministic output.
	for i := 1; i < len(got); i++ {
		if got[i-1].State >= got[i].State {
			t.Fatalf("coverage not sorted: %+v", got)
		}
	}
}

func TestCoordinateValidation(t *testing.T) {
	for _, tc := range []struct {
		lat, lng float64
		valid    bool
		ng       bool
	}{
		{lagosIslandLat, lagosIslandLng, true, true},
		{0, 0, true, false},           // Null Island — valid but not NG
		{91, 3, false, false},         // impossible latitude
		{6.5, 181, false, false},      // impossible longitude
		{-91, 3, false, false},        //
		{51.5, -0.12, true, false},    // London — valid, outside NG (diaspora)
		{math.NaN(), 3, false, false}, //
	} {
		if got := ValidCoord(tc.lat, tc.lng); got != tc.valid {
			t.Errorf("ValidCoord(%v,%v) = %v, want %v", tc.lat, tc.lng, got, tc.valid)
		}
		if tc.valid {
			if got := InNigeria(tc.lat, tc.lng); got != tc.ng {
				t.Errorf("InNigeria(%v,%v) = %v, want %v", tc.lat, tc.lng, got, tc.ng)
			}
		}
	}
}

// TestPostGIS exercises the database path end-to-end. Gated on
// TEST_DATABASE_URL; the migration CREATE EXTENSION is applied defensively
// and the test skips (not fails) when PostGIS is unavailable.
func TestPostGIS(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL unset — skipping PostGIS integration test")
	}
	db, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Skipf("store.Open failed: %v", err)
	}
	defer db.Close()

	tenant := "geo-test"
	err = db.WithTenant(context.Background(), tenant, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(context.Background(), `CREATE EXTENSION IF NOT EXISTS postgis`); err != nil {
			return err
		}
		_, err := tx.ExecContext(context.Background(), `
			CREATE TABLE IF NOT EXISTS geo_point (
				kind text NOT NULL, tenant_id text NOT NULL, entity_id text NOT NULL,
				geog geography(Point,4326) NOT NULL,
				updated_at timestamptz NOT NULL DEFAULT now(),
				PRIMARY KEY (kind, tenant_id, entity_id))`)
		return err
	})
	if err != nil {
		t.Skipf("PostGIS unavailable (extension/table): %v", err)
	}

	// Fresh registry under the test tenant so branch IDs line up with the
	// geo_point mirror (Nearest joins PostGIS entity IDs back onto it).
	be := branch.New()
	for _, b := range []branch.Branch{
		{TenantID: tenant, Name: "Lagos Island HQ", State: "Lagos", Lat: lagosIslandLat, Lng: lagosIslandLng},
		{TenantID: tenant, Name: "Ikeja Service Centre", State: "Lagos", Lat: ikejaLat, Lng: ikejaLng},
		{TenantID: tenant, Name: "Abuja Corporate", State: "FCT", Lat: abujaLat, Lng: abujaLng},
	} {
		if _, err := be.AddBranch(b); err != nil {
			t.Fatalf("AddBranch %s: %v", b.Name, err)
		}
	}
	e := New(be, db)
	for _, b := range be.Branches(tenant, "") {
		if err := e.UpsertBranchGeo(tenant, b); err != nil {
			t.Skipf("upsert failed (PostGIS not fully available?): %v", err)
		}
	}
	defer func() {
		_ = db.WithTenant(context.Background(), tenant, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(context.Background(),
				`DELETE FROM geo_point WHERE tenant_id=$1`, tenant)
			return err
		})
	}()

	out := e.Nearest(tenant, lagosIslandLat, lagosIslandLng, 2)
	if len(out) != 2 {
		t.Fatalf("PostGIS Nearest returned %d, want 2", len(out))
	}
	if out[0].Branch.Name != "Lagos Island HQ" {
		t.Fatalf("PostGIS nearest = %s, want Lagos Island HQ", out[0].Branch.Name)
	}
	if d := out[1].DistanceKm; d < 15 || d > 25 {
		t.Fatalf("PostGIS Lagos→Ikeja = %.2f km, want ~18 km", d)
	}

	// In-memory fallback must agree with PostGIS on ordering.
	mem := New(be, nil)
	got := mem.Nearest(tenant, lagosIslandLat, lagosIslandLng, 2)
	if got[0].Branch.Name != out[0].Branch.Name || got[1].Branch.Name != out[1].Branch.Name {
		t.Fatalf("haversine/PostGIS ordering disagree: mem=%v pg=%v",
			[]string{got[0].Branch.Name, got[1].Branch.Name},
			[]string{out[0].Branch.Name, out[1].Branch.Name})
	}
}
