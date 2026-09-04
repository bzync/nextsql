package types

import (
	"encoding/binary"
	stdjson "encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
)

// Spatial track — general OGC GEOMETRY / GEOGRAPHY types
// (docs/design-spatial.md). A PostGIS-style subsystem alongside the four
// fixed WGS84 shapes in geo.go, NOT a generalization of them.

// OGC geometry type codes (also the values of the GeomSub* constants in
// types.go, so Type.Scale and Geom.Type use the same numbering).
const (
	ogcPoint              uint32 = 1
	ogcLineString         uint32 = 2
	ogcPolygon            uint32 = 3
	ogcMultiPoint         uint32 = 4
	ogcMultiLineString    uint32 = 5
	ogcMultiPolygon       uint32 = 6
	ogcGeometryCollection uint32 = 7
)

// ewkbSRIDFlag marks a following uint32 SRID in an EWKB type word.
const ewkbSRIDFlag uint32 = 0x20000000

// Spatial abuse limits (resource safety). Total vertices across every part
// of one value; nesting depth of GeometryCollection-in-GeometryCollection;
// the part count of any single Multi*/GeometryCollection.
const (
	MaxSpatialVertices = 65536
	MaxSpatialDepth    = 8
	MaxSpatialParts    = 4096
)

// Geom is a recursive general-geometry value. For KindGeometry / KindGeography
// values it lives on Value.Geom.
//
//	Point              Coords = [x, y]
//	LineString         Coords = flat x,y,x,y,…
//	Polygon            Coords = flat, Rings = vertex count per ring
//	MultiPoint         Parts  = Point sub-geometries
//	MultiLineString    Parts  = LineString sub-geometries
//	MultiPolygon       Parts  = Polygon sub-geometries
//	GeometryCollection Parts  = any sub-geometries
type Geom struct {
	Type   uint32
	SRID   uint32
	Coords []float64
	Rings  []int
	Parts  []*Geom
}

// GeomValue wraps a *Geom in a Value of the given Kind (KindGeometry or
// KindGeography). The Geom's SRID is normalized to t's declared SRID.
func GeomValue(g *Geom, t Type) Value {
	if g != nil {
		g.SRID = uint32(t.Precision)
	}
	return Value{Typ: t, Geom: g}
}

// Clone deep-copies a Geom tree.
func (g *Geom) Clone() *Geom {
	if g == nil {
		return nil
	}
	cp := &Geom{Type: g.Type, SRID: g.SRID}
	if g.Coords != nil {
		cp.Coords = append([]float64(nil), g.Coords...)
	}
	if g.Rings != nil {
		cp.Rings = append([]int(nil), g.Rings...)
	}
	for _, p := range g.Parts {
		cp.Parts = append(cp.Parts, p.Clone())
	}
	return cp
}

// vertexCount totals the coordinate pairs across a Geom tree.
func (g *Geom) vertexCount() int {
	if g == nil {
		return 0
	}
	n := len(g.Coords) / 2
	for _, p := range g.Parts {
		n += p.vertexCount()
	}
	return n
}

// depth is the GeometryCollection nesting depth (0 for a leaf/multi).
func (g *Geom) depth() int {
	if g == nil || g.Type != ogcGeometryCollection {
		return 0
	}
	max := 0
	for _, p := range g.Parts {
		if d := p.depth(); d > max {
			max = d
		}
	}
	return 1 + max
}

// finite reports whether every coordinate is finite.
func finiteCoords(c []float64) bool {
	for _, f := range c {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return false
		}
	}
	return true
}

// validateGeom checks structural well-formedness and resource bounds. It does
// not check ring closure or self-intersection (canonicalizeGeom closes rings;
// deep OGC validity is ST_IsValid's job, deferred).
func validateGeom(g *Geom, depth int) error {
	if g == nil {
		return nerr.New(nerr.InvalidArgument, "types.validateGeom", "nil geometry")
	}
	if depth > MaxSpatialDepth {
		return nerr.New(nerr.InvalidArgument, "types.validateGeom", "geometry nesting too deep")
	}
	if !finiteCoords(g.Coords) {
		return nerr.New(nerr.InvalidArgument, "types.validateGeom", "coordinate is not finite")
	}
	switch g.Type {
	case ogcPoint:
		if len(g.Coords) != 2 || len(g.Rings) != 0 || len(g.Parts) != 0 {
			return nerr.New(nerr.InvalidArgument, "types.validateGeom", "malformed Point")
		}
	case ogcLineString:
		if len(g.Coords) < 4 || len(g.Coords)%2 != 0 || len(g.Rings) != 0 || len(g.Parts) != 0 {
			return nerr.New(nerr.InvalidArgument, "types.validateGeom", "LineString needs >= 2 vertices")
		}
	case ogcPolygon:
		if len(g.Rings) == 0 || len(g.Parts) != 0 {
			return nerr.New(nerr.InvalidArgument, "types.validateGeom", "Polygon needs >= 1 ring")
		}
		sum := 0
		for _, r := range g.Rings {
			if r < 4 {
				return nerr.New(nerr.InvalidArgument, "types.validateGeom", "Polygon ring needs >= 4 vertices")
			}
			sum += r
		}
		if sum*2 != len(g.Coords) {
			return nerr.New(nerr.InvalidArgument, "types.validateGeom", "Polygon ring sizes do not match coordinates")
		}
	case ogcMultiPoint, ogcMultiLineString, ogcMultiPolygon, ogcGeometryCollection:
		if len(g.Coords) != 0 || len(g.Rings) != 0 {
			return nerr.New(nerr.InvalidArgument, "types.validateGeom", "multi geometry carries inline coordinates")
		}
		if len(g.Parts) > MaxSpatialParts {
			return nerr.New(nerr.InvalidArgument, "types.validateGeom", "too many geometry parts")
		}
		want := map[uint32]uint32{
			ogcMultiPoint:      ogcPoint,
			ogcMultiLineString: ogcLineString,
			ogcMultiPolygon:    ogcPolygon,
		}[g.Type]
		for _, p := range g.Parts {
			if want != 0 && p.Type != want {
				return nerr.New(nerr.InvalidArgument, "types.validateGeom", "multi geometry part has the wrong type")
			}
			if err := validateGeom(p, depth+1); err != nil {
				return err
			}
		}
	default:
		return nerr.New(nerr.InvalidArgument, "types.validateGeom", "unknown geometry type")
	}
	if g.vertexCount() > MaxSpatialVertices {
		return nerr.New(nerr.InvalidArgument, "types.validateGeom", "geometry exceeds vertex limit")
	}
	return nil
}

// canonicalizeGeom closes every polygon ring (first vertex == last) and
// normalizes ring orientation: exterior counter-clockwise, holes clockwise
// (positive/negative signed area). This makes the canonical EWKB byte order
// (used for index keys and equality) stable regardless of input winding.
func canonicalizeGeom(g *Geom) {
	if g == nil {
		return
	}
	switch g.Type {
	case ogcPolygon:
		off := 0
		for ri, n := range g.Rings {
			ring := g.Coords[off : off+n*2]
			// close
			if ring[0] != ring[len(ring)-2] || ring[1] != ring[len(ring)-1] {
				g.Coords = append(g.Coords[:off+n*2], append([]float64{ring[0], ring[1]}, g.Coords[off+n*2:]...)...)
				g.Rings[ri] = n + 1
				n++
				ring = g.Coords[off : off+n*2]
			}
			// Orient exterior ring CCW, holes CW. A degenerate (zero-area)
			// ring has no meaningful orientation — leave it untouched so
			// canonicalization stays idempotent.
			if area := ringSignedArea(ring); area != 0 && (ri == 0) != (area > 0) {
				reverseRing(ring)
			}
			off += n * 2
		}
	case ogcMultiPoint, ogcMultiLineString, ogcMultiPolygon, ogcGeometryCollection:
		for _, p := range g.Parts {
			p.SRID = g.SRID
			canonicalizeGeom(p)
		}
	}
}

func ringSignedArea(ring []float64) float64 {
	a := 0.0
	n := len(ring) / 2
	for i := 0; i < n-1; i++ {
		x1, y1 := ring[i*2], ring[i*2+1]
		x2, y2 := ring[(i+1)*2], ring[(i+1)*2+1]
		a += x1*y2 - x2*y1
	}
	return a / 2
}

func reverseRing(ring []float64) {
	n := len(ring) / 2
	for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
		ring[i*2], ring[j*2] = ring[j*2], ring[i*2]
		ring[i*2+1], ring[j*2+1] = ring[j*2+1], ring[i*2+1]
	}
}

// --- EWKB codec ------------------------------------------------------------

// EncodeEWKB writes the extended-WKB bytes for g (little-endian, SRID always
// present). Used by the heap-row codec (with a length prefix), ST_AsBinary,
// and the sortable key.
func EncodeEWKB(g *Geom) ([]byte, error) {
	if err := validateGeom(g, 0); err != nil {
		return nil, err
	}
	var b []byte
	b = appendEWKB(b, g, true)
	return b, nil
}

func appendEWKB(b []byte, g *Geom, top bool) []byte {
	b = append(b, 0x01) // little-endian
	t := g.Type
	if top {
		t |= ewkbSRIDFlag
	}
	var w [4]byte
	binary.LittleEndian.PutUint32(w[:], t)
	b = append(b, w[:]...)
	if top {
		binary.LittleEndian.PutUint32(w[:], g.SRID)
		b = append(b, w[:]...)
	}
	switch g.Type {
	case ogcPoint:
		b = appendF64(b, g.Coords[0])
		b = appendF64(b, g.Coords[1])
	case ogcLineString:
		binary.LittleEndian.PutUint32(w[:], uint32(len(g.Coords)/2))
		b = append(b, w[:]...)
		for _, f := range g.Coords {
			b = appendF64(b, f)
		}
	case ogcPolygon:
		binary.LittleEndian.PutUint32(w[:], uint32(len(g.Rings)))
		b = append(b, w[:]...)
		off := 0
		for _, n := range g.Rings {
			binary.LittleEndian.PutUint32(w[:], uint32(n))
			b = append(b, w[:]...)
			for _, f := range g.Coords[off : off+n*2] {
				b = appendF64(b, f)
			}
			off += n * 2
		}
	default: // Multi* / GeometryCollection
		binary.LittleEndian.PutUint32(w[:], uint32(len(g.Parts)))
		b = append(b, w[:]...)
		for _, p := range g.Parts {
			b = appendEWKB(b, p, false)
		}
	}
	return b
}

func appendF64(b []byte, f float64) []byte {
	var w [8]byte
	binary.LittleEndian.PutUint64(w[:], math.Float64bits(f))
	return append(b, w[:]...)
}

// DecodeEWKB parses extended-WKB, returning the geometry and the number of
// bytes consumed. Bounded: rejects over-deep nesting, over-large part /
// vertex counts, and truncation.
func DecodeEWKB(raw []byte) (*Geom, int, error) {
	g, n, err := decodeEWKBAt(raw, 0, 0)
	if err != nil {
		return nil, 0, err
	}
	if err := validateGeom(g, 0); err != nil {
		return nil, 0, err
	}
	return g, n, nil
}

func decodeEWKBAt(raw []byte, off, depth int) (*Geom, int, error) {
	if depth > MaxSpatialDepth {
		return nil, 0, nerr.New(nerr.InvalidFormat, "types.DecodeEWKB", "geometry nesting too deep")
	}
	if off+5 > len(raw) {
		return nil, 0, nerr.New(nerr.InvalidFormat, "types.DecodeEWKB", "truncated geometry header")
	}
	if raw[off] != 0x01 {
		return nil, 0, nerr.New(nerr.InvalidFormat, "types.DecodeEWKB", "only little-endian EWKB is supported")
	}
	tw := binary.LittleEndian.Uint32(raw[off+1:])
	p := off + 5
	g := &Geom{Type: tw &^ ewkbSRIDFlag}
	if tw&ewkbSRIDFlag != 0 {
		if p+4 > len(raw) {
			return nil, 0, nerr.New(nerr.InvalidFormat, "types.DecodeEWKB", "truncated SRID")
		}
		g.SRID = binary.LittleEndian.Uint32(raw[p:])
		p += 4
	}
	readU32 := func() (uint32, error) {
		if p+4 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.DecodeEWKB", "truncated count")
		}
		v := binary.LittleEndian.Uint32(raw[p:])
		p += 4
		return v, nil
	}
	readF64s := func(n int) ([]float64, error) {
		if n < 0 || p+n*8 > len(raw) {
			return nil, nerr.New(nerr.InvalidFormat, "types.DecodeEWKB", "truncated coordinates")
		}
		out := make([]float64, n)
		for i := 0; i < n; i++ {
			out[i] = math.Float64frombits(binary.LittleEndian.Uint64(raw[p:]))
			p += 8
		}
		return out, nil
	}
	switch g.Type {
	case ogcPoint:
		c, err := readF64s(2)
		if err != nil {
			return nil, 0, err
		}
		g.Coords = c
	case ogcLineString:
		n, err := readU32()
		if err != nil {
			return nil, 0, err
		}
		if n > MaxSpatialVertices {
			return nil, 0, nerr.New(nerr.InvalidFormat, "types.DecodeEWKB", "LineString too long")
		}
		c, err := readF64s(int(n) * 2)
		if err != nil {
			return nil, 0, err
		}
		g.Coords = c
	case ogcPolygon:
		nr, err := readU32()
		if err != nil {
			return nil, 0, err
		}
		if nr > MaxSpatialParts {
			return nil, 0, nerr.New(nerr.InvalidFormat, "types.DecodeEWKB", "too many polygon rings")
		}
		total := 0
		for i := uint32(0); i < nr; i++ {
			rn, err := readU32()
			if err != nil {
				return nil, 0, err
			}
			total += int(rn)
			if total > MaxSpatialVertices {
				return nil, 0, nerr.New(nerr.InvalidFormat, "types.DecodeEWKB", "polygon too large")
			}
			c, err := readF64s(int(rn) * 2)
			if err != nil {
				return nil, 0, err
			}
			g.Coords = append(g.Coords, c...)
			g.Rings = append(g.Rings, int(rn))
		}
	case ogcMultiPoint, ogcMultiLineString, ogcMultiPolygon, ogcGeometryCollection:
		np, err := readU32()
		if err != nil {
			return nil, 0, err
		}
		if np > MaxSpatialParts {
			return nil, 0, nerr.New(nerr.InvalidFormat, "types.DecodeEWKB", "too many geometry parts")
		}
		for i := uint32(0); i < np; i++ {
			sub, n, err := decodeEWKBAt(raw, p, depth+1)
			if err != nil {
				return nil, 0, err
			}
			p = n
			g.Parts = append(g.Parts, sub)
		}
	default:
		return nil, 0, nerr.New(nerr.InvalidFormat, "types.DecodeEWKB", "unknown geometry type")
	}
	return g, p, nil
}

// --- WKT / EWKT ----------------------------------------------------------

// ParseGeneralWKT parses WKT or EWKT ("SRID=4326;POINT(1 2)") into a Geom.
// The caller supplies the column SRID as a fallback when the text has none.
func ParseGeneralWKT(s string, fallbackSRID uint32) (*Geom, error) {
	s = strings.TrimSpace(s)
	srid := fallbackSRID
	if up := strings.ToUpper(s); strings.HasPrefix(up, "SRID=") {
		semi := strings.IndexByte(s, ';')
		if semi < 0 {
			return nil, nerr.New(nerr.InvalidArgument, "types.ParseGeneralWKT", "malformed EWKT SRID prefix")
		}
		// bitSize 16 matches sql/parser.geoTypeArgs's own SRID range check
		// (a column's SRID is a u16) so an out-of-range EWKT prefix fails
		// closed here instead of silently wrapping later when the parsed
		// value's SRID is narrowed into the destination column type.
		n, err := strconv.ParseUint(strings.TrimSpace(s[5:semi]), 10, 16)
		if err != nil {
			return nil, nerr.New(nerr.InvalidArgument, "types.ParseGeneralWKT", "SRID out of range")
		}
		srid = uint32(n)
		s = strings.TrimSpace(s[semi+1:])
	}
	g, rest, err := parseWKTGeom(s, 0)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(rest) != "" {
		return nil, nerr.New(nerr.InvalidArgument, "types.ParseGeneralWKT", "trailing text after geometry")
	}
	setSRID(g, srid)
	canonicalizeGeom(g)
	if err := validateGeom(g, 0); err != nil {
		return nil, err
	}
	return g, nil
}

func setSRID(g *Geom, srid uint32) {
	g.SRID = srid
	for _, p := range g.Parts {
		setSRID(p, srid)
	}
}

func parseWKTGeom(s string, depth int) (*Geom, string, error) {
	if depth > MaxSpatialDepth {
		return nil, "", nerr.New(nerr.InvalidArgument, "types.ParseGeneralWKT", "geometry nesting too deep")
	}
	s = strings.TrimSpace(s)
	up := strings.ToUpper(s)
	kw := func(name string) bool { return strings.HasPrefix(up, name) }
	switch {
	case kw("POINT"):
		body, rest, err := wktParen(s[5:])
		if err != nil {
			return nil, "", err
		}
		nums, err := wktNums(body)
		if err != nil || len(nums) != 2 {
			return nil, "", nerr.New(nerr.InvalidArgument, "types.ParseGeneralWKT", "POINT requires x y")
		}
		return &Geom{Type: ogcPoint, Coords: nums}, rest, nil
	case kw("LINESTRING"):
		body, rest, err := wktParen(s[10:])
		if err != nil {
			return nil, "", err
		}
		nums, err := wktNums(body)
		if err != nil || len(nums) < 4 || len(nums)%2 != 0 {
			return nil, "", nerr.New(nerr.InvalidArgument, "types.ParseGeneralWKT", "LINESTRING requires >= 2 x y pairs")
		}
		return &Geom{Type: ogcLineString, Coords: nums}, rest, nil
	case kw("POLYGON"):
		body, rest, err := wktParen(s[7:])
		if err != nil {
			return nil, "", err
		}
		g := &Geom{Type: ogcPolygon}
		for _, ringTxt := range splitWKTGroups(body) {
			nums, err := wktNums(ringTxt)
			if err != nil || len(nums) < 8 || len(nums)%2 != 0 {
				return nil, "", nerr.New(nerr.InvalidArgument, "types.ParseGeneralWKT", "POLYGON ring requires >= 4 x y pairs")
			}
			g.Coords = append(g.Coords, nums...)
			g.Rings = append(g.Rings, len(nums)/2)
		}
		if len(g.Rings) == 0 {
			return nil, "", nerr.New(nerr.InvalidArgument, "types.ParseGeneralWKT", "POLYGON requires an exterior ring")
		}
		return g, rest, nil
	case kw("MULTIPOINT"):
		body, rest, err := wktParen(s[10:])
		if err != nil {
			return nil, "", err
		}
		g := &Geom{Type: ogcMultiPoint}
		// MULTIPOINT accepts both "(1 2, 3 4)" and "((1 2), (3 4))".
		for _, grp := range splitWKTPointList(body) {
			nums, err := wktNums(grp)
			if err != nil || len(nums) != 2 {
				return nil, "", nerr.New(nerr.InvalidArgument, "types.ParseGeneralWKT", "MULTIPOINT part requires x y")
			}
			g.Parts = append(g.Parts, &Geom{Type: ogcPoint, Coords: nums})
		}
		return g, rest, nil
	case kw("MULTILINESTRING"):
		body, rest, err := wktParen(s[15:])
		if err != nil {
			return nil, "", err
		}
		g := &Geom{Type: ogcMultiLineString}
		for _, grp := range splitWKTGroups(body) {
			nums, err := wktNums(grp)
			if err != nil || len(nums) < 4 || len(nums)%2 != 0 {
				return nil, "", nerr.New(nerr.InvalidArgument, "types.ParseGeneralWKT", "MULTILINESTRING part invalid")
			}
			g.Parts = append(g.Parts, &Geom{Type: ogcLineString, Coords: nums})
		}
		return g, rest, nil
	case kw("MULTIPOLYGON"):
		body, rest, err := wktParen(s[12:])
		if err != nil {
			return nil, "", err
		}
		g := &Geom{Type: ogcMultiPolygon}
		for _, polyTxt := range splitWKTGroups(body) {
			poly := &Geom{Type: ogcPolygon}
			for _, ringTxt := range splitWKTGroups(polyTxt) {
				nums, err := wktNums(ringTxt)
				if err != nil || len(nums) < 8 || len(nums)%2 != 0 {
					return nil, "", nerr.New(nerr.InvalidArgument, "types.ParseGeneralWKT", "MULTIPOLYGON ring invalid")
				}
				poly.Coords = append(poly.Coords, nums...)
				poly.Rings = append(poly.Rings, len(nums)/2)
			}
			if len(poly.Rings) == 0 {
				return nil, "", nerr.New(nerr.InvalidArgument, "types.ParseGeneralWKT", "MULTIPOLYGON part has no ring")
			}
			g.Parts = append(g.Parts, poly)
		}
		return g, rest, nil
	case kw("GEOMETRYCOLLECTION"):
		body, rest, err := wktParen(s[18:])
		if err != nil {
			return nil, "", err
		}
		g := &Geom{Type: ogcGeometryCollection}
		body = strings.TrimSpace(body)
		if strings.EqualFold(body, "EMPTY") || body == "" {
			return g, rest, nil
		}
		for body != "" {
			sub, r, err := parseWKTGeom(body, depth+1)
			if err != nil {
				return nil, "", err
			}
			g.Parts = append(g.Parts, sub)
			r = strings.TrimSpace(r)
			if strings.HasPrefix(r, ",") {
				body = strings.TrimSpace(r[1:])
				continue
			}
			body = ""
			rest = r + rest
		}
		return g, rest, nil
	default:
		return nil, "", nerr.New(nerr.InvalidArgument, "types.ParseGeneralWKT", "unrecognized geometry keyword")
	}
}

// wktParen consumes a balanced (...) group at the front of s (after optional
// whitespace) and returns its inner text plus whatever follows the group.
func wktParen(s string) (inner, rest string, err error) {
	s = strings.TrimSpace(s)
	if s == "" || s[0] != '(' {
		return "", "", nerr.New(nerr.InvalidArgument, "types.ParseGeneralWKT", "expected (")
	}
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[1:i], s[i+1:], nil
			}
		}
	}
	return "", "", nerr.New(nerr.InvalidArgument, "types.ParseGeneralWKT", "unbalanced parentheses")
}

// splitWKTGroups splits "(a)(,)(b)" style text into the inner text of each
// top-level (...) group.
func splitWKTGroups(s string) []string {
	var out []string
	depth := 0
	start := -1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			if depth == 0 {
				start = i + 1
			}
			depth++
		case ')':
			depth--
			if depth == 0 && start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
		}
	}
	return out
}

// splitWKTPointList handles MULTIPOINT's two spellings — return each point's
// coordinate text whether wrapped in parens or not.
func splitWKTPointList(s string) []string {
	if strings.Contains(s, "(") {
		return splitWKTGroups(s)
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// FormatGeomWKT renders a Geom as WKT (no SRID prefix).
func FormatGeomWKT(g *Geom) string {
	var b strings.Builder
	writeWKT(&b, g)
	return b.String()
}

// FormatGeomEWKT renders a Geom as EWKT ("SRID=4326;POINT(...)").
func FormatGeomEWKT(g *Geom) string {
	return "SRID=" + strconv.FormatUint(uint64(g.SRID), 10) + ";" + FormatGeomWKT(g)
}

func writeWKT(b *strings.Builder, g *Geom) {
	num := func(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }
	pairs := func(c []float64) {
		for i := 0; i+1 < len(c); i += 2 {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(num(c[i]))
			b.WriteByte(' ')
			b.WriteString(num(c[i+1]))
		}
	}
	switch g.Type {
	case ogcPoint:
		b.WriteString("POINT(")
		pairs(g.Coords)
		b.WriteByte(')')
	case ogcLineString:
		b.WriteString("LINESTRING(")
		pairs(g.Coords)
		b.WriteByte(')')
	case ogcPolygon:
		b.WriteString("POLYGON(")
		off := 0
		for ri, n := range g.Rings {
			if ri > 0 {
				b.WriteString(", ")
			}
			b.WriteByte('(')
			pairs(g.Coords[off : off+n*2])
			b.WriteByte(')')
			off += n * 2
		}
		b.WriteByte(')')
	case ogcMultiPoint:
		b.WriteString("MULTIPOINT(")
		for i, p := range g.Parts {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteByte('(')
			pairs(p.Coords)
			b.WriteByte(')')
		}
		b.WriteByte(')')
	case ogcMultiLineString:
		b.WriteString("MULTILINESTRING(")
		for i, p := range g.Parts {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteByte('(')
			pairs(p.Coords)
			b.WriteByte(')')
		}
		b.WriteByte(')')
	case ogcMultiPolygon:
		b.WriteString("MULTIPOLYGON(")
		for i, p := range g.Parts {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteByte('(')
			off := 0
			for ri, n := range p.Rings {
				if ri > 0 {
					b.WriteString(", ")
				}
				b.WriteByte('(')
				pairs(p.Coords[off : off+n*2])
				b.WriteByte(')')
				off += n * 2
			}
			b.WriteByte(')')
		}
		b.WriteByte(')')
	case ogcGeometryCollection:
		b.WriteString("GEOMETRYCOLLECTION(")
		for i, p := range g.Parts {
			if i > 0 {
				b.WriteString(", ")
			}
			writeWKT(b, p)
		}
		b.WriteByte(')')
	}
}

// --- heap-row and sortable-key codec hooks -------------------------------

// encodeGeneralGeo writes the heap-row payload: a u32 total length prefix
// (for O(1) skipScalar) followed by canonical EWKB.
func encodeGeneralGeo(v Value) ([]byte, error) {
	if v.Geom == nil {
		return nil, nerr.New(nerr.InvalidArgument, "types.encodeGeneralGeo", "nil geometry value")
	}
	g := v.Geom.Clone()
	g.SRID = uint32(v.Typ.Precision)
	canonicalizeGeom(g)
	if v.Typ.Scale != 0 && g.Type != uint32(v.Typ.Scale) {
		return nil, nerr.New(nerr.InvalidArgument, "types.encodeGeneralGeo", "geometry subtype does not match the column")
	}
	body, err := EncodeEWKB(g)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 4+len(body))
	encoding.PutU32(out, 0, uint32(len(body)))
	copy(out[4:], body)
	return out, nil
}

func decodeGeneralGeo(raw []byte, off int, t Type) (Value, int, error) {
	n, err := encoding.ReadU32(raw, off)
	if err != nil {
		return Value{}, 0, err
	}
	body, err := encoding.ReadBytes(raw, off+4, int(n))
	if err != nil {
		return Value{}, 0, err
	}
	g, consumed, err := DecodeEWKB(body)
	if err != nil {
		return Value{}, 0, err
	}
	if consumed != len(body) {
		return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeGeneralGeo", "trailing bytes after geometry body")
	}
	if t.Scale != 0 && g.Type != uint32(t.Scale) {
		return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeGeneralGeo", "geometry subtype does not match the column")
	}
	return Value{Typ: t, Geom: g}, off + 4 + int(n), nil
}

// encodeSortableGeneralGeo is canonical EWKB run through the order-preserving
// byte escaping — a deterministic total order (docs/design-spatial.md §2.5).
func encodeSortableGeneralGeo(v Value) ([]byte, error) {
	if v.Geom == nil {
		return nil, nerr.New(nerr.InvalidArgument, "types.encodeSortableGeneralGeo", "nil geometry value")
	}
	g := v.Geom.Clone()
	g.SRID = uint32(v.Typ.Precision)
	canonicalizeGeom(g)
	body, err := EncodeEWKB(g)
	if err != nil {
		return nil, err
	}
	return encodeSortableBytes(body), nil
}

func decodeSortableGeneralGeo(raw []byte, off int, t Type) (Value, int, error) {
	body, next, err := decodeSortableBytes(raw, off)
	if err != nil {
		return Value{}, 0, err
	}
	g, _, err := DecodeEWKB(body)
	if err != nil {
		return Value{}, 0, err
	}
	return Value{Typ: t, Geom: g}, next, nil
}

// coerceGeneralGeo coerces a general-geometry value to a GEOMETRY / GEOGRAPHY
// column type: re-normalizes the SRID, checks the subtype, canonicalizes.
func coerceGeneralGeo(v Value, dest Type) (Value, error) {
	if v.Geom == nil {
		return Null(dest), nil
	}
	g := v.Geom.Clone()
	setSRID(g, uint32(dest.Precision))
	canonicalizeGeom(g)
	if dest.Scale != 0 && g.Type != uint32(dest.Scale) {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce",
			"geometry is "+GeomSubName(uint16(g.Type))+" but the column is "+dest.String())
	}
	if err := validateGeom(g, 0); err != nil {
		return Value{}, err
	}
	return Value{Typ: dest, Geom: g}, nil
}

// fixedShapeToGeom bridges a POINT / LINESTRING / POLYGON (and BOX, expanded
// to a polygon ring) value into a *Geom for the CAST bridge. ok is false for
// any other Kind. SRID is left 0 — the caller re-normalizes.
func fixedShapeToGeom(v Value) (*Geom, bool) {
	switch v.Typ.Kind {
	case KindPoint:
		return &Geom{Type: ogcPoint, Coords: []float64{v.Lon, v.Lat}}, true
	case KindLine:
		return &Geom{Type: ogcLineString, Coords: append([]float64(nil), v.Coords...)}, true
	case KindPolygon:
		return &Geom{Type: ogcPolygon, Coords: append([]float64(nil), v.Coords...), Rings: append([]int(nil), v.Rings...)}, true
	case KindBox:
		w, s, e, n := v.Box[0], v.Box[1], v.Box[2], v.Box[3]
		if w > e {
			return nil, false // antimeridian-wrapping box has no single ring
		}
		ring := []float64{w, s, e, s, e, n, w, n, w, s}
		return &Geom{Type: ogcPolygon, Coords: ring, Rings: []int{5}}, true
	default:
		return nil, false
	}
}

// geomToFixedShape unwraps a single-subtype *Geom back to a POINT /
// LINESTRING / POLYGON value for the reverse CAST bridge.
func geomToFixedShape(g *Geom, dest Type) (Value, error) {
	if g == nil {
		return Null(dest), nil
	}
	switch dest.Kind {
	case KindPoint:
		if g.Type != ogcPoint || len(g.Coords) != 2 {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "geometry is not a Point")
		}
		return PointValue(g.Coords[0], g.Coords[1])
	case KindLine:
		if g.Type != ogcLineString {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "geometry is not a LineString")
		}
		return LineValue(g.Coords)
	case KindPolygon:
		if g.Type != ogcPolygon {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "geometry is not a Polygon")
		}
		return PolygonValue(g.Coords, g.Rings)
	default:
		return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "unsupported CAST target")
	}
}

// spatialFuncs is the set of ST_* function names the general GEOMETRY /
// GEOGRAPHY family adds (docs/design-spatial.md §4). Names it shares with the
// four fixed WGS84 shapes (ST_X, ST_Distance, …) are omitted — IsGeoFunc
// already covers those.
var spatialFuncs = map[string]bool{
	"st_geomfromtext": true, "st_geometryfromtext": true, "st_geogfromtext": true,
	"st_geographyfromtext": true, "st_geomfromewkt": true, "st_point": true,
	"st_srid": true, "st_setsrid": true, "st_geometrytype": true,
	"st_numgeometries": true, "st_geometryn": true, "st_astext": true,
	"st_asewkt": true, "st_asbinary": true, "st_aswkb": true, "st_asewkb": true,
	"st_dimension": true, "st_isempty": true, "st_transform": true,
	"st_geogfromewkb": true, "st_geomfromwkb": true, "st_geomfromgeojson": true,
	"st_asgeojson": true, "st_dwithin": true, "st_contains": true,
	"st_covers": true, "st_coveredby": true, "st_within": true,
	"st_crosses": true, "st_overlaps": true, "st_touches": true,
	"st_equals": true, "st_relate": true, "st_boundary": true,
	"st_pointn": true, "st_startpoint": true, "st_endpoint": true,
	"st_exteriorring": true, "st_interiorringn": true, "st_numinteriorrings": true,
	"st_buffer": true, "st_intersection": true, "st_union": true,
	"st_difference": true, "st_symdifference": true, "st_simplify": true,
	"st_segmentize": true, "st_collect": true, "st_extent": true,
	"st_makevalid": true, "st_reverse": true, "st_force2d": true,
	"st_convexhull": true,
}

// IsSpatialFunc reports whether name is one of the ST_* functions specific to
// the general GEOMETRY / GEOGRAPHY family.
func IsSpatialFunc(name string) bool { return spatialFuncs[strings.ToLower(name)] }

// webMercatorR is the sphere radius Web Mercator (EPSG:3857) uses.
const webMercatorR = 6378137.0

// TransformGeom reprojects g to the target SRID. Only 4326 <-> 3857 is
// supported (closed-form spherical Mercator, no PROJ dependency —
// docs/design-spatial.md §2.3); any other pair errors.
func TransformGeom(g *Geom, target uint32) (*Geom, error) {
	if g.SRID == target {
		return g.Clone(), nil
	}
	var fn func(x, y float64) (float64, float64)
	switch {
	case g.SRID == uint32(SRIDWGS84) && target == uint32(SRIDWebMerc):
		fn = func(lon, lat float64) (float64, float64) {
			x := webMercatorR * lon * math.Pi / 180
			y := webMercatorR * math.Log(math.Tan(math.Pi/4+(lat*math.Pi/180)/2))
			return x, y
		}
	case g.SRID == uint32(SRIDWebMerc) && target == uint32(SRIDWGS84):
		fn = func(x, y float64) (float64, float64) {
			lon := x / webMercatorR * 180 / math.Pi
			lat := (2*math.Atan(math.Exp(y/webMercatorR)) - math.Pi/2) * 180 / math.Pi
			return lon, lat
		}
	default:
		return nil, nerr.New(nerr.InvalidArgument, "types.TransformGeom",
			"ST_Transform supports only SRID 4326 <-> 3857 (no PROJ dependency)")
	}
	out := g.Clone()
	var walk func(*Geom)
	walk = func(n *Geom) {
		n.SRID = target
		for i := 0; i+1 < len(n.Coords); i += 2 {
			n.Coords[i], n.Coords[i+1] = fn(n.Coords[i], n.Coords[i+1])
		}
		for _, p := range n.Parts {
			walk(p)
		}
	}
	walk(out)
	canonicalizeGeom(out)
	return out, nil
}

// --- GeoJSON (RFC 7946) ---------------------------------------------------
// No SRID: GeoJSON coordinates are implicitly WGS84 (CRS84); a decoded value
// takes the destination column's declared SRID, same as WKT/EWKT text.

type geoJSON struct {
	Type        string             `json:"type"`
	Coordinates stdjson.RawMessage `json:"coordinates,omitempty"`
	Geometries  []geoJSON          `json:"geometries,omitempty"`
}

// GeomToGeoJSON renders g as an RFC 7946 GeoJSON Geometry object.
func GeomToGeoJSON(g *Geom) ([]byte, error) {
	return stdjson.Marshal(geomToGeoJSONValue(g))
}

func geomToGeoJSONValue(g *Geom) map[string]any {
	pair := func(i int) []float64 { return []float64{g.Coords[i*2], g.Coords[i*2+1]} }
	switch g.Type {
	case ogcPoint:
		return map[string]any{"type": "Point", "coordinates": pair(0)}
	case ogcLineString:
		coords := make([][]float64, len(g.Coords)/2)
		for i := range coords {
			coords[i] = pair(i)
		}
		return map[string]any{"type": "LineString", "coordinates": coords}
	case ogcPolygon:
		rings := make([][][]float64, 0, len(g.Rings))
		off := 0
		for _, n := range g.Rings {
			ring := make([][]float64, n)
			for i := 0; i < n; i++ {
				ring[i] = []float64{g.Coords[(off+i)*2], g.Coords[(off+i)*2+1]}
			}
			rings = append(rings, ring)
			off += n
		}
		return map[string]any{"type": "Polygon", "coordinates": rings}
	case ogcMultiPoint:
		coords := make([][]float64, len(g.Parts))
		for i, p := range g.Parts {
			coords[i] = []float64{p.Coords[0], p.Coords[1]}
		}
		return map[string]any{"type": "MultiPoint", "coordinates": coords}
	case ogcMultiLineString:
		coords := make([][][]float64, len(g.Parts))
		for i, p := range g.Parts {
			lc := make([][]float64, len(p.Coords)/2)
			for j := range lc {
				lc[j] = []float64{p.Coords[j*2], p.Coords[j*2+1]}
			}
			coords[i] = lc
		}
		return map[string]any{"type": "MultiLineString", "coordinates": coords}
	case ogcMultiPolygon:
		coords := make([][][][]float64, len(g.Parts))
		for i, poly := range g.Parts {
			v := geomToGeoJSONValue(poly)
			coords[i] = v["coordinates"].([][][]float64)
		}
		return map[string]any{"type": "MultiPolygon", "coordinates": coords}
	case ogcGeometryCollection:
		geoms := make([]map[string]any, len(g.Parts))
		for i, p := range g.Parts {
			geoms[i] = geomToGeoJSONValue(p)
		}
		return map[string]any{"type": "GeometryCollection", "geometries": geoms}
	default:
		return map[string]any{}
	}
}

// ParseGeoJSON decodes an RFC 7946 GeoJSON Geometry object into a Geom.
// srid is applied to the whole tree (GeoJSON carries no CRS of its own).
func ParseGeoJSON(data []byte, srid uint32) (*Geom, error) {
	var gj geoJSON
	if err := stdjson.Unmarshal(data, &gj); err != nil {
		return nil, nerr.Wrap(nerr.InvalidArgument, "types.ParseGeoJSON", "invalid GeoJSON", err)
	}
	g, err := geoJSONToGeom(gj, 0)
	if err != nil {
		return nil, err
	}
	setSRID(g, srid)
	canonicalizeGeom(g)
	if err := validateGeom(g, 0); err != nil {
		return nil, err
	}
	return g, nil
}

func geoJSONToGeom(gj geoJSON, depth int) (*Geom, error) {
	if depth > MaxSpatialDepth {
		return nil, nerr.New(nerr.InvalidArgument, "types.ParseGeoJSON", "geometry nesting too deep")
	}
	dec := func(v any) error { return stdjson.Unmarshal(gj.Coordinates, v) }
	switch gj.Type {
	case "Point":
		var c []float64
		if err := dec(&c); err != nil || len(c) < 2 {
			return nil, nerr.New(nerr.InvalidArgument, "types.ParseGeoJSON", "invalid Point coordinates")
		}
		return &Geom{Type: ogcPoint, Coords: c[:2]}, nil
	case "LineString":
		var c [][]float64
		if err := dec(&c); err != nil {
			return nil, nerr.New(nerr.InvalidArgument, "types.ParseGeoJSON", "invalid LineString coordinates")
		}
		flat, err := flattenXY(c)
		if err != nil {
			return nil, err
		}
		return &Geom{Type: ogcLineString, Coords: flat}, nil
	case "Polygon":
		var rings [][][]float64
		if err := dec(&rings); err != nil {
			return nil, nerr.New(nerr.InvalidArgument, "types.ParseGeoJSON", "invalid Polygon coordinates")
		}
		g := &Geom{Type: ogcPolygon}
		for _, r := range rings {
			if len(r) < 4 {
				return nil, nerr.New(nerr.InvalidArgument, "types.ParseGeoJSON", "Polygon ring needs >= 4 positions")
			}
			flat, err := flattenXY(r)
			if err != nil {
				return nil, err
			}
			g.Coords = append(g.Coords, flat...)
			g.Rings = append(g.Rings, len(r))
		}
		if len(g.Rings) == 0 {
			return nil, nerr.New(nerr.InvalidArgument, "types.ParseGeoJSON", "Polygon requires an exterior ring")
		}
		return g, nil
	case "MultiPoint":
		var c [][]float64
		if err := dec(&c); err != nil {
			return nil, nerr.New(nerr.InvalidArgument, "types.ParseGeoJSON", "invalid MultiPoint coordinates")
		}
		g := &Geom{Type: ogcMultiPoint}
		for _, p := range c {
			if len(p) < 2 {
				return nil, nerr.New(nerr.InvalidArgument, "types.ParseGeoJSON", "invalid MultiPoint coordinates")
			}
			g.Parts = append(g.Parts, &Geom{Type: ogcPoint, Coords: p[:2]})
		}
		return g, nil
	case "MultiLineString":
		var c [][][]float64
		if err := dec(&c); err != nil {
			return nil, nerr.New(nerr.InvalidArgument, "types.ParseGeoJSON", "invalid MultiLineString coordinates")
		}
		g := &Geom{Type: ogcMultiLineString}
		for _, l := range c {
			flat, err := flattenXY(l)
			if err != nil {
				return nil, err
			}
			g.Parts = append(g.Parts, &Geom{Type: ogcLineString, Coords: flat})
		}
		return g, nil
	case "MultiPolygon":
		var c [][][][]float64
		if err := dec(&c); err != nil {
			return nil, nerr.New(nerr.InvalidArgument, "types.ParseGeoJSON", "invalid MultiPolygon coordinates")
		}
		g := &Geom{Type: ogcMultiPolygon}
		for _, poly := range c {
			p := &Geom{Type: ogcPolygon}
			for _, r := range poly {
				if len(r) < 4 {
					return nil, nerr.New(nerr.InvalidArgument, "types.ParseGeoJSON", "Polygon ring needs >= 4 positions")
				}
				flat, err := flattenXY(r)
				if err != nil {
					return nil, err
				}
				p.Coords = append(p.Coords, flat...)
				p.Rings = append(p.Rings, len(r))
			}
			if len(p.Rings) == 0 {
				return nil, nerr.New(nerr.InvalidArgument, "types.ParseGeoJSON", "MultiPolygon part requires an exterior ring")
			}
			g.Parts = append(g.Parts, p)
		}
		return g, nil
	case "GeometryCollection":
		if len(gj.Geometries) > MaxSpatialParts {
			return nil, nerr.New(nerr.InvalidArgument, "types.ParseGeoJSON", "too many geometry parts")
		}
		g := &Geom{Type: ogcGeometryCollection}
		for _, sub := range gj.Geometries {
			part, err := geoJSONToGeom(sub, depth+1)
			if err != nil {
				return nil, err
			}
			g.Parts = append(g.Parts, part)
		}
		return g, nil
	default:
		return nil, nerr.New(nerr.InvalidArgument, "types.ParseGeoJSON", "unknown or missing GeoJSON geometry type")
	}
}

// flattenXY flattens [x,y] position pairs. Every position must carry at
// least 2 coordinates (GeoJSON allows a 3rd altitude value, which this
// engine — 2D only, docs/design-spatial.md §8 — discards); a short position
// fails closed rather than silently shifting every later ring/segment.
func flattenXY(pts [][]float64) ([]float64, error) {
	out := make([]float64, 0, len(pts)*2)
	for _, p := range pts {
		if len(p) < 2 {
			return nil, nerr.New(nerr.InvalidArgument, "types.ParseGeoJSON", "position needs at least 2 coordinates")
		}
		out = append(out, p[0], p[1])
	}
	return out, nil
}

// GeomBBox returns the axis-aligned bounding box [minX, minY, maxX, maxY].
func GeomBBox(g *Geom) [4]float64 {
	bb := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	var walk func(*Geom)
	walk = func(n *Geom) {
		for i := 0; i+1 < len(n.Coords); i += 2 {
			x, y := n.Coords[i], n.Coords[i+1]
			if x < bb[0] {
				bb[0] = x
			}
			if y < bb[1] {
				bb[1] = y
			}
			if x > bb[2] {
				bb[2] = x
			}
			if y > bb[3] {
				bb[3] = y
			}
		}
		for _, p := range n.Parts {
			walk(p)
		}
	}
	walk(g)
	return bb
}
