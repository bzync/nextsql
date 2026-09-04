package types

import (
	"math"
	"strings"
	"testing"
)

func mustGeom(t *testing.T, wkt string, srid uint32) *Geom {
	t.Helper()
	g, err := ParseGeneralWKT(wkt, srid)
	if err != nil {
		t.Fatalf("ParseGeneralWKT(%q): %v", wkt, err)
	}
	return g
}

func TestSpatialWKTEWKBRoundTrip(t *testing.T) {
	cases := []string{
		"POINT(1 2)",
		"LINESTRING(0 0, 1 1, 2 0)",
		"POLYGON((0 0, 4 0, 4 4, 0 4, 0 0))",
		"POLYGON((0 0, 10 0, 10 10, 0 10, 0 0), (2 2, 2 4, 4 4, 4 2, 2 2))",
		"MULTIPOINT((0 0), (1 1))",
		"MULTIPOINT(0 0, 1 1)",
		"MULTILINESTRING((0 0, 1 1), (2 2, 3 3))",
		"MULTIPOLYGON(((0 0, 1 0, 1 1, 0 1, 0 0)), ((5 5, 6 5, 6 6, 5 5)))",
		"GEOMETRYCOLLECTION(POINT(1 2), LINESTRING(0 0, 1 1))",
	}
	for _, wkt := range cases {
		g := mustGeom(t, wkt, 4326)
		ewkb, err := EncodeEWKB(g)
		if err != nil {
			t.Fatalf("EncodeEWKB(%q): %v", wkt, err)
		}
		back, n, err := DecodeEWKB(ewkb)
		if err != nil || n != len(ewkb) {
			t.Fatalf("DecodeEWKB(%q): %v n=%d/%d", wkt, err, n, len(ewkb))
		}
		if back.SRID != 4326 {
			t.Errorf("%q: SRID lost, got %d", wkt, back.SRID)
		}
		// EWKB is canonical, so a second encode is byte-identical.
		ewkb2, _ := EncodeEWKB(back)
		if string(ewkb) != string(ewkb2) {
			t.Errorf("%q: EWKB not canonical/stable", wkt)
		}
		// Heap-row + key round trips.
		gt, _ := GeometryType(0, 4326)
		val := Value{Typ: gt, Geom: g}
		raw, err := EncodeRow([]Value{val})
		if err != nil {
			t.Fatalf("EncodeRow(%q): %v", wkt, err)
		}
		got, err := DecodeRow(raw, []Type{gt})
		if err != nil {
			t.Fatalf("DecodeRow(%q): %v", wkt, err)
		}
		if c, err := got[0].Cmp(val); err != nil || c != 0 {
			t.Fatalf("%q: heap round trip mismatch (%v)", wkt, err)
		}
		key, err := EncodeKey([]Value{val})
		if err != nil {
			t.Fatalf("EncodeKey(%q): %v", wkt, err)
		}
		dk, err := DecodeKey(key, []Type{gt})
		if err != nil {
			t.Fatalf("DecodeKey(%q): %v", wkt, err)
		}
		if c, err := dk[0].Cmp(val); err != nil || c != 0 {
			t.Fatalf("%q: key round trip mismatch (%v)", wkt, err)
		}
	}
}

func TestSpatialEWKTPrefix(t *testing.T) {
	g, err := ParseGeneralWKT("SRID=3857;POINT(100 200)", 0)
	if err != nil {
		t.Fatal(err)
	}
	if g.SRID != 3857 || g.Type != ogcPoint {
		t.Fatalf("EWKT parse: %+v", g)
	}
	if got := FormatGeomEWKT(g); got != "SRID=3857;POINT(100 200)" {
		t.Errorf("FormatGeomEWKT = %q", got)
	}
}

// TestSpatialEWKTPrefixSRIDRange is a regression test for a bug found during
// a "fix all bugs" pass (2026-09-04): the EWKT "SRID=<n>;" prefix parsed n
// with strconv.ParseUint(..., 32), so an out-of-range value like 99999 (which
// fits a u32 but not the u16 a column's SRID actually is) sailed through here
// uncaught and only got silently truncated later — by uint16(g.SRID) — when
// the caller narrowed it into a destination column type, producing a
// different SRID than the one written (99999 -> 34463) with no error. Must
// fail closed here instead, matching sql/parser.geoTypeArgs's own "SRID out
// of range" check for the GEOMETRY(sub, srid) DDL form.
func TestSpatialEWKTPrefixSRIDRange(t *testing.T) {
	if _, err := ParseGeneralWKT("SRID=99999;POINT(1 2)", 0); err == nil {
		t.Fatal("SRID=99999 (out of u16 range) should be rejected, not silently truncated")
	}
	if _, err := ParseGeneralWKT("SRID=65535;POINT(1 2)", 0); err != nil {
		t.Fatalf("max valid u16 SRID should be accepted: %v", err)
	}
}

func TestSpatialSubtypeAndSRIDEnforced(t *testing.T) {
	ptCol, _ := GeometryType(GeomSubPoint, 4326)
	line := mustGeom(t, "LINESTRING(0 0, 1 1)", 4326)
	if _, err := Coerce(Value{Typ: Type{Kind: KindGeometry}, Geom: line}, ptCol); err == nil {
		t.Error("a LineString into a GEOMETRY(Point) column should error")
	}
	pt := mustGeom(t, "POINT(1 2)", 0)
	v, err := Coerce(Value{Typ: Type{Kind: KindGeometry}, Geom: pt}, ptCol)
	if err != nil {
		t.Fatalf("point into GEOMETRY(Point, 4326): %v", err)
	}
	if v.Geom.SRID != 4326 {
		t.Errorf("SRID not normalized: %d", v.Geom.SRID)
	}
}

func TestSpatialCastBridge(t *testing.T) {
	p := MustPoint(-73.98, 40.75)
	geog, _ := GeographyType(GeomSubPoint, 4326)
	g, err := Coerce(p, geog)
	if err != nil {
		t.Fatalf("POINT -> GEOGRAPHY(Point, 4326): %v", err)
	}
	if g.Typ.Kind != KindGeography || g.Geom.Type != ogcPoint {
		t.Fatalf("bridge result: %+v", g)
	}
	// Back to POINT.
	back, err := Coerce(g, Point())
	if err != nil {
		t.Fatalf("GEOGRAPHY(Point) -> POINT: %v", err)
	}
	if back.Typ.Kind != KindPoint || back.Lon != -73.98 {
		t.Fatalf("reverse bridge: %+v", back)
	}
	// A non-Point geography cannot become a POINT.
	polyGeog := Value{Typ: Type{Kind: KindGeography}, Geom: mustGeom(t, "POLYGON((0 0, 1 0, 1 1, 0 0))", 4326)}
	if _, err := Coerce(polyGeog, Point()); err == nil {
		t.Error("polygon geography -> POINT should error")
	}
}

func TestSpatialTextCoerce(t *testing.T) {
	gt, _ := GeometryType(0, 0)
	v, err := Coerce(StringValue("LINESTRING(0 0, 5 5)"), gt)
	if err != nil {
		t.Fatalf("text -> GEOMETRY: %v", err)
	}
	if v.Geom.Type != ogcLineString {
		t.Fatalf("%+v", v.Geom)
	}
	// Round trip through String().
	txt, err := Coerce(v, Text())
	if err != nil || !strings.HasPrefix(txt.Str, "LINESTRING(") {
		t.Fatalf("GEOMETRY -> TEXT = %q %v", txt.Str, err)
	}
}

func TestSpatialDecodeEWKBBounds(t *testing.T) {
	// A part count claiming billions must be rejected before allocating.
	bad := []byte{0x01, byte(ogcMultiPoint), 0, 0, 0xFF, 0xFF, 0xFF, 0xFF}
	if _, _, err := DecodeEWKB(bad); err == nil {
		t.Error("oversized part count should be rejected")
	}
	if _, _, err := DecodeEWKB([]byte{0x02, 1, 0, 0, 0}); err == nil {
		t.Error("big-endian EWKB should be rejected")
	}
	if _, _, err := DecodeEWKB(nil); err == nil {
		t.Error("empty input should error")
	}
}

func TestSpatialOps(t *testing.T) {
	square := mustGeom(t, "POLYGON((0 0, 10 0, 10 10, 0 10, 0 0))", 0)
	hole := mustGeom(t, "POLYGON((0 0, 10 0, 10 10, 0 10, 0 0), (3 3, 3 7, 7 7, 7 3, 3 3))", 0)
	inPt := mustGeom(t, "POINT(1 1)", 0)
	inHolePt := mustGeom(t, "POINT(5 5)", 0)
	outPt := mustGeom(t, "POINT(20 20)", 0)
	line := mustGeom(t, "LINESTRING(-5 5, 5 5)", 0)

	if GeomArea(square, false) != 100 {
		t.Errorf("square area = %v", GeomArea(square, false))
	}
	if a := GeomArea(hole, false); a != 84 {
		t.Errorf("square-with-hole area = %v (want 84)", a)
	}
	if GeomPerimeter(square, false) != 40 {
		t.Errorf("square perimeter = %v", GeomPerimeter(square, false))
	}
	if !GeomsIntersect(square, inPt) || GeomsIntersect(square, outPt) {
		t.Error("intersects point in/out")
	}
	if GeomsIntersect(hole, inHolePt) {
		t.Error("a point in the hole must not intersect the polygon")
	}
	if !GeomsIntersect(square, line) {
		t.Error("a line crossing the square must intersect it")
	}
	if !GeomContains(square, inPt, false) || GeomContains(square, outPt, false) {
		t.Error("contains point in/out")
	}
	d, err := GeomDistance(outPt, square, false)
	if err != nil || d != planarDist(20, 20, 10, 10) {
		t.Errorf("distance outside point to square = %v (%v)", d, err)
	}
	cx, cy, ok := GeomCentroid(square)
	if !ok || cx != 5 || cy != 5 {
		t.Errorf("square centroid = %v,%v", cx, cy)
	}
	// geodetic area of a 1-degree box near the equator.
	geoBox := mustGeom(t, "POLYGON((0 0, 1 0, 1 1, 0 1, 0 0))", 4326)
	if a := GeomArea(geoBox, true); a < 1.2e10 || a > 1.3e10 {
		t.Errorf("geodetic 1deg box area = %v", a)
	}
}

func TestSpatialS5(t *testing.T) {
	// Convex hull of an L-shape's vertices is its bounding rectangle-ish hull.
	lshape := mustGeom(t, "POLYGON((0 0, 4 0, 4 1, 1 1, 1 4, 0 4, 0 0))", 0)
	hull := GeomConvexHull(lshape)
	if hull.Type != ogcPolygon {
		t.Fatalf("hull type = %d", hull.Type)
	}
	lshapeArea := GeomArea(lshape, false) // 7: two overlapping 4x1 bars
	if a := GeomArea(hull, false); a < lshapeArea-1e-9 {
		t.Errorf("hull area %v should be >= the L-shape's own area %v", a, lshapeArea)
	}
	// A point cloud's hull must contain every input point.
	for i := 0; i < len(lshape.Coords)/2; i++ {
		x, y := lshape.Coords[i*2], lshape.Coords[i*2+1]
		if in, on := pointInPolygon(x, y, hull); !in && !on {
			t.Errorf("hull does not contain vertex (%v,%v)", x, y)
		}
	}

	// Simplify collapses near-collinear points.
	line := mustGeom(t, "LINESTRING(0 0, 1 0.001, 2 0, 3 5, 4 0)", 0)
	simplified := GeomSimplify(line, 0.1)
	if got := len(simplified.Coords) / 2; got >= 5 {
		t.Errorf("simplify should drop the near-collinear point: %d pts", got)
	}

	// Segmentize adds vertices.
	seg := GeomSegmentize(mustGeom(t, "LINESTRING(0 0, 10 0)", 0), 3)
	if n := len(seg.Coords) / 2; n < 4 {
		t.Errorf("segmentize should add vertices: %d pts", n)
	}

	// Buffer of a point is a polygon containing the original point and every
	// hull vertex is exactly `radius` away or less is not guaranteed, but the
	// centre must be strictly inside.
	pt := mustGeom(t, "POINT(5 5)", 0)
	buf, err := GeomBuffer(pt, 2)
	if err != nil {
		t.Fatal(err)
	}
	if in, _ := pointInPolygon(5, 5, buf); !in {
		t.Error("buffer must contain its own centre")
	}
	if a := GeomArea(buf, false); math.Abs(a-math.Pi*4) > 0.1 {
		t.Errorf("circle-buffer area = %v (want ~%v)", a, math.Pi*4)
	}

	// Intersection of two overlapping convex squares.
	sq1 := mustGeom(t, "POLYGON((0 0, 4 0, 4 4, 0 4, 0 0))", 0)
	sq2 := mustGeom(t, "POLYGON((2 2, 6 2, 6 6, 2 6, 2 2))", 0)
	inter, err := GeomIntersection(sq1, sq2)
	if err != nil {
		t.Fatal(err)
	}
	if a := GeomArea(inter, false); math.Abs(a-4) > 1e-9 {
		t.Errorf("intersection area = %v (want 4)", a)
	}

	// Union of disjoint squares.
	sq3 := mustGeom(t, "POLYGON((100 100, 101 100, 101 101, 100 101, 100 100))", 0)
	union, err := GeomUnion(sq1, sq3)
	if err != nil {
		t.Fatal(err)
	}
	if union.Type != ogcMultiPolygon || len(union.Parts) != 2 {
		t.Fatalf("disjoint union should be a MultiPolygon of 2: %+v", union)
	}

	// Difference: disjoint b leaves a unchanged.
	diff, err := GeomDifference(sq1, sq3)
	if err != nil {
		t.Fatal(err)
	}
	if GeomArea(diff, false) != GeomArea(sq1, false) {
		t.Errorf("difference with a disjoint operand should be unchanged")
	}
	// Difference: b fully contains a -> empty.
	big := mustGeom(t, "POLYGON((-10 -10, 10 -10, 10 10, -10 10, -10 -10))", 0)
	empty, err := GeomDifference(sq1, big)
	if err != nil || empty != nil {
		t.Errorf("a fully inside b should difference to empty: %+v %v", empty, err)
	}
}

// TestSpatialPredicateSelfCases covers two real bugs found and fixed after
// initial S2 landing: ST_Contains(A, A) must be true (a solid polygon
// contains an identical copy of itself — vertex/edge-midpoint sampling
// alone never finds a genuinely interior point when b's boundary coincides
// exactly with a's, so GeomContains also probes each polygonal part of b's
// own centroid), and ST_Touches(A, A) must be false (two identical or
// fully-covering geometries are not "touching" — touches uses covers, not
// strict contains, on both sides so it correctly excludes the identical
// and full-containment cases, not just the strict-containment case).
func TestSpatialPredicateSelfCases(t *testing.T) {
	sqA := mustGeom(t, "POLYGON((0 0, 4 0, 4 4, 0 4, 0 0))", 0)
	sqA2 := mustGeom(t, "POLYGON((0 0, 4 0, 4 4, 0 4, 0 0))", 0)
	sqAdjacent := mustGeom(t, "POLYGON((4 0, 8 0, 8 4, 4 4, 4 0))", 0) // shares the x=4 edge
	small := mustGeom(t, "POLYGON((1 1, 2 1, 2 2, 1 2, 1 1))", 0)      // strictly inside sqA

	if !GeomContains(sqA, sqA2, false) {
		t.Error("ST_Contains(A, A) must be true")
	}
	if !GeomContains(sqA, small, false) {
		t.Error("ST_Contains(A, strictly-interior B) must be true")
	}
	if GeomContains(sqA, sqAdjacent, false) {
		t.Error("ST_Contains(A, non-overlapping-neighbor) must be false")
	}

	touches := func(a, b *Geom) bool {
		return GeomsIntersect(a, b) && !GeomContains(a, b, true) && !GeomContains(b, a, true)
	}
	if touches(sqA, sqA2) {
		t.Error("ST_Touches(A, A) must be false — identical geometries are not touching")
	}
	if !touches(sqA, sqAdjacent) {
		t.Error("ST_Touches(A, edge-adjacent-neighbor) must be true")
	}
}
