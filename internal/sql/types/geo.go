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
		off += n * 2
	}
	return nil
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

// PointInPolygon is exterior-minus-holes ray casting. Rings must not cross the antimeridian.
func PointInPolygon(p, poly Value) (bool, error) {
	if !p.IsPoint() {
		return false, nerr.New(nerr.InvalidArgument, "types.PointInPolygon", "expected POINT")
	}
	if !poly.IsPolygon() {
		return false, nerr.New(nerr.InvalidArgument, "types.PointInPolygon", "expected POLYGON")
	}
	off := 0
	in := false
	for i, n := range poly.Rings {
		end := off + n*2
		if end > len(poly.Coords) {
			return false, nerr.New(nerr.InvalidArgument, "types.PointInPolygon", "truncated ring")
		}
		hit := pointInRing(p.Lon, p.Lat, poly.Coords[off:end])
		if i == 0 {
			in = hit
		} else if hit {
			in = false
		}
		off = end
	}
	return in, nil
}

func pointInRing(lon, lat float64, coords []float64) bool {
	n := len(coords) / 2
	if n < 4 {
		return false
	}
	inside := false
	j := n - 1
	for i := 0; i < n; i++ {
		xi, yi := coords[i*2], coords[i*2+1]
		xj, yj := coords[j*2], coords[j*2+1]
		if (yi > lat) != (yj > lat) {
			xint := (xj-xi)*(lat-yi)/(yj-yi) + xi
			if lon < xint {
				inside = !inside
			}
		}
		j = i
	}
	return inside
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

// DistanceAny is meters between two geometries. Point-line / point-polygon
// use an equirectangular closest-point on each segment, then haversine.
func DistanceAny(a, b Value) (float64, error) {
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
	return 0, nerr.New(nerr.InvalidArgument, "types.DistanceAny", "unsupported geometry pair")
}

func pointToPolygon(p, poly Value) (float64, error) {
	in, err := PointInPolygon(p, poly)
	if err != nil {
		return 0, err
	}
	if in {
		return 0, nil
	}
	return pointToLine(p.Lon, p.Lat, poly.Coords), nil
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
		return ParseWKT(v.Str)
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
	case KindPoint, KindLine, KindPolygon:
		return v, nil
	case KindString, KindText:
		g, err := ParseWKT(v.Str)
		if err != nil {
			return Value{}, err
		}
		switch g.Typ.Kind {
		case KindPoint, KindLine, KindPolygon:
			return g, nil
		default:
			return Value{}, nerr.New(nerr.InvalidArgument, "types.asDistanceGeom", "expected POINT, LINESTRING, or POLYGON")
		}
	default:
		return Value{}, nerr.New(nerr.InvalidArgument, "types.asDistanceGeom", "expected POINT, LINESTRING, or POLYGON")
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
		m, err := DistanceM(a, b)
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
		in, err := PointInGeom(p, reg)
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
	case "point", "box", "linestring", "polygon", "lon", "lat", "distance", "distance_spheroid", "dwithin", "within", "covers", "linelength":
		return true
	default:
		return false
	}
}
