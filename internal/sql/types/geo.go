package types

import (
	"encoding/binary"
	"math"
	"math/bits"
	"strconv"
	"strings"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
)

// Mean Earth radius in meters (IUGG). Used by haversine DISTANCE.
const EarthRadiusM = 6371008.8

// WGS84 ellipsoid (EPSG:4326). Used by DISTANCE_SPHEROID.
const (
	WGS84A = 6378137.0
	WGS84F = 1 / 298.257223563
)

func Point() Type   { return Type{Kind: KindPoint} }
func Box() Type     { return Type{Kind: KindBox} }
func Line() Type    { return Type{Kind: KindLine} }
func Polygon() Type { return Type{Kind: KindPolygon} }

func PointValue(lon, lat float64) (Value, error) {
	if err := checkLonLat(lon, lat); err != nil {
		return Value{}, err
	}
	return Value{Typ: Point(), Lon: lon, Lat: lat}, nil
}

func BoxValue(west, south, east, north float64) (Value, error) {
	if err := checkLonLat(west, south); err != nil {
		return Value{}, err
	}
	if err := checkLonLat(east, north); err != nil {
		return Value{}, err
	}
	if south > north {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.BoxValue", "BOX south is greater than north")
	}
	return Value{Typ: Box(), Box: [4]float64{west, south, east, north}}, nil
}

func MustPoint(lon, lat float64) Value {
	v, err := PointValue(lon, lat)
	if err != nil {
		panic(err)
	}
	return v
}

// LineValue stores interleaved lon, lat. At least two vertices.
func LineValue(coords []float64) (Value, error) {
	if err := validateLine(coords); err != nil {
		return Value{}, err
	}
	return Value{Typ: Line(), Coords: append([]float64(nil), coords...)}, nil
}

// PolygonValue stores concatenated rings. Each ring is closed (first == last).
func PolygonValue(coords []float64, rings []int) (Value, error) {
	if err := validatePolygon(coords, rings); err != nil {
		return Value{}, err
	}
	return Value{
		Typ:    Polygon(),
		Coords: append([]float64(nil), coords...),
		Rings:  append([]int(nil), rings...),
	}, nil
}

func checkLonLat(lon, lat float64) error {
	if math.IsNaN(lon) || math.IsNaN(lat) || math.IsInf(lon, 0) || math.IsInf(lat, 0) {
		return nerr.New(nerr.InvalidArgument, "types.checkLonLat", "coordinate is not finite")
	}
	if lon < -180 || lon > 180 {
		return nerr.New(nerr.InvalidArgument, "types.checkLonLat", "longitude out of range")
	}
	if lat < -90 || lat > 90 {
		return nerr.New(nerr.InvalidArgument, "types.checkLonLat", "latitude out of range")
	}
	return nil
}

func validateLine(coords []float64) error {
	if len(coords)%2 != 0 || len(coords) < 4 {
		return nerr.New(nerr.InvalidArgument, "types.validateLine", "LINESTRING requires at least 2 vertices")
	}
	n := len(coords) / 2
	if n > MaxGeoVertices {
		return nerr.New(nerr.InvalidArgument, "types.validateLine", "LINESTRING exceeds vertex limit")
	}
	for i := 0; i < n; i++ {
		if err := checkLonLat(coords[i*2], coords[i*2+1]); err != nil {
			return err
		}
	}
	if geoLonSpan(coords) > 180 {
		return nerr.New(nerr.InvalidArgument, "types.validateLine", "LINESTRING crosses the antimeridian")
	}
	return nil
}

func validatePolygon(coords []float64, rings []int) error {
	if len(rings) < 1 {
		return nerr.New(nerr.InvalidArgument, "types.validatePolygon", "POLYGON requires an exterior ring")
	}
	sum := 0
	for _, n := range rings {
		if n < 4 {
			return nerr.New(nerr.InvalidArgument, "types.validatePolygon", "POLYGON ring must have at least 4 vertices")
		}
		sum += n
	}
	if sum*2 != len(coords) {
		return nerr.New(nerr.InvalidArgument, "types.validatePolygon", "POLYGON ring sizes do not match coordinates")
	}
	if sum > MaxGeoVertices {
		return nerr.New(nerr.InvalidArgument, "types.validatePolygon", "POLYGON exceeds vertex limit")
	}
	off := 0
	ringViews := make([][]float64, 0, len(rings))
	for _, n := range rings {
		ring := coords[off : off+n*2]
		for i := 0; i < n; i++ {
			if err := checkLonLat(ring[i*2], ring[i*2+1]); err != nil {
				return err
			}
		}
		if ring[0] != ring[(n-1)*2] || ring[1] != ring[(n-1)*2+1] {
			return nerr.New(nerr.InvalidArgument, "types.validatePolygon", "POLYGON ring is not closed")
		}
		if geoLonSpan(ring) > 180 {
			return nerr.New(nerr.InvalidArgument, "types.validatePolygon", "POLYGON ring crosses the antimeridian")
		}
		if ringArea2(ring) == 0 {
			return nerr.New(nerr.InvalidArgument, "types.validatePolygon", "POLYGON ring has zero area")
		}
		if ringSelfIntersects(ring) {
			return nerr.New(nerr.InvalidArgument, "types.validatePolygon", "POLYGON ring self-intersects")
		}
		ringViews = append(ringViews, ring)
		off += n * 2
	}
	for i := 1; i < len(ringViews); i++ {
		state := pointInRingState(ringViews[i][0], ringViews[i][1], ringViews[0])
		if state != pointInside || ringsIntersect(ringViews[i], ringViews[0]) {
			return nerr.New(nerr.InvalidArgument, "types.validatePolygon", "POLYGON hole is not strictly inside the exterior ring")
		}
		for j := 1; j < i; j++ {
			if ringsIntersect(ringViews[i], ringViews[j]) ||
				pointInRingState(ringViews[i][0], ringViews[i][1], ringViews[j]) != pointOutside ||
				pointInRingState(ringViews[j][0], ringViews[j][1], ringViews[i]) != pointOutside {
				return nerr.New(nerr.InvalidArgument, "types.validatePolygon", "POLYGON holes overlap or nest")
			}
		}
	}
	return nil
}

func ringArea2(coords []float64) float64 {
	var area float64
	for i := 0; i+3 < len(coords); i += 2 {
		area += coords[i]*coords[i+3] - coords[i+2]*coords[i+1]
	}
	return area
}

func ringSelfIntersects(ring []float64) bool {
	n := len(ring)/2 - 1
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if j == i+1 || i == 0 && j == n-1 {
				continue
			}
			if segmentsIntersect(
				ring[i*2], ring[i*2+1], ring[(i+1)*2], ring[(i+1)*2+1],
				ring[j*2], ring[j*2+1], ring[(j+1)*2], ring[(j+1)*2+1],
			) {
				return true
			}
		}
	}
	return false
}

func ringsIntersect(a, b []float64) bool {
	for i := 0; i+3 < len(a); i += 2 {
		for j := 0; j+3 < len(b); j += 2 {
			if segmentsIntersect(a[i], a[i+1], a[i+2], a[i+3], b[j], b[j+1], b[j+2], b[j+3]) {
				return true
			}
		}
	}
	return false
}

func geoLonSpan(coords []float64) float64 {
	if len(coords) < 2 {
		return 0
	}
	min, max := coords[0], coords[0]
	for i := 0; i < len(coords); i += 2 {
		if coords[i] < min {
			min = coords[i]
		}
		if coords[i] > max {
			max = coords[i]
		}
	}
	return max - min
}

func (v Value) IsPoint() bool   { return !v.Null && v.Typ.Kind == KindPoint }
func (v Value) IsBox() bool     { return !v.Null && v.Typ.Kind == KindBox }
func (v Value) IsLine() bool    { return !v.Null && v.Typ.Kind == KindLine }
func (v Value) IsPolygon() bool { return !v.Null && v.Typ.Kind == KindPolygon }

func formatCoord(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func formatPoint(lon, lat float64) string {
	return "POINT(" + formatCoord(lon) + " " + formatCoord(lat) + ")"
}

func formatBox(b [4]float64) string {
	return "BOX(" + formatCoord(b[0]) + " " + formatCoord(b[1]) + ", " + formatCoord(b[2]) + " " + formatCoord(b[3]) + ")"
}

func formatPairs(coords []float64) string {
	var b strings.Builder
	for i := 0; i+1 < len(coords); i += 2 {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(formatCoord(coords[i]))
		b.WriteByte(' ')
		b.WriteString(formatCoord(coords[i+1]))
	}
	return b.String()
}

func formatLine(coords []float64) string {
	return "LINESTRING(" + formatPairs(coords) + ")"
}

func formatPolygon(coords []float64, rings []int) string {
	var b strings.Builder
	b.WriteString("POLYGON(")
	off := 0
	for i, n := range rings {
		if i > 0 {
			b.WriteString(", ")
		}
		end := off + n*2
		if end > len(coords) {
			end = len(coords)
		}
		b.WriteByte('(')
		b.WriteString(formatPairs(coords[off:end]))
		b.WriteByte(')')
		off = end
	}
	b.WriteByte(')')
	return b.String()
}

// Haversine distance in meters between two WGS84 points.
func DistanceM(a, b Value) (float64, error) {
	if !a.IsPoint() || !b.IsPoint() {
		return 0, nerr.New(nerr.InvalidArgument, "types.DistanceM", "DISTANCE requires POINT")
	}
	return haversine(a.Lon, a.Lat, b.Lon, b.Lat), nil
}

func haversine(lon1, lat1, lon2, lat2 float64) float64 {
	φ1 := lat1 * math.Pi / 180
	φ2 := lat2 * math.Pi / 180
	dφ := (lat2 - lat1) * math.Pi / 180
	dλ := (lon2 - lon1) * math.Pi / 180
	s := math.Sin(dφ/2)*math.Sin(dφ/2) + math.Cos(φ1)*math.Cos(φ2)*math.Sin(dλ/2)*math.Sin(dλ/2)
	return 2 * EarthRadiusM * math.Asin(math.Min(1, math.Sqrt(s)))
}

// DistanceSpheroidM is the Vincenty inverse distance on the WGS84 ellipsoid.
// If the iteration does not converge (near-antipodal), it falls back to haversine.
func DistanceSpheroidM(a, b Value) (float64, error) {
	if !a.IsPoint() || !b.IsPoint() {
		return 0, nerr.New(nerr.InvalidArgument, "types.DistanceSpheroidM", "DISTANCE_SPHEROID requires POINT")
	}
	return vincentyInverse(a.Lon, a.Lat, b.Lon, b.Lat), nil
}

func vincentyInverse(lon1, lat1, lon2, lat2 float64) float64 {
	if lon1 == lon2 && lat1 == lat2 {
		return 0
	}
	a := WGS84A
	f := WGS84F
	bAxis := a * (1 - f)
	φ1 := lat1 * math.Pi / 180
	φ2 := lat2 * math.Pi / 180
	L := (lon2 - lon1) * math.Pi / 180
	U1 := math.Atan((1 - f) * math.Tan(φ1))
	U2 := math.Atan((1 - f) * math.Tan(φ2))
	sinU1, cosU1 := math.Sincos(U1)
	sinU2, cosU2 := math.Sincos(U2)
	λ := L
	var cos2σm, sinσ, cosσ, σ, sinα, cos2α float64
	converged := false
	for i := 0; i < 100; i++ {
		sinλ, cosλ := math.Sincos(λ)
		sinσ = math.Sqrt((cosU2*sinλ)*(cosU2*sinλ) + (cosU1*sinU2-sinU1*cosU2*cosλ)*(cosU1*sinU2-sinU1*cosU2*cosλ))
		if sinσ == 0 {
			return 0
		}
		cosσ = sinU1*sinU2 + cosU1*cosU2*cosλ
		σ = math.Atan2(sinσ, cosσ)
		sinα = cosU1 * cosU2 * sinλ / sinσ
		cos2α = 1 - sinα*sinα
		if cos2α == 0 {
			cos2σm = 0
		} else {
			cos2σm = cosσ - 2*sinU1*sinU2/cos2α
		}
		C := f / 16 * cos2α * (2 + f*(4-3*cos2α))
		λPrev := λ
		λ = L + (1-C)*f*sinα*(σ+C*sinσ*(cos2σm+C*cosσ*(-1+2*cos2σm*cos2σm)))
		if math.Abs(λ-λPrev) < 1e-12 {
			converged = true
			break
		}
	}
	if !converged {
		return haversine(lon1, lat1, lon2, lat2)
	}
	u2 := cos2α * (a*a - bAxis*bAxis) / (bAxis * bAxis)
	A := 1 + u2/16384*(4096+u2*(-768+u2*(320-175*u2)))
	B := u2 / 1024 * (256 + u2*(-128+u2*(74-47*u2)))
	Δσ := B * sinσ * (cos2σm + B/4*(cosσ*(-1+2*cos2σm*cos2σm)-B/6*cos2σm*(-3+4*sinσ*sinσ)*(-3+4*cos2σm*cos2σm)))
	return bAxis * A * (σ - Δσ)
}

func PointInBox(p, box Value) (bool, error) {
	if !p.IsPoint() {
		return false, nerr.New(nerr.InvalidArgument, "types.PointInBox", "expected POINT")
	}
	if !box.IsBox() {
		return false, nerr.New(nerr.InvalidArgument, "types.PointInBox", "expected BOX")
	}
	w, s, e, n := box.Box[0], box.Box[1], box.Box[2], box.Box[3]
	if p.Lat < s || p.Lat > n {
		return false, nil
	}
	if w <= e {
		return p.Lon >= w && p.Lon <= e, nil
	}
	// antimeridian wrap: west..180 or -180..east
	return p.Lon >= w || p.Lon <= e, nil
}

// PointInPolygon reports polygon coverage: exterior and ring boundaries are
// included, while hole interiors are excluded. Rings do not cross antimeridian.
func PointInPolygon(p, poly Value) (bool, error) {
	if !p.IsPoint() {
		return false, nerr.New(nerr.InvalidArgument, "types.PointInPolygon", "expected POINT")
	}
	if !poly.IsPolygon() {
		return false, nerr.New(nerr.InvalidArgument, "types.PointInPolygon", "expected POLYGON")
	}
	off := 0
	covered := false
	for i, n := range poly.Rings {
		end := off + n*2
		if end > len(poly.Coords) {
			return false, nerr.New(nerr.InvalidArgument, "types.PointInPolygon", "truncated ring")
		}
		state := pointInRingState(p.Lon, p.Lat, poly.Coords[off:end])
		if i == 0 {
			covered = state != pointOutside
		} else if state == pointInside {
			covered = false
		} else if state == pointBoundary {
			return true, nil
		}
		off = end
	}
	return covered, nil
}

func PointWithinPolygon(p, poly Value) (bool, error) {
	if !p.IsPoint() || !poly.IsPolygon() {
		return false, nerr.New(nerr.InvalidArgument, "types.PointWithinPolygon", "expected POINT and POLYGON")
	}
	off := 0
	for i, n := range poly.Rings {
		end := off + n*2
		if end > len(poly.Coords) {
			return false, nerr.New(nerr.InvalidArgument, "types.PointWithinPolygon", "truncated ring")
		}
		state := pointInRingState(p.Lon, p.Lat, poly.Coords[off:end])
		if i == 0 && state != pointInside || i > 0 && state != pointOutside {
			return false, nil
		}
		off = end
	}
	return true, nil
}

func pointInRing(lon, lat float64, coords []float64) bool {
	return pointInRingState(lon, lat, coords) != pointOutside
}

const (
	pointOutside = iota
	pointInside
	pointBoundary
)

func pointInRingState(lon, lat float64, coords []float64) int {
	n := len(coords) / 2
	if n < 4 {
		return pointOutside
	}
	inside := false
	j := n - 1
	for i := 0; i < n; i++ {
		xi, yi := coords[i*2], coords[i*2+1]
		xj, yj := coords[j*2], coords[j*2+1]
		if pointOnSegment(lon, lat, xi, yi, xj, yj) {
			return pointBoundary
		}
		if (yi > lat) != (yj > lat) {
			xint := (xj-xi)*(lat-yi)/(yj-yi) + xi
			if lon < xint {
				inside = !inside
			}
		}
		j = i
	}
	if inside {
		return pointInside
	}
	return pointOutside
}

func PointInGeom(p, geom Value) (bool, error) {
	switch {
	case geom.IsBox():
		return PointInBox(p, geom)
	case geom.IsPolygon():
		return PointInPolygon(p, geom)
	default:
		return false, nerr.New(nerr.InvalidArgument, "types.PointInGeom", "expected BOX or POLYGON")
	}
}

func PointWithinGeom(p, geom Value) (bool, error) {
	switch {
	case geom.IsBox():
		if !p.IsPoint() {
			return false, nerr.New(nerr.InvalidArgument, "types.PointWithinGeom", "expected POINT")
		}
		w, s, e, n := geom.Box[0], geom.Box[1], geom.Box[2], geom.Box[3]
		if p.Lat <= s || p.Lat >= n {
			return false, nil
		}
		if w <= e {
			return p.Lon > w && p.Lon < e, nil
		}
		return p.Lon > w || p.Lon < e, nil
	case geom.IsPolygon():
		return PointWithinPolygon(p, geom)
	default:
		return false, nerr.New(nerr.InvalidArgument, "types.PointWithinGeom", "expected BOX or POLYGON")
	}
}

// DistanceAny is the shortest distance in meters between supported geometries.
// Segment closest-points use a local equirectangular projection followed by
// haversine. Topological intersections return zero before distance estimation.
func DistanceAny(a, b Value) (float64, error) {
	if a.IsBox() {
		return distanceBoxToGeom(a, b)
	}
	if b.IsBox() {
		return distanceBoxToGeom(b, a)
	}
	if a.IsPoint() && b.IsPoint() {
		return haversine(a.Lon, a.Lat, b.Lon, b.Lat), nil
	}
	if a.IsPoint() && b.IsLine() {
		return pointToLine(a.Lon, a.Lat, b.Coords), nil
	}
	if b.IsPoint() && a.IsLine() {
		return pointToLine(b.Lon, b.Lat, a.Coords), nil
	}
	if a.IsPoint() && b.IsPolygon() {
		return pointToPolygon(a, b)
	}
	if b.IsPoint() && a.IsPolygon() {
		return pointToPolygon(b, a)
	}
	if a.IsLine() && b.IsLine() {
		return lineToLine(a.Coords, b.Coords), nil
	}
	if a.IsLine() && b.IsPolygon() {
		return lineToPolygon(a, b)
	}
	if b.IsLine() && a.IsPolygon() {
		return lineToPolygon(b, a)
	}
	if a.IsPolygon() && b.IsPolygon() {
		return polygonToPolygon(a, b)
	}
	return 0, nerr.New(nerr.InvalidArgument, "types.DistanceAny", "unsupported geometry pair")
}

func distanceBoxToGeom(box, geom Value) (float64, error) {
	polys, err := boxPolygons(box)
	if err != nil {
		return 0, err
	}
	best := math.Inf(1)
	for _, poly := range polys {
		d, err := DistanceAny(poly, geom)
		if err != nil {
			return 0, err
		}
		if d < best {
			best = d
		}
	}
	return best, nil
}

func boxPolygons(box Value) ([]Value, error) {
	if !box.IsBox() {
		return nil, nerr.New(nerr.InvalidArgument, "types.boxPolygons", "expected BOX")
	}
	w, s, e, n := box.Box[0], box.Box[1], box.Box[2], box.Box[3]
	makePart := func(left, right float64) (Value, error) {
		switch {
		case left == right && s == n:
			return PointValue(left, s)
		case left == right:
			return LineValue([]float64{left, s, left, n})
		case s == n:
			return LineValue([]float64{left, s, right, s})
		default:
			return PolygonValue([]float64{left, s, right, s, right, n, left, n, left, s}, []int{5})
		}
	}
	if w <= e {
		if e-w <= 180 {
			p, err := makePart(w, e)
			return []Value{p}, err
		}
		left, err := makePart(w, w+180)
		if err != nil {
			return nil, err
		}
		right, err := makePart(w+180, e)
		if err != nil {
			return nil, err
		}
		return []Value{left, right}, nil
	}
	left, err := makePart(w, 180)
	if err != nil {
		return nil, err
	}
	right, err := makePart(-180, e)
	if err != nil {
		return nil, err
	}
	return []Value{left, right}, nil
}

func pointToPolygon(p, poly Value) (float64, error) {
	in, err := PointInPolygon(p, poly)
	if err != nil {
		return 0, err
	}
	if in {
		return 0, nil
	}
	best := math.Inf(1)
	off := 0
	for _, n := range poly.Rings {
		end := off + n*2
		if end > len(poly.Coords) {
			return 0, nerr.New(nerr.InvalidArgument, "types.pointToPolygon", "truncated ring")
		}
		if d := pointToLine(p.Lon, p.Lat, poly.Coords[off:end]); d < best {
			best = d
		}
		off = end
	}
	return best, nil
}

func lineToLine(a, b []float64) float64 {
	best := math.Inf(1)
	for i := 0; i+3 < len(a); i += 2 {
		for j := 0; j+3 < len(b); j += 2 {
			d := segmentDistance(a[i], a[i+1], a[i+2], a[i+3], b[j], b[j+1], b[j+2], b[j+3])
			if d == 0 {
				return 0
			}
			if d < best {
				best = d
			}
		}
	}
	return best
}

func lineToPolygon(line, poly Value) (float64, error) {
	for i := 0; i+1 < len(line.Coords); i += 2 {
		inside, err := PointInPolygon(MustPoint(line.Coords[i], line.Coords[i+1]), poly)
		if err != nil {
			return 0, err
		}
		if inside {
			return 0, nil
		}
	}
	best := math.Inf(1)
	off := 0
	for _, n := range poly.Rings {
		end := off + n*2
		if end > len(poly.Coords) {
			return 0, nerr.New(nerr.InvalidArgument, "types.lineToPolygon", "truncated ring")
		}
		if d := lineToLine(line.Coords, poly.Coords[off:end]); d < best {
			best = d
		}
		off = end
	}
	return best, nil
}

func polygonToPolygon(a, b Value) (float64, error) {
	if len(a.Coords) < 2 || len(b.Coords) < 2 {
		return 0, nerr.New(nerr.InvalidArgument, "types.polygonToPolygon", "empty POLYGON")
	}
	for _, pair := range [][2]Value{{MustPoint(a.Coords[0], a.Coords[1]), b}, {MustPoint(b.Coords[0], b.Coords[1]), a}} {
		inside, err := PointInPolygon(pair[0], pair[1])
		if err != nil {
			return 0, err
		}
		if inside {
			return 0, nil
		}
	}
	best := math.Inf(1)
	aoff := 0
	for _, an := range a.Rings {
		aend := aoff + an*2
		boff := 0
		for _, bn := range b.Rings {
			bend := boff + bn*2
			d := lineToLine(a.Coords[aoff:aend], b.Coords[boff:bend])
			if d == 0 {
				return 0, nil
			}
			if d < best {
				best = d
			}
			boff = bend
		}
		aoff = aend
	}
	return best, nil
}

func segmentDistance(ax, ay, bx, by, cx, cy, dx, dy float64) float64 {
	if segmentsIntersect(ax, ay, bx, by, cx, cy, dx, dy) {
		return 0
	}
	return math.Min(
		math.Min(distPointSeg(ax, ay, cx, cy, dx, dy), distPointSeg(bx, by, cx, cy, dx, dy)),
		math.Min(distPointSeg(cx, cy, ax, ay, bx, by), distPointSeg(dx, dy, ax, ay, bx, by)),
	)
}

func segmentsIntersect(ax, ay, bx, by, cx, cy, dx, dy float64) bool {
	o1 := orientation(ax, ay, bx, by, cx, cy)
	o2 := orientation(ax, ay, bx, by, dx, dy)
	o3 := orientation(cx, cy, dx, dy, ax, ay)
	o4 := orientation(cx, cy, dx, dy, bx, by)
	if o1 != o2 && o3 != o4 {
		return true
	}
	return o1 == 0 && pointOnSegment(cx, cy, ax, ay, bx, by) ||
		o2 == 0 && pointOnSegment(dx, dy, ax, ay, bx, by) ||
		o3 == 0 && pointOnSegment(ax, ay, cx, cy, dx, dy) ||
		o4 == 0 && pointOnSegment(bx, by, cx, cy, dx, dy)
}

func orientation(ax, ay, bx, by, cx, cy float64) int {
	v := (bx-ax)*(cy-ay) - (by-ay)*(cx-ax)
	scale := math.Max(1, math.Abs(bx-ax)+math.Abs(by-ay)+math.Abs(cx-ax)+math.Abs(cy-ay))
	eps := 1e-12 * scale
	if v > eps {
		return 1
	}
	if v < -eps {
		return -1
	}
	return 0
}

func pointOnSegment(px, py, ax, ay, bx, by float64) bool {
	if orientation(ax, ay, bx, by, px, py) != 0 {
		return false
	}
	eps := 1e-12
	return px >= math.Min(ax, bx)-eps && px <= math.Max(ax, bx)+eps &&
		py >= math.Min(ay, by)-eps && py <= math.Max(ay, by)+eps
}

func pointToLine(lon, lat float64, coords []float64) float64 {
	n := len(coords) / 2
	if n == 0 {
		return math.Inf(1)
	}
	if n == 1 {
		return haversine(lon, lat, coords[0], coords[1])
	}
	best := math.Inf(1)
	for i := 0; i < n-1; i++ {
		d := distPointSeg(lon, lat, coords[i*2], coords[i*2+1], coords[i*2+2], coords[i*2+3])
		if d < best {
			best = d
		}
	}
	return best
}

func distPointSeg(lon, lat, lon1, lat1, lon2, lat2 float64) float64 {
	φ := lat * math.Pi / 180
	cos := math.Cos(φ)
	if cos < 1e-6 {
		cos = 1e-6
	}
	x := (lon - lon1) * cos
	y := lat - lat1
	dx := (lon2 - lon1) * cos
	dy := lat2 - lat1
	len2 := dx*dx + dy*dy
	t := 0.0
	if len2 > 0 {
		t = (x*dx + y*dy) / len2
		if t < 0 {
			t = 0
		} else if t > 1 {
			t = 1
		}
	}
	return haversine(lon, lat, lon1+t*(lon2-lon1), lat1+t*(lat2-lat1))
}

func LineLengthM(line Value) (float64, error) {
	if !line.IsLine() {
		return 0, nerr.New(nerr.InvalidArgument, "types.LineLengthM", "LINELENGTH requires LINESTRING")
	}
	n := len(line.Coords) / 2
	if n < 2 {
		return 0, nil
	}
	var sum float64
	for i := 0; i < n-1; i++ {
		sum += haversine(line.Coords[i*2], line.Coords[i*2+1], line.Coords[i*2+2], line.Coords[i*2+3])
	}
	return sum, nil
}

// PolygonPerimeterM returns the spherical length of the exterior and every hole.
func PolygonPerimeterM(poly Value) (float64, error) {
	if !poly.IsPolygon() {
		return 0, nerr.New(nerr.InvalidArgument, "types.PolygonPerimeterM", "PERIMETER requires POLYGON")
	}
	var sum float64
	off := 0
	for _, n := range poly.Rings {
		end := off + n*2
		if end > len(poly.Coords) {
			return 0, nerr.New(nerr.InvalidArgument, "types.PolygonPerimeterM", "truncated ring")
		}
		for i := off; i+3 < end; i += 2 {
			sum += haversine(poly.Coords[i], poly.Coords[i+1], poly.Coords[i+2], poly.Coords[i+3])
		}
		off = end
	}
	return sum, nil
}

// PolygonAreaM2 returns spherical square meters using a bounded ring integral.
// The first ring contributes area and subsequent rings subtract their area.
func PolygonAreaM2(poly Value) (float64, error) {
	if !poly.IsPolygon() {
		return 0, nerr.New(nerr.InvalidArgument, "types.PolygonAreaM2", "AREA requires POLYGON")
	}
	var area float64
	off := 0
	for ri, n := range poly.Rings {
		end := off + n*2
		if end > len(poly.Coords) {
			return 0, nerr.New(nerr.InvalidArgument, "types.PolygonAreaM2", "truncated ring")
		}
		ringArea := sphericalRingArea(poly.Coords[off:end])
		if ri == 0 {
			area = ringArea
		} else {
			area -= ringArea
		}
		off = end
	}
	if area < 0 && area > -1e-6 {
		area = 0
	}
	return math.Max(0, area), nil
}

func sphericalRingArea(ring []float64) float64 {
	var sum float64
	for i := 0; i+3 < len(ring); i += 2 {
		lon1 := ring[i] * math.Pi / 180
		lat1 := ring[i+1] * math.Pi / 180
		lon2 := ring[i+2] * math.Pi / 180
		lat2 := ring[i+3] * math.Pi / 180
		dlon := lon2 - lon1
		if dlon > math.Pi {
			dlon -= 2 * math.Pi
		} else if dlon < -math.Pi {
			dlon += 2 * math.Pi
		}
		sum += dlon * (2 + math.Sin(lat1) + math.Sin(lat2))
	}
	return math.Abs(sum) * EarthRadiusM * EarthRadiusM / 2
}

// GeometriesIntersect reports whether two supported geometries share any point.
func GeometriesIntersect(a, b Value) (bool, error) {
	d, err := DistanceAny(a, b)
	if err != nil {
		return false, err
	}
	return d == 0, nil
}

// GeoEnvelope returns the smallest axis-aligned BOX represented by GeoBBox.
func GeoEnvelope(v Value) (Value, error) {
	w, s, e, n, _, ok := GeoBBox(v)
	if !ok {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.GeoEnvelope", "expected geometry")
	}
	return BoxValue(w, s, e, n)
}

// GeoCentroid returns a deterministic lon/lat centroid. Polygon centroids use
// the planar lon/lat ring centroid with holes subtracted; line centroids are
// segment-length weighted. These types reject antimeridian-crossing lines and
// polygons, while wrapping boxes use their wrapped longitudinal midpoint.
func GeoCentroid(v Value) (Value, error) {
	switch {
	case v.IsPoint():
		return v, nil
	case v.IsBox():
		w, s, e, n := v.Box[0], v.Box[1], v.Box[2], v.Box[3]
		lon := (w + e) / 2
		if w > e {
			lon = w + (e+360-w)/2
			if lon > 180 {
				lon -= 360
			}
		}
		return PointValue(lon, (s+n)/2)
	case v.IsLine():
		var lon, lat, total float64
		for i := 0; i+3 < len(v.Coords); i += 2 {
			weight := haversine(v.Coords[i], v.Coords[i+1], v.Coords[i+2], v.Coords[i+3])
			lon += (v.Coords[i] + v.Coords[i+2]) / 2 * weight
			lat += (v.Coords[i+1] + v.Coords[i+3]) / 2 * weight
			total += weight
		}
		if total == 0 {
			return PointValue(v.Coords[0], v.Coords[1])
		}
		return PointValue(lon/total, lat/total)
	case v.IsPolygon():
		var lon, lat, total float64
		off := 0
		for ri, n := range v.Rings {
			end := off + n*2
			if end > len(v.Coords) {
				return Value{}, nerr.New(nerr.InvalidArgument, "types.GeoCentroid", "truncated ring")
			}
			x, y, area := planarRingCentroid(v.Coords[off:end])
			if ri > 0 {
				area = -area
			}
			lon += x * area
			lat += y * area
			total += area
			off = end
		}
		if total != 0 {
			return PointValue(lon/total, lat/total)
		}
		env, err := GeoEnvelope(v)
		if err != nil {
			return Value{}, err
		}
		return GeoCentroid(env)
	default:
		return Value{}, nerr.New(nerr.InvalidArgument, "types.GeoCentroid", "expected geometry")
	}
}

func planarRingCentroid(ring []float64) (lon, lat, area float64) {
	var crossSum, xsum, ysum float64
	for i := 0; i+3 < len(ring); i += 2 {
		cross := ring[i]*ring[i+3] - ring[i+2]*ring[i+1]
		crossSum += cross
		xsum += (ring[i] + ring[i+2]) * cross
		ysum += (ring[i+1] + ring[i+3]) * cross
	}
	if crossSum == 0 {
		return ring[0], ring[1], 0
	}
	lon = xsum / (3 * crossSum)
	lat = ysum / (3 * crossSum)
	return lon, lat, math.Abs(crossSum / 2)
}

func GeometryTypeName(v Value) (string, error) {
	switch {
	case v.IsPoint():
		return "POINT", nil
	case v.IsBox():
		return "BOX", nil
	case v.IsLine():
		return "LINESTRING", nil
	case v.IsPolygon():
		return "POLYGON", nil
	default:
		return "", nerr.New(nerr.InvalidArgument, "types.GeometryTypeName", "expected geometry")
	}
}

func GeometryPointCount(v Value) (int, error) {
	switch {
	case v.IsPoint():
		return 1, nil
	case v.IsBox():
		return 4, nil
	case v.IsLine(), v.IsPolygon():
		return len(v.Coords) / 2, nil
	default:
		return 0, nerr.New(nerr.InvalidArgument, "types.GeometryPointCount", "expected geometry")
	}
}

// GeoBBox is the axis-aligned lon/lat box of a geometry. wrap is true when the box crosses the antimeridian.
func GeoBBox(v Value) (west, south, east, north float64, wrap bool, ok bool) {
	switch {
	case v.IsPoint():
		return v.Lon, v.Lat, v.Lon, v.Lat, false, true
	case v.IsBox():
		return v.Box[0], v.Box[1], v.Box[2], v.Box[3], v.Box[0] > v.Box[2], true
	case v.IsLine() || v.IsPolygon():
		if len(v.Coords) < 2 {
			return 0, 0, 0, 0, false, false
		}
		west, east = v.Coords[0], v.Coords[0]
		south, north = v.Coords[1], v.Coords[1]
		for i := 0; i < len(v.Coords); i += 2 {
			if v.Coords[i] < west {
				west = v.Coords[i]
			}
			if v.Coords[i] > east {
				east = v.Coords[i]
			}
			if v.Coords[i+1] < south {
				south = v.Coords[i+1]
			}
			if v.Coords[i+1] > north {
				north = v.Coords[i+1]
			}
		}
		return west, south, east, north, false, true
	default:
		return 0, 0, 0, 0, false, false
	}
}

// CircleBBox is an axis-aligned lon/lat box that contains the circle of radius meters.
func CircleBBox(lon, lat, meters float64) (west, south, east, north float64, world bool) {
	if meters < 0 {
		meters = 0
	}
	if meters >= math.Pi*EarthRadiusM {
		return -180, -90, 180, 90, true
	}
	dlat := (meters / EarthRadiusM) * 180 / math.Pi
	south = lat - dlat
	north = lat + dlat
	if south < -90 {
		south = -90
	}
	if north > 90 {
		north = 90
	}
	cos := math.Cos(lat * math.Pi / 180)
	if cos < 1e-6 {
		return -180, south, 180, north, true
	}
	dlon := (meters / EarthRadiusM) * 180 / math.Pi / cos
	if dlon >= 180 {
		return -180, south, 180, north, true
	}
	west = lon - dlon
	east = lon + dlon
	if west < -180 {
		west += 360
	}
	if east > 180 {
		east -= 360
	}
	return west, south, east, north, false
}

// ExpandBBox grows a non-wrapping box by a meter radius. world is true if the result covers the globe.
func ExpandBBox(west, south, east, north, meters float64) (w, s, e, n float64, world bool) {
	if meters < 0 {
		meters = 0
	}
	if west > east {
		return -180, -90, 180, 90, true
	}
	w1, s1, e1, n1, world1 := CircleBBox(west, south, meters)
	w2, s2, e2, n2, world2 := CircleBBox(east, north, meters)
	if world1 || world2 || w1 > e1 || w2 > e2 {
		return -180, -90, 180, 90, true
	}
	w = w1
	if w2 < w {
		w = w2
	}
	e = e1
	if e2 > e {
		e = e2
	}
	s = s1
	if s2 < s {
		s = s2
	}
	n = n1
	if n2 > n {
		n = n2
	}
	if s < -90 {
		s = -90
	}
	if n > 90 {
		n = 90
	}
	return w, s, e, n, false
}

// GeoHash64 is a 64-bit Morton code of quantized lon/lat (Z-order).
func GeoHash64(lon, lat float64) uint64 {
	return interleave32(quant32(lon, -180, 180), quant32(lat, -90, 90))
}

func quant32(v, min, max float64) uint32 {
	if v <= min {
		return 0
	}
	if v >= max {
		return math.MaxUint32
	}
	t := (v - min) / (max - min)
	x := math.Floor(t * float64(math.MaxUint32))
	if x < 0 {
		return 0
	}
	if x > float64(math.MaxUint32) {
		return math.MaxUint32
	}
	return uint32(x)
}

func interleave32(x, y uint32) uint64 {
	return spread32(x) | spread32(y)<<1
}

func spread32(x uint32) uint64 {
	v := uint64(x)
	v = (v | v<<16) & 0x0000FFFF0000FFFF
	v = (v | v<<8) & 0x00FF00FF00FF00FF
	v = (v | v<<4) & 0x0F0F0F0F0F0F0F0F
	v = (v | v<<2) & 0x3333333333333333
	v = (v | v<<1) & 0x5555555555555555
	return v
}

// GeoHashRange is the inclusive-start exclusive-end 64-bit range covering a box.
// bits==0 means the whole world (scan the entire spatial index).
func GeoHashRange(west, south, east, north float64) (start, end uint64, nbits int) {
	if west > east {
		// wrap: cannot represent as one prefix; full scan
		return 0, 0, 0
	}
	corners := []uint64{
		GeoHash64(west, south),
		GeoHash64(west, north),
		GeoHash64(east, south),
		GeoHash64(east, north),
	}
	nbits = 64
	for i := 1; i < 4; i++ {
		xor := corners[0] ^ corners[i]
		n := bits.LeadingZeros64(xor)
		if n < nbits {
			nbits = n
		}
	}
	if nbits == 0 {
		return 0, 0, 0
	}
	shift := uint(64 - nbits)
	start = (corners[0] >> shift) << shift
	if nbits == 64 {
		if start == math.MaxUint64 {
			return start, 0, nbits
		}
		return start, start + 1, nbits
	}
	span := uint64(1) << shift
	if start > math.MaxUint64-span {
		return start, 0, nbits
	}
	return start, start + span, nbits
}

// EncodeGeoKey writes 0x01 || uint64be(hash) || EncodeKey(pk).
func EncodeGeoKey(hash uint64, pk []Value) ([]byte, error) {
	buf := make([]byte, 9)
	buf[0] = 1
	binary.BigEndian.PutUint64(buf[1:], hash)
	if len(pk) == 0 {
		return buf, nil
	}
	rest, err := EncodeKey(pk)
	if err != nil {
		return nil, err
	}
	return append(buf, rest...), nil
}

// GeoKeyBounds returns [start, end) keys for a hash range. A nil end is unbounded.
func GeoKeyBounds(start, end uint64, bits int) (lo, hi []byte) {
	if bits == 0 {
		return nil, nil
	}
	lo = make([]byte, 9)
	lo[0] = 1
	binary.BigEndian.PutUint64(lo[1:], start)
	if end == 0 && start != 0 && bits < 64 {
		return lo, nil
	}
	if end == 0 && bits == 64 && start == math.MaxUint64 {
		return lo, nil
	}
	if end == 0 && bits == 0 {
		return nil, nil
	}
	if end == 0 {
		return lo, nil
	}
	hi = make([]byte, 9)
	hi[0] = 1
	binary.BigEndian.PutUint64(hi[1:], end)
	return lo, hi
}

func encodeGeo(v Value) ([]byte, error) {
	switch v.Typ.Kind {
	case KindLine:
		if err := validateLine(v.Coords); err != nil {
			return nil, err
		}
		n := len(v.Coords) / 2
		buf := make([]byte, 2+n*16)
		encoding.PutU16(buf, 0, uint16(n))
		putCoordsLE(buf[2:], v.Coords)
		return buf, nil
	case KindPolygon:
		if err := validatePolygon(v.Coords, v.Rings); err != nil {
			return nil, err
		}
		n := 2
		for _, r := range v.Rings {
			n += 2 + r*16
		}
		buf := make([]byte, n)
		encoding.PutU16(buf, 0, uint16(len(v.Rings)))
		off := 2
		co := 0
		for _, r := range v.Rings {
			encoding.PutU16(buf, off, uint16(r))
			off += 2
			putCoordsLE(buf[off:], v.Coords[co:co+r*2])
			off += r * 16
			co += r * 2
		}
		return buf, nil
	default:
		return nil, nerr.New(nerr.InvalidArgument, "types.encodeGeo", "expected LINESTRING or POLYGON")
	}
}

func decodeGeo(raw []byte, off int, t Type) (Value, int, error) {
	switch t.Kind {
	case KindLine:
		n, err := encoding.ReadU16(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		need := int(n) * 16
		b, err := encoding.ReadBytes(raw, off+2, need)
		if err != nil {
			return Value{}, 0, err
		}
		coords := coordsLE(b)
		v, err := LineValue(coords)
		if err != nil {
			return Value{}, 0, err
		}
		return v, off + 2 + need, nil
	case KindPolygon:
		nr, err := encoding.ReadU16(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		pos := off + 2
		rings := make([]int, 0, nr)
		var coords []float64
		for i := 0; i < int(nr); i++ {
			np, err := encoding.ReadU16(raw, pos)
			if err != nil {
				return Value{}, 0, err
			}
			pos += 2
			need := int(np) * 16
			b, err := encoding.ReadBytes(raw, pos, need)
			if err != nil {
				return Value{}, 0, err
			}
			coords = append(coords, coordsLE(b)...)
			rings = append(rings, int(np))
			pos += need
		}
		v, err := PolygonValue(coords, rings)
		if err != nil {
			return Value{}, 0, err
		}
		return v, pos, nil
	default:
		return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeGeo", "expected LINESTRING or POLYGON")
	}
}

func encodeSortableGeo(v Value) ([]byte, error) {
	switch v.Typ.Kind {
	case KindLine:
		if err := validateLine(v.Coords); err != nil {
			return nil, err
		}
		n := len(v.Coords) / 2
		out := make([]byte, 2, 2+n*16)
		encoding.PutU16(out, 0, uint16(n))
		for i := 0; i < len(v.Coords); i++ {
			out = append(out, encodeSortableF64(v.Coords[i])...)
		}
		return out, nil
	case KindPolygon:
		if err := validatePolygon(v.Coords, v.Rings); err != nil {
			return nil, err
		}
		out := make([]byte, 2)
		encoding.PutU16(out, 0, uint16(len(v.Rings)))
		co := 0
		for _, r := range v.Rings {
			var hdr [2]byte
			encoding.PutU16(hdr[:], 0, uint16(r))
			out = append(out, hdr[:]...)
			for i := 0; i < r*2; i++ {
				out = append(out, encodeSortableF64(v.Coords[co+i])...)
			}
			co += r * 2
		}
		return out, nil
	default:
		return nil, nerr.New(nerr.InvalidArgument, "types.encodeSortableGeo", "expected LINESTRING or POLYGON")
	}
}

func decodeSortableGeo(raw []byte, off int, t Type) (Value, int, error) {
	switch t.Kind {
	case KindLine:
		n, err := encoding.ReadU16(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		pos := off + 2
		coords := make([]float64, int(n)*2)
		for i := range coords {
			coords[i], pos, err = decodeSortableF64(raw, pos)
			if err != nil {
				return Value{}, 0, err
			}
		}
		v, err := LineValue(coords)
		if err != nil {
			return Value{}, 0, err
		}
		return v, pos, nil
	case KindPolygon:
		nr, err := encoding.ReadU16(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		pos := off + 2
		rings := make([]int, 0, nr)
		var coords []float64
		for i := 0; i < int(nr); i++ {
			np, err := encoding.ReadU16(raw, pos)
			if err != nil {
				return Value{}, 0, err
			}
			pos += 2
			for k := 0; k < int(np)*2; k++ {
				var f float64
				f, pos, err = decodeSortableF64(raw, pos)
				if err != nil {
					return Value{}, 0, err
				}
				coords = append(coords, f)
			}
			rings = append(rings, int(np))
		}
		v, err := PolygonValue(coords, rings)
		if err != nil {
			return Value{}, 0, err
		}
		return v, pos, nil
	default:
		return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeSortableGeo", "expected LINESTRING or POLYGON")
	}
}

func putCoordsLE(dst []byte, coords []float64) {
	for i, f := range coords {
		binary.LittleEndian.PutUint64(dst[i*8:], math.Float64bits(f))
	}
}

func coordsLE(b []byte) []float64 {
	n := len(b) / 8
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = math.Float64frombits(binary.LittleEndian.Uint64(b[i*8 : i*8+8]))
	}
	return out
}

func metersDecimal(m float64) (Value, error) {
	s := strconv.FormatFloat(m, 'f', 3, 64)
	d, err := ParseDecimal(s)
	if err != nil {
		return Value{}, err
	}
	return DecimalValue(d, Type{Kind: KindDecimal, Scale: 3}), nil
}

func asFloat(v Value) (float64, error) {
	if v.Null {
		return 0, nerr.New(nerr.InvalidArgument, "types.asFloat", "NULL coordinate")
	}
	switch v.Typ.Kind {
	case KindDecimal:
		f, err := strconv.ParseFloat(v.Dec.String(), 64)
		if err != nil {
			return 0, nerr.New(nerr.InvalidArgument, "types.asFloat", "invalid number")
		}
		return f, nil
	case KindString, KindText:
		f, err := strconv.ParseFloat(strings.TrimSpace(v.Str), 64)
		if err != nil {
			return 0, nerr.New(nerr.InvalidArgument, "types.asFloat", "invalid number")
		}
		return f, nil
	default:
		return 0, nerr.New(nerr.InvalidArgument, "types.asFloat", "expected a number")
	}
}

func asPoint(v Value) (Value, error) {
	if v.Null {
		return Null(Point()), nil
	}
	if v.Typ.Kind == KindPoint {
		return v, nil
	}
	if v.Typ.Kind == KindString || v.Typ.Kind == KindText {
		g, err := ParseWKT(v.Str)
		if err != nil {
			return Value{}, err
		}
		if !g.IsPoint() {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.asPoint", "expected POINT")
		}
		return g, nil
	}
	return Value{}, nerr.New(nerr.InvalidArgument, "types.asPoint", "expected POINT")
}

func asBox(v Value) (Value, error) {
	if v.Null {
		return Null(Box()), nil
	}
	if v.Typ.Kind == KindBox {
		return v, nil
	}
	if v.Typ.Kind == KindString || v.Typ.Kind == KindText {
		g, err := ParseWKT(v.Str)
		if err != nil {
			return Value{}, err
		}
		if g.Typ.Kind != KindBox {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.asBox", "expected BOX")
		}
		return g, nil
	}
	return Value{}, nerr.New(nerr.InvalidArgument, "types.asBox", "expected BOX")
}

func asLine(v Value) (Value, error) {
	if v.Null {
		return Null(Line()), nil
	}
	if v.Typ.Kind == KindLine {
		return v, nil
	}
	if v.Typ.Kind == KindString || v.Typ.Kind == KindText {
		return parseLineText(v.Str)
	}
	return Value{}, nerr.New(nerr.InvalidArgument, "types.asLine", "expected LINESTRING")
}

func asPolygon(v Value) (Value, error) {
	if v.Null {
		return Null(Polygon()), nil
	}
	if v.Typ.Kind == KindPolygon {
		return v, nil
	}
	if v.Typ.Kind == KindString || v.Typ.Kind == KindText {
		return parsePolygonText(v.Str)
	}
	return Value{}, nerr.New(nerr.InvalidArgument, "types.asPolygon", "expected POLYGON")
}

func asRegion(v Value) (Value, error) {
	if v.Null {
		return v, nil
	}
	switch v.Typ.Kind {
	case KindBox, KindPolygon:
		return v, nil
	case KindString, KindText:
		g, err := ParseWKT(v.Str)
		if err != nil {
			return Value{}, err
		}
		if g.Typ.Kind != KindBox && g.Typ.Kind != KindPolygon {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.asRegion", "expected BOX or POLYGON")
		}
		return g, nil
	default:
		return Value{}, nerr.New(nerr.InvalidArgument, "types.asRegion", "expected BOX or POLYGON")
	}
}

func asDistanceGeom(v Value) (Value, error) {
	if v.Null {
		return v, nil
	}
	switch v.Typ.Kind {
	case KindPoint, KindBox, KindLine, KindPolygon:
		return v, nil
	case KindString, KindText:
		g, err := ParseWKT(v.Str)
		if err != nil {
			return Value{}, err
		}
		switch g.Typ.Kind {
		case KindPoint, KindBox, KindLine, KindPolygon:
			return g, nil
		default:
			return Value{}, nerr.New(nerr.InvalidArgument, "types.asDistanceGeom", "expected POINT, BOX, LINESTRING, or POLYGON")
		}
	default:
		return Value{}, nerr.New(nerr.InvalidArgument, "types.asDistanceGeom", "expected POINT, BOX, LINESTRING, or POLYGON")
	}
}

func asGeometry(v Value) (Value, error) {
	if v.Null {
		return v, nil
	}
	switch v.Typ.Kind {
	case KindPoint, KindBox, KindLine, KindPolygon:
		return v, nil
	case KindString, KindText:
		return ParseWKT(v.Str)
	default:
		return Value{}, nerr.New(nerr.InvalidArgument, "types.asGeometry", "expected geometry")
	}
}

func parseLineText(s string) (Value, error) {
	s = strings.TrimSpace(s)
	up := strings.ToUpper(s)
	if strings.HasPrefix(up, "LINESTRING") {
		return ParseWKT(s)
	}
	return ParseWKT("LINESTRING" + s)
}

func parsePolygonText(s string) (Value, error) {
	s = strings.TrimSpace(s)
	up := strings.ToUpper(s)
	if strings.HasPrefix(up, "POLYGON") {
		return ParseWKT(s)
	}
	return ParseWKT("POLYGON" + s)
}

// ParseWKT reads POINT(lon lat), BOX(w s, e n), LINESTRING(...), or POLYGON((...)).
func ParseWKT(s string) (Value, error) {
	s = strings.TrimSpace(s)
	up := strings.ToUpper(s)
	switch {
	case strings.HasPrefix(up, "POINT"):
		body, err := wktBody(s, 5)
		if err != nil {
			return Value{}, err
		}
		nums, err := wktNums(body)
		if err != nil || len(nums) != 2 {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseWKT", "POINT requires lon lat")
		}
		return PointValue(nums[0], nums[1])
	case strings.HasPrefix(up, "BOX"):
		body, err := wktBody(s, 3)
		if err != nil {
			return Value{}, err
		}
		nums, err := wktNums(body)
		if err != nil || len(nums) != 4 {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseWKT", "BOX requires west south east north")
		}
		return BoxValue(nums[0], nums[1], nums[2], nums[3])
	case strings.HasPrefix(up, "LINESTRING"):
		body, err := wktBody(s, 10)
		if err != nil {
			return Value{}, err
		}
		nums, err := wktNums(body)
		if err != nil || len(nums)%2 != 0 {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseWKT", "LINESTRING requires lon lat pairs")
		}
		return LineValue(nums)
	case strings.HasPrefix(up, "POLYGON"):
		return parseWKTPolygon(s)
	default:
		return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseWKT", "expected POINT, BOX, LINESTRING, or POLYGON")
	}
}

func parseWKTPolygon(s string) (Value, error) {
	rest := strings.TrimSpace(s[7:])
	if len(rest) < 2 || rest[0] != '(' || rest[len(rest)-1] != ')' {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseWKT", "expected (...)")
	}
	inner := rest[1 : len(rest)-1]
	var coords []float64
	var rings []int
	i := 0
	for i < len(inner) {
		for i < len(inner) && (inner[i] == ' ' || inner[i] == ',' || inner[i] == '\t' || inner[i] == '\n' || inner[i] == '\r') {
			i++
		}
		if i >= len(inner) {
			break
		}
		if inner[i] != '(' {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseWKT", "POLYGON ring expected")
		}
		j := i + 1
		depth := 1
		for j < len(inner) && depth > 0 {
			switch inner[j] {
			case '(':
				depth++
			case ')':
				depth--
			}
			j++
		}
		if depth != 0 {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseWKT", "unbalanced POLYGON ring")
		}
		nums, err := wktNums(inner[i+1 : j-1])
		if err != nil || len(nums)%2 != 0 {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseWKT", "POLYGON ring requires lon lat pairs")
		}
		coords = append(coords, nums...)
		rings = append(rings, len(nums)/2)
		i = j
	}
	return PolygonValue(coords, rings)
}

func wktBody(s string, prefix int) (string, error) {
	s = strings.TrimSpace(s[prefix:])
	if len(s) < 2 || s[0] != '(' || s[len(s)-1] != ')' {
		return "", nerr.New(nerr.InvalidArgument, "types.ParseWKT", "expected (...)")
	}
	return s[1 : len(s)-1], nil
}

func wktNums(s string) ([]float64, error) {
	s = strings.Map(func(r rune) rune {
		if r == ',' {
			return ' '
		}
		return r
	}, s)
	parts := strings.Fields(s)
	out := make([]float64, 0, len(parts))
	for _, p := range parts {
		f, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return nil, nerr.New(nerr.InvalidArgument, "types.ParseWKT", "invalid number")
		}
		out = append(out, f)
	}
	return out, nil
}

func CanonGeoName(name string) string {
	switch name {
	case "st_makepoint", "makepoint", "location":
		return "point"
	case "st_makebox", "makebox", "st_makeenvelope":
		return "box"
	case "st_makeline", "makeline":
		return "linestring"
	case "st_makepolygon", "makepolygon":
		return "polygon"
	case "longitude", "st_x":
		return "lon"
	case "latitude", "st_y":
		return "lat"
	case "st_distance":
		return "distance"
	case "st_distancespheroid", "st_distance_spheroid", "distancespheroid":
		return "distance_spheroid"
	case "st_dwithin":
		return "dwithin"
	case "st_within":
		return "within"
	case "st_covers", "covers":
		return "covers"
	case "st_length", "linelength":
		return "linelength"
	case "st_area":
		return "area"
	case "st_perimeter":
		return "perimeter"
	case "st_centroid":
		return "centroid"
	case "st_envelope", "bbox":
		return "envelope"
	case "st_intersects":
		return "intersects"
	case "st_disjoint":
		return "disjoint"
	case "st_geometrytype", "geometry_type":
		return "geometrytype"
	case "st_npoints":
		return "npoints"
	case "st_nrings":
		return "nrings"
	default:
		return name
	}
}

// EvalGeo evaluates a geospatial function. ok is false when name is not geo.
func EvalGeo(name string, args []Value) (v Value, ok bool, err error) {
	switch CanonGeoName(name) {
	case "point":
		if len(args) != 2 {
			return Value{}, true, nerr.New(nerr.InvalidArgument, "types.EvalGeo", "POINT(lon, lat) takes 2 arguments")
		}
		lon, err := asFloat(args[0])
		if err != nil {
			return Value{}, true, err
		}
		lat, err := asFloat(args[1])
		if err != nil {
			return Value{}, true, err
		}
		p, err := PointValue(lon, lat)
		return p, true, err
	case "box":
		if len(args) != 4 {
			return Value{}, true, nerr.New(nerr.InvalidArgument, "types.EvalGeo", "BOX(west, south, east, north) takes 4 arguments")
		}
		var nums [4]float64
		for i := 0; i < 4; i++ {
			nums[i], err = asFloat(args[i])
			if err != nil {
				return Value{}, true, err
			}
		}
		b, err := BoxValue(nums[0], nums[1], nums[2], nums[3])
		return b, true, err
	case "linestring":
		if len(args) != 1 {
			return Value{}, true, nerr.New(nerr.InvalidArgument, "types.EvalGeo", "LINESTRING takes 1 WKT argument")
		}
		ln, err := asLine(args[0])
		return ln, true, err
	case "polygon":
		if len(args) != 1 {
			return Value{}, true, nerr.New(nerr.InvalidArgument, "types.EvalGeo", "POLYGON takes 1 WKT argument")
		}
		poly, err := asPolygon(args[0])
		return poly, true, err
	case "lon":
		if len(args) != 1 {
			return Value{}, true, nerr.New(nerr.InvalidArgument, "types.EvalGeo", "LON takes 1 argument")
		}
		p, err := asPoint(args[0])
		if err != nil {
			return Value{}, true, err
		}
		if p.Null {
			return Null(Type{Kind: KindDecimal}), true, nil
		}
		d, err := metersDecimal(p.Lon)
		return d, true, err
	case "lat":
		if len(args) != 1 {
			return Value{}, true, nerr.New(nerr.InvalidArgument, "types.EvalGeo", "LAT takes 1 argument")
		}
		p, err := asPoint(args[0])
		if err != nil {
			return Value{}, true, err
		}
		if p.Null {
			return Null(Type{Kind: KindDecimal}), true, nil
		}
		d, err := metersDecimal(p.Lat)
		return d, true, err
	case "distance":
		if len(args) != 2 {
			return Value{}, true, nerr.New(nerr.InvalidArgument, "types.EvalGeo", "DISTANCE takes 2 arguments")
		}
		a, err := asDistanceGeom(args[0])
		if err != nil {
			return Value{}, true, err
		}
		b, err := asDistanceGeom(args[1])
		if err != nil {
			return Value{}, true, err
		}
		if a.Null || b.Null {
			return Null(Type{Kind: KindDecimal, Scale: 3}), true, nil
		}
		m, err := DistanceAny(a, b)
		if err != nil {
			return Value{}, true, err
		}
		d, err := metersDecimal(m)
		return d, true, err
	case "distance_spheroid":
		if len(args) != 2 {
			return Value{}, true, nerr.New(nerr.InvalidArgument, "types.EvalGeo", "DISTANCE_SPHEROID takes 2 arguments")
		}
		a, err := asPoint(args[0])
		if err != nil {
			return Value{}, true, err
		}
		b, err := asPoint(args[1])
		if err != nil {
			return Value{}, true, err
		}
		if a.Null || b.Null {
			return Null(Type{Kind: KindDecimal, Scale: 3}), true, nil
		}
		m, err := DistanceSpheroidM(a, b)
		if err != nil {
			return Value{}, true, err
		}
		d, err := metersDecimal(m)
		return d, true, err
	case "linelength":
		if len(args) != 1 {
			return Value{}, true, nerr.New(nerr.InvalidArgument, "types.EvalGeo", "LINELENGTH takes 1 argument")
		}
		ln, err := asLine(args[0])
		if err != nil {
			return Value{}, true, err
		}
		if ln.Null {
			return Null(Type{Kind: KindDecimal, Scale: 3}), true, nil
		}
		m, err := LineLengthM(ln)
		if err != nil {
			return Value{}, true, err
		}
		d, err := metersDecimal(m)
		return d, true, err
	case "area":
		if len(args) != 1 {
			return Value{}, true, nerr.New(nerr.InvalidArgument, "types.EvalGeo", "AREA takes 1 argument")
		}
		poly, err := asPolygon(args[0])
		if err != nil {
			return Value{}, true, err
		}
		if poly.Null {
			return Null(Type{Kind: KindDecimal, Scale: 3}), true, nil
		}
		m2, err := PolygonAreaM2(poly)
		if err != nil {
			return Value{}, true, err
		}
		d, err := metersDecimal(m2)
		return d, true, err
	case "perimeter":
		if len(args) != 1 {
			return Value{}, true, nerr.New(nerr.InvalidArgument, "types.EvalGeo", "PERIMETER takes 1 argument")
		}
		poly, err := asPolygon(args[0])
		if err != nil {
			return Value{}, true, err
		}
		if poly.Null {
			return Null(Type{Kind: KindDecimal, Scale: 3}), true, nil
		}
		m, err := PolygonPerimeterM(poly)
		if err != nil {
			return Value{}, true, err
		}
		d, err := metersDecimal(m)
		return d, true, err
	case "centroid", "envelope", "geometrytype", "npoints", "nrings":
		if len(args) != 1 {
			return Value{}, true, nerr.New(nerr.InvalidArgument, "types.EvalGeo", strings.ToUpper(CanonGeoName(name))+" takes 1 argument")
		}
		geom, err := asGeometry(args[0])
		if err != nil {
			return Value{}, true, err
		}
		if geom.Null {
			switch CanonGeoName(name) {
			case "centroid":
				return Null(Point()), true, nil
			case "envelope":
				return Null(Box()), true, nil
			case "geometrytype":
				return Null(String()), true, nil
			default:
				return Null(Type{Kind: KindDecimal}), true, nil
			}
		}
		switch CanonGeoName(name) {
		case "centroid":
			out, err := GeoCentroid(geom)
			return out, true, err
		case "envelope":
			out, err := GeoEnvelope(geom)
			return out, true, err
		case "geometrytype":
			typ, err := GeometryTypeName(geom)
			return StringValue(typ), true, err
		case "npoints":
			n, err := GeometryPointCount(geom)
			if err != nil {
				return Value{}, true, err
			}
			return DecimalValue(DecimalFromInt64(int64(n)), Type{Kind: KindDecimal}), true, nil
		case "nrings":
			if !geom.IsPolygon() {
				return Value{}, true, nerr.New(nerr.InvalidArgument, "types.EvalGeo", "NRINGS requires POLYGON")
			}
			return DecimalValue(DecimalFromInt64(int64(len(geom.Rings))), Type{Kind: KindDecimal}), true, nil
		}
		return Value{}, true, nerr.New(nerr.Internal, "types.EvalGeo", "unhandled geometry function")
	case "intersects", "disjoint":
		if len(args) != 2 {
			return Value{}, true, nerr.New(nerr.InvalidArgument, "types.EvalGeo", strings.ToUpper(CanonGeoName(name))+" takes 2 arguments")
		}
		a, err := asDistanceGeom(args[0])
		if err != nil {
			return Value{}, true, err
		}
		b, err := asDistanceGeom(args[1])
		if err != nil {
			return Value{}, true, err
		}
		if a.Null || b.Null {
			return Null(Bool()), true, nil
		}
		intersects, err := GeometriesIntersect(a, b)
		if err != nil {
			return Value{}, true, err
		}
		if CanonGeoName(name) == "disjoint" {
			intersects = !intersects
		}
		return BoolValue(intersects), true, nil
	case "dwithin":
		if len(args) != 3 {
			return Value{}, true, nerr.New(nerr.InvalidArgument, "types.EvalGeo", "DWITHIN takes 3 arguments")
		}
		a, err := asDistanceGeom(args[0])
		if err != nil {
			return Value{}, true, err
		}
		b, err := asDistanceGeom(args[1])
		if err != nil {
			return Value{}, true, err
		}
		r, err := asFloat(args[2])
		if err != nil {
			return Value{}, true, err
		}
		if r < 0 {
			return Value{}, true, nerr.New(nerr.InvalidArgument, "types.EvalGeo", "DWITHIN radius must be >= 0")
		}
		if a.Null || b.Null {
			return Null(Bool()), true, nil
		}
		m, err := DistanceAny(a, b)
		if err != nil {
			return Value{}, true, err
		}
		return BoolValue(m <= r), true, nil
	case "within":
		if len(args) != 2 {
			return Value{}, true, nerr.New(nerr.InvalidArgument, "types.EvalGeo", "WITHIN takes 2 arguments")
		}
		p, err := asPoint(args[0])
		if err != nil {
			return Value{}, true, err
		}
		reg, err := asRegion(args[1])
		if err != nil {
			return Value{}, true, err
		}
		if p.Null || reg.Null {
			return Null(Bool()), true, nil
		}
		in, err := PointWithinGeom(p, reg)
		return BoolValue(in), true, err
	case "covers":
		if len(args) != 2 {
			return Value{}, true, nerr.New(nerr.InvalidArgument, "types.EvalGeo", "COVERS takes 2 arguments")
		}
		reg, err := asRegion(args[0])
		if err != nil {
			return Value{}, true, err
		}
		p, err := asPoint(args[1])
		if err != nil {
			return Value{}, true, err
		}
		if p.Null || reg.Null {
			return Null(Bool()), true, nil
		}
		in, err := PointInGeom(p, reg)
		return BoolValue(in), true, err
	default:
		return Value{}, false, nil
	}
}

func IsGeoFunc(name string) bool {
	switch CanonGeoName(name) {
	case "point", "box", "linestring", "polygon", "lon", "lat", "distance", "distance_spheroid", "dwithin", "within", "covers", "linelength", "area", "perimeter", "centroid", "envelope", "intersects", "disjoint", "geometrytype", "npoints", "nrings":
		return true
	default:
		return false
	}
}
