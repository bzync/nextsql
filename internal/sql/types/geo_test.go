package types

import (
	"math"
	"testing"
)

func TestPointBoxRoundTrip(t *testing.T) {
	p, err := PointValue(-73.9857, 40.7484)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := EncodeRow([]Value{p})
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRow(raw, []Type{Point()})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Lon != p.Lon || got[0].Lat != p.Lat {
		t.Fatalf("%+v", got[0])
	}
	b, err := BoxValue(-74.1, 40.6, -73.8, 40.9)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = EncodeRow([]Value{b})
	if err != nil {
		t.Fatal(err)
	}
	got, err = DecodeRow(raw, []Type{Box()})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Box != b.Box {
		t.Fatalf("%+v", got[0].Box)
	}
}

func TestInvalidCoordinates(t *testing.T) {
	if _, err := PointValue(200, 0); err == nil {
		t.Fatal("lon")
	}
	if _, err := PointValue(0, 100); err == nil {
		t.Fatal("lat")
	}
	if _, err := BoxValue(0, 10, 1, 0); err == nil {
		t.Fatal("south > north")
	}
}

func TestHaversineNYCtoLA(t *testing.T) {
	nyc := MustPoint(-74.0060, 40.7128)
	la := MustPoint(-118.2437, 34.0522)
	d, err := DistanceM(nyc, la)
	if err != nil {
		t.Fatal(err)
	}
	// ~3935–3945 km depending on ellipsoid; haversine on mean radius is ~3936 km
	if d < 3.90e6 || d > 3.98e6 {
		t.Fatalf("distance %f", d)
	}
	same, err := DistanceM(nyc, nyc)
	if err != nil || same > 1e-6 {
		t.Fatalf("zero distance %f %v", same, err)
	}
}

func TestPointInBoxAndWrap(t *testing.T) {
	p := MustPoint(170, 0)
	box, err := BoxValue(160, -10, 175, 10)
	if err != nil {
		t.Fatal(err)
	}
	in, err := PointInBox(p, box)
	if err != nil || !in {
		t.Fatal(in, err)
	}
	wrap, err := BoxValue(170, -10, -170, 10)
	if err != nil {
		t.Fatal(err)
	}
	in, err = PointInBox(MustPoint(175, 0), wrap)
	if err != nil || !in {
		t.Fatal("wrap east")
	}
	in, err = PointInBox(MustPoint(-175, 0), wrap)
	if err != nil || !in {
		t.Fatal("wrap west")
	}
	in, err = PointInBox(MustPoint(0, 0), wrap)
	if err != nil || in {
		t.Fatal("wrap mid")
	}
}

func TestWKTParse(t *testing.T) {
	p, err := ParseWKT("POINT(-73.98 40.75)")
	if err != nil || p.Lon != -73.98 || p.Lat != 40.75 {
		t.Fatalf("%+v %v", p, err)
	}
	b, err := ParseWKT("BOX(-74 40, -73 41)")
	if err != nil || b.Box[0] != -74 || b.Box[3] != 41 {
		t.Fatalf("%+v %v", b, err)
	}
	ln, err := ParseWKT("LINESTRING(-74 40, -73 41, -72 40)")
	if err != nil || !ln.IsLine() || len(ln.Coords) != 6 {
		t.Fatalf("line %+v %v", ln, err)
	}
	poly, err := ParseWKT("POLYGON((-74 40, -73 40, -73 41, -74 41, -74 40))")
	if err != nil || !poly.IsPolygon() || len(poly.Rings) != 1 || poly.Rings[0] != 5 {
		t.Fatalf("poly %+v %v", poly, err)
	}
	if _, err := ParseWKT("POLYGON((-74 40, -73 40, -73 41))"); err == nil {
		t.Fatal("unclosed")
	}
	if _, err := ParseWKT("LINESTRING(200 0, 0 0)"); err == nil {
		t.Fatal("bad lon")
	}
}

func TestGeoHashNearbySharePrefix(t *testing.T) {
	a := GeoHash64(-73.9857, 40.7484)
	b := GeoHash64(-73.9858, 40.7485)
	c := GeoHash64(139.6917, 35.6895) // Tokyo
	if a^b == 0 {
		t.Fatal("identical hashes for distinct points")
	}
	// nearby points share many leading bits
	if bits := bitsLeading(a ^ b); bits < 20 {
		t.Fatalf("nearby prefix %d", bits)
	}
	if bits := bitsLeading(a ^ c); bits > 8 {
		t.Fatalf("distant prefix %d", bits)
	}
}

func bitsLeading(x uint64) int {
	n := 0
	for i := 63; i >= 0; i-- {
		if x&(1<<uint(i)) != 0 {
			break
		}
		n++
	}
	return n
}

func TestEvalGeoPointDistance(t *testing.T) {
	lon, _ := ParseDecimal("-73.98")
	lat, _ := ParseDecimal("40.75")
	p, ok, err := EvalGeo("point", []Value{
		DecimalValue(lon, Type{Kind: KindDecimal}),
		DecimalValue(lat, Type{Kind: KindDecimal}),
	})
	if err != nil || !ok || math.Abs(p.Lon+73.98) > 1e-9 {
		t.Fatalf("%+v %v %v", p, ok, err)
	}
	d, ok, err := EvalGeo("st_distance", []Value{p, p})
	if err != nil || !ok || d.Dec.String() != "0.000" {
		t.Fatalf("dist %+v %v %v", d, ok, err)
	}
}

func TestPolygonPointInAndHole(t *testing.T) {
	poly, err := ParseWKT("POLYGON((-74.1 40.6, -73.8 40.6, -73.8 40.9, -74.1 40.9, -74.1 40.6))")
	if err != nil {
		t.Fatal(err)
	}
	in, err := PointInPolygon(MustPoint(-73.9857, 40.7484), poly)
	if err != nil || !in {
		t.Fatalf("empire in manhattan box: %v %v", in, err)
	}
	in, err = PointInPolygon(MustPoint(139.6917, 35.6895), poly)
	if err != nil || in {
		t.Fatalf("tokyo not in box: %v %v", in, err)
	}
	holed, err := ParseWKT("POLYGON((-74.1 40.6, -73.8 40.6, -73.8 40.9, -74.1 40.9, -74.1 40.6), (-74.00 40.72, -73.97 40.72, -73.97 40.76, -74.00 40.76, -74.00 40.72))")
	if err != nil {
		t.Fatal(err)
	}
	in, err = PointInPolygon(MustPoint(-73.9857, 40.7484), holed)
	if err != nil || in {
		t.Fatalf("empire in hole: %v %v", in, err)
	}
	in, err = PointInPolygon(MustPoint(-74.05, 40.65), holed)
	if err != nil || !in {
		t.Fatalf("downtown still inside: %v %v", in, err)
	}
	// WITHIN is strict interior; COVERS includes polygon boundaries, including
	// the boundary of a hole.
	holeBoundary := MustPoint(-74.00, 40.74)
	covered, err := PointInPolygon(holeBoundary, holed)
	if err != nil || !covered {
		t.Fatalf("hole boundary covered: %v %v", covered, err)
	}
	within, err := PointWithinPolygon(holeBoundary, holed)
	if err != nil || within {
		t.Fatalf("hole boundary not within: %v %v", within, err)
	}
	exteriorBoundary := MustPoint(-74.1, 40.7)
	covered, err = PointInPolygon(exteriorBoundary, holed)
	if err != nil || !covered {
		t.Fatalf("exterior boundary covered: %v %v", covered, err)
	}
	within, err = PointWithinPolygon(exteriorBoundary, holed)
	if err != nil || within {
		t.Fatalf("exterior boundary not within: %v %v", within, err)
	}
}

func TestLineLengthAndDWithin(t *testing.T) {
	ln, err := ParseWKT("LINESTRING(-74.0060 40.7128, -73.9857 40.7484)")
	if err != nil {
		t.Fatal(err)
	}
	m, err := LineLengthM(ln)
	if err != nil || m < 3000 || m > 6000 {
		t.Fatalf("battery to empire ~4.5km, got %f %v", m, err)
	}
	on, err := DistanceAny(MustPoint(-74.0060, 40.7128), ln)
	if err != nil || on > 1 {
		t.Fatalf("endpoint %f %v", on, err)
	}
	far, err := DistanceAny(MustPoint(139.6917, 35.6895), ln)
	if err != nil || far < 1e7 {
		t.Fatalf("tokyo to nyc line %f %v", far, err)
	}
}

func TestRichGeometryDistanceAndTopology(t *testing.T) {
	crossA, err := ParseWKT("LINESTRING(0 0, 2 2)")
	if err != nil {
		t.Fatal(err)
	}
	crossB, err := ParseWKT("LINESTRING(0 2, 2 0)")
	if err != nil {
		t.Fatal(err)
	}
	d, err := DistanceAny(crossA, crossB)
	if err != nil || d != 0 {
		t.Fatalf("crossing lines distance=%f err=%v", d, err)
	}
	intersects, err := GeometriesIntersect(crossA, crossB)
	if err != nil || !intersects {
		t.Fatalf("crossing lines intersect=%v err=%v", intersects, err)
	}

	poly, err := ParseWKT("POLYGON((0 0, 2 0, 2 2, 0 2, 0 0))")
	if err != nil {
		t.Fatal(err)
	}
	through, _ := ParseWKT("LINESTRING(-1 1, 3 1)")
	d, err = DistanceAny(through, poly)
	if err != nil || d != 0 {
		t.Fatalf("line/polygon distance=%f err=%v", d, err)
	}
	disjoint, _ := ParseWKT("LINESTRING(3 3, 4 4)")
	d, err = DistanceAny(disjoint, poly)
	if err != nil || d < 100000 {
		t.Fatalf("disjoint line/polygon distance=%f err=%v", d, err)
	}

	overlap, _ := ParseWKT("POLYGON((1 1, 3 1, 3 3, 1 3, 1 1))")
	d, err = DistanceAny(poly, overlap)
	if err != nil || d != 0 {
		t.Fatalf("overlapping polygons distance=%f err=%v", d, err)
	}
	far, _ := ParseWKT("POLYGON((4 4, 5 4, 5 5, 4 5, 4 4))")
	d, err = DistanceAny(poly, far)
	if err != nil || d < 100000 {
		t.Fatalf("disjoint polygons distance=%f err=%v", d, err)
	}

	box, _ := BoxValue(0, 0, 2, 2)
	d, err = DistanceAny(MustPoint(1, 1), box)
	if err != nil || d != 0 {
		t.Fatalf("point/box distance=%f err=%v", d, err)
	}
	wrap, _ := BoxValue(170, -10, -170, 10)
	d, err = DistanceAny(MustPoint(175, 0), wrap)
	if err != nil || d != 0 {
		t.Fatalf("point/wrapping-box distance=%f err=%v", d, err)
	}
}

func TestRichGeometryMeasurementsAndInspection(t *testing.T) {
	poly, err := ParseWKT("POLYGON((0 0, 1 0, 1 1, 0 1, 0 0))")
	if err != nil {
		t.Fatal(err)
	}
	area, err := PolygonAreaM2(poly)
	if err != nil || area < 1.2e10 || area > 1.3e10 {
		t.Fatalf("one-degree area=%f err=%v", area, err)
	}
	perimeter, err := PolygonPerimeterM(poly)
	if err != nil || perimeter < 440000 || perimeter > 450000 {
		t.Fatalf("one-degree perimeter=%f err=%v", perimeter, err)
	}
	center, err := GeoCentroid(poly)
	if err != nil || math.Abs(center.Lon-.5) > 1e-12 || math.Abs(center.Lat-.5) > 1e-12 {
		t.Fatalf("centroid=%+v err=%v", center, err)
	}
	env, err := GeoEnvelope(poly)
	if err != nil || env.Box != [4]float64{0, 0, 1, 1} {
		t.Fatalf("envelope=%+v err=%v", env, err)
	}
	name, err := GeometryTypeName(poly)
	if err != nil || name != "POLYGON" {
		t.Fatalf("type=%q err=%v", name, err)
	}
	n, err := GeometryPointCount(poly)
	if err != nil || n != 5 {
		t.Fatalf("npoints=%d err=%v", n, err)
	}

	areaValue, ok, err := EvalGeo("st_area", []Value{poly})
	if err != nil || !ok || areaValue.Dec.String() == "0.000" {
		t.Fatalf("ST_Area=%+v ok=%v err=%v", areaValue, ok, err)
	}
	typeValue, ok, err := EvalGeo("st_geometrytype", []Value{poly})
	if err != nil || !ok || typeValue.Str != "POLYGON" {
		t.Fatalf("ST_GeometryType=%+v ok=%v err=%v", typeValue, ok, err)
	}
	countValue, ok, err := EvalGeo("st_nrings", []Value{poly})
	if err != nil || !ok || countValue.Dec.String() != "1" {
		t.Fatalf("ST_NRings=%+v ok=%v err=%v", countValue, ok, err)
	}
}

func TestPolygonRejectsInvalidTopology(t *testing.T) {
	for _, wkt := range []string{
		"POLYGON((0 0, 2 2, 0 2, 2 0, 0 0))",
		"POLYGON((0 0, 4 0, 4 4, 0 4, 0 0), (5 5, 6 5, 6 6, 5 6, 5 5))",
		"POLYGON((0 0, 8 0, 8 8, 0 8, 0 0), (1 1, 5 1, 5 5, 1 5, 1 1), (4 4, 7 4, 7 7, 4 7, 4 4))",
	} {
		if _, err := ParseWKT(wkt); err == nil {
			t.Fatalf("expected invalid topology: %s", wkt)
		}
	}
}

func TestSpheroidNYCtoLA(t *testing.T) {
	nyc := MustPoint(-74.0060, 40.7128)
	la := MustPoint(-118.2437, 34.0522)
	sphere, err := DistanceM(nyc, la)
	if err != nil {
		t.Fatal(err)
	}
	ellip, err := DistanceSpheroidM(nyc, la)
	if err != nil {
		t.Fatal(err)
	}
	if ellip < 3.943e6 || ellip > 3.946e6 {
		t.Fatalf("WGS84 NYC-LA %f", ellip)
	}
	if math.Abs(ellip-sphere) < 1000 {
		t.Fatalf("spheroid should differ from haversine: sphere=%f ellip=%f", sphere, ellip)
	}
	same, err := DistanceSpheroidM(nyc, nyc)
	if err != nil || same != 0 {
		t.Fatalf("zero %f %v", same, err)
	}
}

func TestGeoEncodeRoundTrip(t *testing.T) {
	ln, err := LineValue([]float64{-74, 40, -73, 41})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := EncodeRow([]Value{ln})
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRow(raw, []Type{Line()})
	if err != nil {
		t.Fatal(err)
	}
	if cmp, err := got[0].Cmp(ln); err != nil || cmp != 0 {
		t.Fatalf("line %+v %v", got[0], err)
	}
	poly, err := ParseWKT("POLYGON((-74 40, -73 40, -73 41, -74 41, -74 40))")
	if err != nil {
		t.Fatal(err)
	}
	raw, err = EncodeRow([]Value{poly})
	if err != nil {
		t.Fatal(err)
	}
	got, err = DecodeRow(raw, []Type{Polygon()})
	if err != nil {
		t.Fatal(err)
	}
	if cmp, err := got[0].Cmp(poly); err != nil || cmp != 0 {
		t.Fatalf("poly %+v %v", got[0], err)
	}
	too := make([]float64, (MaxGeoVertices+1)*2)
	if _, err := LineValue(too); err == nil {
		t.Fatal("vertex limit")
	}
}

func TestGeoKeyRoundTripOrder(t *testing.T) {
	k1, err := EncodeGeoKey(10, []Value{StringValue("a")})
	if err != nil {
		t.Fatal(err)
	}
	k2, err := EncodeGeoKey(11, []Value{StringValue("a")})
	if err != nil {
		t.Fatal(err)
	}
	if string(k1) >= string(k2) {
		t.Fatal("hash key order")
	}
}
