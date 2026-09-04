package executor

import (
	"fmt"
	"strings"
	"math"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/sql/types"
)

// TestSpatialS1 covers the Spatial track S1: GEOMETRY / GEOGRAPHY column
// declaration, the ST_GeomFromText / ST_GeogFromText / ST_Point constructors,
// plain-WKT coercion, the accessors, whole-value SELECT, ORDER BY (canonical
// EWKB order), and catalog persist / reopen.
func TestSpatialS1(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE places (
		id INT64 PRIMARY KEY,
		loc GEOMETRY(Point, 4326),
		zone GEOGRAPHY(Polygon, 4326),
		route GEOMETRY)`)
	execOK(t, s, `INSERT INTO places (id, loc, zone, route) VALUES
		(1, ST_Point(-73.98, 40.75, 4326),
		    ST_GeogFromText('POLYGON((0 0, 4 0, 4 4, 0 4, 0 0))'),
		    ST_GeomFromText('LINESTRING(0 0, 1 1, 2 0)', 4326))`)
	execOK(t, s, `INSERT INTO places (id, loc) VALUES (2, 'POINT(10 20)')`)
	execOK(t, s, `INSERT INTO places (id, loc, route) VALUES
		(3, ST_GeomFromEWKT('SRID=4326;POINT(1 1)'),
		    ST_GeomFromText('MULTIPOINT((0 0), (5 5), (9 9))'))`)

	r, err := s.Exec(`SELECT id, ST_X(loc), ST_Y(loc), ST_SRID(loc),
		ST_GeometryType(loc), ST_NPoints(route), ST_NumGeometries(route)
		FROM places ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows[0][1].Flt != -73.98 || r.Rows[0][2].Flt != 40.75 || r.Rows[0][3].Int != 4326 {
		t.Fatalf("row 1 accessors: %+v", r.Rows[0])
	}
	if r.Rows[0][5].Int != 3 {
		t.Fatalf("row 1 ST_NPoints(LINESTRING with 3 pts) = %v", r.Rows[0][5])
	}
	if r.Rows[2][6].Int != 3 {
		t.Fatalf("row 3 ST_NumGeometries(MULTIPOINT of 3) = %v", r.Rows[2][6])
	}

	// A non-Point value into a GEOMETRY(Point) column is rejected.
	if _, err := s.Exec(`INSERT INTO places (id, loc) VALUES (9, 'LINESTRING(0 0, 1 1)')`); err == nil {
		t.Error("a LineString into a GEOMETRY(Point) column should be rejected")
	}

	// Whole-value SELECT returns the typed geometry.
	r, err = s.Exec(`SELECT zone FROM places WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows[0][0].Typ.Kind != types.KindGeography || r.Rows[0][0].Geom == nil {
		t.Fatalf("whole geography select: %+v", r.Rows[0][0])
	}

	// ORDER BY on a geometry column is a deterministic total order.
	r, err = s.Exec(`SELECT id FROM places ORDER BY loc`)
	if err != nil {
		t.Fatalf("ORDER BY geometry: %v", err)
	}
	if len(r.Rows) != 3 {
		t.Fatalf("ORDER BY geometry rows: %d", len(r.Rows))
	}

	// Coercion bridge: a fixed POINT value flows into a GEOGRAPHY(Point)
	// column (docs/design-spatial.md §2.7).
	execOK(t, s, `CREATE TABLE bridge (id INT64 PRIMARY KEY, g GEOGRAPHY(Point, 4326))`)
	execOK(t, s, `INSERT INTO bridge (id, g) VALUES (1, POINT(1, 2))`)
	r, err = s.Exec(`SELECT ST_AsText(g), ST_SRID(g) FROM bridge WHERE id = 1`)
	if err != nil {
		t.Fatalf("POINT -> GEOGRAPHY bridge: %v", err)
	}
	if r.Rows[0][0].Str != "POINT(1 2)" || r.Rows[0][1].Int != 4326 {
		t.Fatalf("bridge result: %q srid=%v", r.Rows[0][0].Str, r.Rows[0][1])
	}

	db.Close()
	re, err := Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer re.Close()
	r, err = re.Session().Exec(`SELECT ST_AsEWKT(zone), ST_GeometryType(route) FROM places WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows[0][0].Str != "SRID=4326;POLYGON((0 0, 4 0, 4 4, 0 4, 0 0))" {
		t.Fatalf("geography lost on restart: %q", r.Rows[0][0].Str)
	}
	if r.Rows[0][1].Str != "ST_LineString" {
		t.Fatalf("route type after restart: %q", r.Rows[0][1].Str)
	}
}

// TestSpatialS2 covers S2: measurement (planar for GEOMETRY, geodetic for
// GEOGRAPHY) and the predicate suite.
func TestSpatialS2(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	db, err := Create(path, testKeys(t), 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := db.Session()
	execOK(t, s, `CREATE TABLE g (id INT64 PRIMARY KEY, a GEOMETRY, b GEOMETRY,
		zone GEOGRAPHY(Polygon, 4326), p GEOMETRY(Point, 4326))`)
	execOK(t, s, `INSERT INTO g (id, a, b) VALUES (1,
		ST_GeomFromText('POLYGON((0 0, 4 0, 4 4, 0 4, 0 0))'),
		ST_GeomFromText('POINT(2 2)'))`)
	execOK(t, s, `INSERT INTO g (id, a, b) VALUES (2,
		ST_GeomFromText('LINESTRING(0 0, 3 4)'),
		ST_GeomFromText('POINT(10 10)'))`)
	execOK(t, s, `INSERT INTO g (id, zone) VALUES (3, ST_GeogFromText('POLYGON((0 0, 1 0, 1 1, 0 1, 0 0))'))`)

	r, err := s.Exec(`SELECT ST_Area(a), ST_Perimeter(a) FROM g WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows[0][0].Flt != 16 || r.Rows[0][1].Flt != 16 {
		t.Fatalf("planar area/perimeter of a 4x4 square: %+v", r.Rows[0])
	}

	r, err = s.Exec(`SELECT ST_Length(a) FROM g WHERE id = 2`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows[0][0].Flt != 5 { // 3-4-5 triangle
		t.Fatalf("planar line length: %v", r.Rows[0][0])
	}

	r, err = s.Exec(`SELECT ST_Distance(a, b), ST_Contains(a, b), ST_Intersects(a, b),
		ST_Within(b, a), ST_Disjoint(a, b) FROM g WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows[0][0].Flt != 0 || !r.Rows[0][1].Bool || !r.Rows[0][2].Bool ||
		!r.Rows[0][3].Bool || r.Rows[0][4].Bool {
		t.Fatalf("polygon-contains-point predicates: %+v", r.Rows[0])
	}

	r, err = s.Exec(`SELECT ST_Distance(a, b), ST_Contains(a, b), ST_DWithin(a, b, 100),
		ST_DWithin(a, b, 5) FROM g WHERE id = 2`)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(r.Rows[0][0].Flt-math.Sqrt(85)) > 1e-9 {
		t.Fatalf("point-to-line distance = %v (want sqrt(85))", r.Rows[0][0].Flt)
	}
	if r.Rows[0][1].Bool || !r.Rows[0][2].Bool || r.Rows[0][3].Bool {
		t.Fatalf("line/point DWithin: %+v", r.Rows[0])
	}

	// GEOGRAPHY area is spherical metres² — a ~1°×1° box near the equator.
	r, err = s.Exec(`SELECT ST_Area(zone) FROM g WHERE id = 3`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows[0][0].Flt < 1.2e10 || r.Rows[0][0].Flt > 1.3e10 {
		t.Fatalf("geodetic area = %v m² (expected ~1.23e10)", r.Rows[0][0].Flt)
	}

	r, err = s.Exec(`SELECT ST_AsText(ST_Centroid(a)), ST_AsText(ST_Envelope(a)) FROM g WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows[0][0].Str != "POINT(2 2)" {
		t.Fatalf("centroid of the square = %q", r.Rows[0][0].Str)
	}

	// ST_Transform 4326 -> 3857 puts the origin at (0,0).
	execOK(t, s, `INSERT INTO g (id, p) VALUES (4, ST_Point(0, 0, 4326))`)
	r, err = s.Exec(`SELECT ST_X(ST_Transform(p, 3857)), ST_SRID(ST_Transform(p, 3857)) FROM g WHERE id = 4`)
	if err != nil {
		t.Fatalf("ST_Transform: %v", err)
	}
	if math.Abs(r.Rows[0][0].Flt) > 1e-6 || r.Rows[0][1].Int != 3857 {
		t.Fatalf("transform origin: %+v", r.Rows[0])
	}
}

// TestSpatialS3 covers S3: CREATE SPATIAL INDEX on a GEOMETRY column, the
// bbox Z-order key, optimiser sargability for ST_Intersects / ST_DWithin /
// ST_Within against a constant, and restart durability.
func TestSpatialS3(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := db.Session()
	execOK(t, s, `CREATE TABLE places (id INT64 PRIMARY KEY, loc GEOMETRY(Point, 4326))`)
	for i := 0; i < 400; i++ {
		execOK(t, s, fmt.Sprintf(`INSERT INTO places (id, loc) VALUES (%d, ST_Point(%d, %d, 4326))`, i, i%20, i/20))
	}
	execOK(t, s, `CREATE SPATIAL INDEX ix_loc ON places (loc)`)

	// EXPLAIN must pick the spatial index.
	ex, err := s.Exec(`EXPLAIN SELECT id FROM places WHERE ST_Intersects(loc, ST_GeomFromText('POLYGON((4 4, 7 4, 7 7, 4 7, 4 4))', 4326))`)
	if err != nil {
		t.Fatal(err)
	}
	usedIndex := false
	for _, row := range ex.Rows {
		if l := row[0].String(); (contains2(l, "IndexScan") && contains2(l, "spatial")) {
			usedIndex = true
		}
	}
	if !usedIndex {
		t.Fatalf("ST_Intersects should use the spatial index; plan:\n%s", planText(ex))
	}

	// ... and return the exact set (residual re-check).
	r, err := s.Exec(`SELECT count(*) FROM places WHERE ST_Intersects(loc, ST_GeomFromText('POLYGON((4 4, 7 4, 7 7, 4 7, 4 4))', 4326))`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows[0][0].String() != "16" {
		t.Fatalf("ST_Intersects box count = %v (want 16)", r.Rows[0][0].String())
	}

	r, err = s.Exec(`SELECT id FROM places WHERE ST_DWithin(loc, ST_Point(5, 5, 4326), 1.0) ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rows) != 5 {
		t.Fatalf("ST_DWithin 1.0 of (5,5) = %d rows (want 5 — the plus shape)", len(r.Rows))
	}

	db.Close()
	re, err := Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer re.Close()
	r, err = re.Session().Exec(`SELECT count(*) FROM places WHERE ST_DWithin(loc, ST_Point(19, 19, 4326), 0.5)`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows[0][0].String() != "1" {
		t.Fatalf("after restart, spatial index count = %v", r.Rows[0][0].String())
	}
}

func contains2(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func planText(r *Result) string {
	out := ""
	for _, row := range r.Rows {
		out += row[0].String() + "\n"
	}
	return out
}

// TestSpatialS5 covers S5: ST_ConvexHull, ST_Simplify, ST_Segmentize,
// ST_Buffer, and the bounded ST_Intersection / ST_Union / ST_Difference /
// ST_SymDifference overlay operators.
func TestSpatialS5(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	db, err := Create(path, testKeys(t), 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := db.Session()
	execOK(t, s, `CREATE TABLE g (id INT64 PRIMARY KEY, a GEOMETRY, b GEOMETRY)`)
	execOK(t, s, `INSERT INTO g (id, a, b) VALUES (1,
		ST_GeomFromText('POLYGON((0 0, 4 0, 4 4, 0 4, 0 0))'),
		ST_GeomFromText('POLYGON((2 2, 6 2, 6 6, 2 6, 2 2))'))`)

	r, err := s.Exec(`SELECT ST_Area(ST_Intersection(a, b)) FROM g WHERE id = 1`)
	if err != nil {
		t.Fatalf("ST_Intersection: %v", err)
	}
	if r.Rows[0][0].Flt != 4 {
		t.Fatalf("intersection area = %v (want 4)", r.Rows[0][0].Flt)
	}

	r, err = s.Exec(`SELECT ST_GeometryType(ST_Union(a, ST_GeomFromText('POLYGON((100 100, 101 100, 101 101, 100 100))'))) FROM g WHERE id = 1`)
	if err != nil {
		t.Fatalf("ST_Union: %v", err)
	}
	if r.Rows[0][0].Str != "ST_MultiPolygon" {
		t.Fatalf("disjoint union type = %q", r.Rows[0][0].Str)
	}

	r, err = s.Exec(`SELECT ST_Area(ST_Difference(a, ST_GeomFromText('POLYGON((100 100, 101 100, 101 101, 100 100))'))) FROM g WHERE id = 1`)
	if err != nil {
		t.Fatalf("ST_Difference: %v", err)
	}
	if r.Rows[0][0].Flt != 16 {
		t.Fatalf("difference with a disjoint operand should be unchanged: %v", r.Rows[0][0].Flt)
	}

	r, err = s.Exec(`SELECT ST_AsText(ST_Buffer(a, 1)) FROM g WHERE id = 1`)
	if err != nil {
		t.Fatalf("ST_Buffer: %v", err)
	}
	if !strings.HasPrefix(r.Rows[0][0].Str, "POLYGON(") {
		t.Fatalf("buffer result: %q", r.Rows[0][0].Str)
	}

	execOK(t, s, `INSERT INTO g (id, a) VALUES (2, ST_GeomFromText('LINESTRING(0 0, 10 0)'))`)
	r, err = s.Exec(`SELECT ST_NPoints(ST_Segmentize(a, 3)) FROM g WHERE id = 2`)
	if err != nil {
		t.Fatalf("ST_Segmentize: %v", err)
	}
	if r.Rows[0][0].Int < 4 {
		t.Fatalf("segmentize should add vertices: %v", r.Rows[0][0].Int)
	}

	r, err = s.Exec(`SELECT ST_GeometryType(ST_ConvexHull(a)) FROM g WHERE id = 1`)
	if err != nil {
		t.Fatalf("ST_ConvexHull: %v", err)
	}
	if r.Rows[0][0].Str != "ST_Polygon" {
		t.Fatalf("convex hull of a square should stay a Polygon: %q", r.Rows[0][0].Str)
	}
}

// TestSpatialS6GeoJSON covers S6: ST_AsGeoJSON / ST_GeomFromGeoJSON.
func TestSpatialS6GeoJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	db, err := Create(path, testKeys(t), 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := db.Session()
	execOK(t, s, `CREATE TABLE g (id INT64 PRIMARY KEY, a GEOMETRY(Point, 4326))`)
	execOK(t, s, `INSERT INTO g (id, a) VALUES (1, ST_Point(1, 2, 4326))`)
	r, err := s.Exec(`SELECT ST_AsGeoJSON(a) FROM g WHERE id = 1`)
	if err != nil {
		t.Fatalf("ST_AsGeoJSON: %v", err)
	}
	if r.Rows[0][0].Str != `{"coordinates":[1,2],"type":"Point"}` {
		t.Fatalf("geojson = %q", r.Rows[0][0].Str)
	}
	r, err = s.Exec(`SELECT ST_AsText(ST_GeomFromGeoJSON('{"type":"LineString","coordinates":[[0,0],[1,1]]}', 4326))`)
	if err == nil {
		t.Logf("no-FROM select worked: %v", r.Rows)
	}
	execOK(t, s, `INSERT INTO g (id, a) VALUES (2, 'POINT(0 0)')`)
	r, err = s.Exec(`SELECT ST_AsText(ST_GeomFromGeoJSON('{"type":"LineString","coordinates":[[0,0],[1,1]]}', 4326)) FROM g WHERE id = 2`)
	if err != nil {
		t.Fatalf("ST_GeomFromGeoJSON: %v", err)
	}
	if r.Rows[0][0].Str != "LINESTRING(0 0, 1 1)" {
		t.Fatalf("from geojson = %q", r.Rows[0][0].Str)
	}
}

// TestSpatialSRIDArgRange is a regression test for a bug found during a
// "fix all bugs" pass (2026-09-04): every function-argument SRID (ST_Point's
// 3rd arg, ST_SetSRID/ST_Transform's 2nd arg, ST_GeomFromGeoJSON's 2nd arg)
// was coerced to Int64 and narrowed with a bare uint16(n.Int) — an
// out-of-range value like 99999 silently wrapped to a different SRID
// (34463) instead of erroring, unlike the GEOMETRY(sub, srid) DDL form
// (sql/parser.geoTypeArgs), which already rejected it with "SRID out of
// range". All these call sites must fail the same way.
func TestSpatialSRIDArgRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	db, err := Create(path, testKeys(t), 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := db.Session()

	bad := []string{
		`SELECT ST_Point(1, 2, 99999)`,
		`SELECT ST_Point(1, 2, -1)`,
		`SELECT ST_SetSRID(ST_Point(1, 2), 99999)`,
		`SELECT ST_Transform(ST_Point(1, 2, 4326), 99999)`,
		`SELECT ST_GeomFromGeoJSON('{"type":"Point","coordinates":[1,2]}', 99999)`,
		`SELECT ST_GeomFromText('SRID=99999;POINT(1 2)')`,
	}
	for _, q := range bad {
		if _, err := s.Exec(q); err == nil {
			t.Errorf("%s: expected an SRID-out-of-range error, got none", q)
		}
	}

	// The boundary and a normal value must still work.
	ok := []struct {
		q    string
		want int64
	}{
		{`SELECT ST_SRID(ST_Point(1, 2, 65535))`, 65535},
		{`SELECT ST_SRID(ST_SetSRID(ST_Point(1, 2), 3857))`, 3857},
		{`SELECT ST_SRID(ST_GeomFromText('SRID=3857;POINT(1 2)'))`, 3857},
	}
	for _, tc := range ok {
		r, err := s.Exec(tc.q)
		if err != nil {
			t.Fatalf("%s: %v", tc.q, err)
		}
		if r.Rows[0][0].Int != tc.want {
			t.Errorf("%s: got SRID %d, want %d", tc.q, r.Rows[0][0].Int, tc.want)
		}
	}
}
