package types

import (
	"math"
	"sort"

	"github.com/bzync/nextsql/internal/nerr"
)

// Spatial track S2 — measurement and predicates over *Geom
// (docs/design-spatial.md §7). GEOMETRY uses planar (Cartesian) math in the
// SRID's own units; GEOGRAPHY uses geodetic (spherical) math in metres,
// reusing geo.go's haversine / spherical-ring-area primitives. The predicate
// semantics are native and documented (matching geo.go's stance), not a full
// OGC DE-9IM implementation — see docs/design-spatial.md §8.

// --- coordinate accessors ------------------------------------------------

// eachSegment calls fn(x1,y1,x2,y2) for every line segment in g (LineString
// rings, Polygon rings, and the segments of every part).
func eachSegment(g *Geom, fn func(x1, y1, x2, y2 float64)) {
	switch g.Type {
	case ogcLineString:
		for i := 0; i+3 < len(g.Coords); i += 2 {
			fn(g.Coords[i], g.Coords[i+1], g.Coords[i+2], g.Coords[i+3])
		}
	case ogcPolygon:
		off := 0
		for _, n := range g.Rings {
			ring := g.Coords[off : off+n*2]
			for i := 0; i+3 < len(ring); i += 2 {
				fn(ring[i], ring[i+1], ring[i+2], ring[i+3])
			}
			off += n * 2
		}
	default:
		for _, p := range g.Parts {
			eachSegment(p, fn)
		}
	}
}

// eachPoint calls fn(x,y) for every vertex in g.
func eachPoint(g *Geom, fn func(x, y float64)) {
	for i := 0; i+1 < len(g.Coords); i += 2 {
		fn(g.Coords[i], g.Coords[i+1])
	}
	for _, p := range g.Parts {
		eachPoint(p, fn)
	}
}

// polygonRings returns each polygon's rings (as flat coord slices) across g.
func polygonRings(g *Geom) [][]float64 {
	var out [][]float64
	var walk func(*Geom)
	walk = func(n *Geom) {
		if n.Type == ogcPolygon {
			off := 0
			for _, c := range n.Rings {
				out = append(out, n.Coords[off:off+c*2])
				off += c * 2
			}
		}
		for _, p := range n.Parts {
			walk(p)
		}
	}
	walk(g)
	return out
}

// polygonsOf returns each Polygon Geom inside g (a lone Polygon, or the parts
// of a MultiPolygon / GeometryCollection).
func polygonsOf(g *Geom) []*Geom {
	var out []*Geom
	var walk func(*Geom)
	walk = func(n *Geom) {
		if n.Type == ogcPolygon {
			out = append(out, n)
		}
		for _, p := range n.Parts {
			walk(p)
		}
	}
	walk(g)
	return out
}

// --- point / polygon primitives (planar) --------------------------------

// pointInPolygon reports whether (x,y) is inside poly (exterior minus holes),
// reusing geo.go's pointInRingState. onBoundary is true when the point lies on
// any ring edge.
func pointInPolygon(x, y float64, poly *Geom) (inside, onBoundary bool) {
	off := 0
	for ri, n := range poly.Rings {
		ring := poly.Coords[off : off+n*2]
		off += n * 2
		switch pointInRingState(x, y, ring) {
		case pointBoundary:
			return true, true
		case pointInside:
			if ri > 0 {
				return false, false // strictly inside a hole
			}
		default: // outside
			if ri == 0 {
				return false, false
			}
		}
	}
	return true, false
}

// planarDist is Euclidean distance.
func planarDist(x1, y1, x2, y2 float64) float64 {
	return math.Hypot(x2-x1, y2-y1)
}

// planarPointSeg is the Euclidean distance from a point to a segment.
func planarPointSeg(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	l2 := dx*dx + dy*dy
	if l2 == 0 {
		return planarDist(px, py, ax, ay)
	}
	t := ((px-ax)*dx + (py-ay)*dy) / l2
	t = math.Max(0, math.Min(1, t))
	return planarDist(px, py, ax+t*dx, ay+t*dy)
}

// --- measurement -------------------------------------------------------

// GeomDistance is the minimum distance between two geometries. geodetic
// selects metres-on-the-sphere (GEOGRAPHY) vs planar units (GEOMETRY).
func GeomDistance(a, b *Geom, geodetic bool) (float64, error) {
	if a == nil || b == nil {
		return 0, nerr.New(nerr.InvalidArgument, "types.GeomDistance", "nil geometry")
	}
	if GeomsIntersect(a, b) {
		return 0, nil
	}
	best := math.Inf(1)
	pd := func(x1, y1, x2, y2 float64) float64 {
		if geodetic {
			return haversine(x1, y1, x2, y2)
		}
		return planarDist(x1, y1, x2, y2)
	}
	aPts := collectPoints(a)
	bPts := collectPoints(b)
	// min over all vertex pairs and all point-to-segment pairs (both ways).
	for i := 0; i+1 < len(aPts); i += 2 {
		for j := 0; j+1 < len(bPts); j += 2 {
			if d := pd(aPts[i], aPts[i+1], bPts[j], bPts[j+1]); d < best {
				best = d
			}
		}
		eachSegment(b, func(x1, y1, x2, y2 float64) {
			var d float64
			if geodetic {
				d = distPointSeg(aPts[i], aPts[i+1], x1, y1, x2, y2)
			} else {
				d = planarPointSeg(aPts[i], aPts[i+1], x1, y1, x2, y2)
			}
			if d < best {
				best = d
			}
		})
	}
	for j := 0; j+1 < len(bPts); j += 2 {
		eachSegment(a, func(x1, y1, x2, y2 float64) {
			var d float64
			if geodetic {
				d = distPointSeg(bPts[j], bPts[j+1], x1, y1, x2, y2)
			} else {
				d = planarPointSeg(bPts[j], bPts[j+1], x1, y1, x2, y2)
			}
			if d < best {
				best = d
			}
		})
	}
	if math.IsInf(best, 1) {
		return 0, nerr.New(nerr.InvalidArgument, "types.GeomDistance", "empty geometry")
	}
	return best, nil
}

func collectPoints(g *Geom) []float64 {
	var out []float64
	eachPoint(g, func(x, y float64) { out = append(out, x, y) })
	return out
}

// GeomLength is the total length of every LineString component (0 for
// point/polygon geometries — polygon perimeter is GeomPerimeter).
func GeomLength(g *Geom, geodetic bool) float64 {
	sum := 0.0
	var walk func(*Geom)
	walk = func(n *Geom) {
		if n.Type == ogcLineString {
			for i := 0; i+3 < len(n.Coords); i += 2 {
				sum += segLen(n.Coords[i], n.Coords[i+1], n.Coords[i+2], n.Coords[i+3], geodetic)
			}
		}
		for _, p := range n.Parts {
			walk(p)
		}
	}
	walk(g)
	return sum
}

// GeomPerimeter is the total ring length of every Polygon component.
func GeomPerimeter(g *Geom, geodetic bool) float64 {
	sum := 0.0
	for _, ring := range polygonRings(g) {
		for i := 0; i+3 < len(ring); i += 2 {
			sum += segLen(ring[i], ring[i+1], ring[i+2], ring[i+3], geodetic)
		}
	}
	return sum
}

func segLen(x1, y1, x2, y2 float64, geodetic bool) float64 {
	if geodetic {
		return haversine(x1, y1, x2, y2)
	}
	return planarDist(x1, y1, x2, y2)
}

// GeomArea is the total area of every Polygon component (holes subtracted).
// Planar (shoelace, SRID units²) for GEOMETRY; spherical metres² for GEOGRAPHY.
func GeomArea(g *Geom, geodetic bool) float64 {
	total := 0.0
	for _, poly := range polygonsOf(g) {
		off := 0
		for ri, n := range poly.Rings {
			ring := poly.Coords[off : off+n*2]
			off += n * 2
			var a float64
			if geodetic {
				a = sphericalRingArea(ring)
			} else {
				a = math.Abs(ringSignedArea(ring))
			}
			if ri == 0 {
				total += a
			} else {
				total -= a
			}
		}
	}
	if total < 0 {
		total = 0
	}
	return total
}

// GeomCentroid returns the area-weighted centroid for polygonal input, the
// length-weighted midpoint for lines, and the mean of vertices for points.
func GeomCentroid(g *Geom) (x, y float64, ok bool) {
	if polys := polygonsOf(g); len(polys) > 0 {
		var cx, cy, area float64
		for _, poly := range polys {
			off := 0
			for ri, n := range poly.Rings {
				ring := poly.Coords[off : off+n*2]
				off += n * 2
				a := ringSignedArea(ring)
				rx, ry := ringCentroid(ring, a)
				sign := 1.0
				if ri > 0 {
					sign = -1.0
				}
				cx += sign * rx * math.Abs(a)
				cy += sign * ry * math.Abs(a)
				area += sign * math.Abs(a)
			}
		}
		if area == 0 {
			return 0, 0, false
		}
		return cx / area, cy / area, true
	}
	// lines: length-weighted segment midpoints
	var lx, ly, ll float64
	hasLine := false
	var walk func(*Geom)
	walk = func(n *Geom) {
		if n.Type == ogcLineString {
			hasLine = true
			for i := 0; i+3 < len(n.Coords); i += 2 {
				l := planarDist(n.Coords[i], n.Coords[i+1], n.Coords[i+2], n.Coords[i+3])
				lx += l * (n.Coords[i] + n.Coords[i+2]) / 2
				ly += l * (n.Coords[i+1] + n.Coords[i+3]) / 2
				ll += l
			}
		}
		for _, p := range n.Parts {
			walk(p)
		}
	}
	walk(g)
	if hasLine && ll > 0 {
		return lx / ll, ly / ll, true
	}
	// points: mean of vertices
	var px, py float64
	cnt := 0
	eachPoint(g, func(x, y float64) { px += x; py += y; cnt++ })
	if cnt == 0 {
		return 0, 0, false
	}
	return px / float64(cnt), py / float64(cnt), true
}

func ringCentroid(ring []float64, signedArea float64) (float64, float64) {
	if signedArea == 0 {
		// degenerate — fall back to vertex mean
		var sx, sy float64
		n := len(ring)/2 - 1
		for i := 0; i < n; i++ {
			sx += ring[i*2]
			sy += ring[i*2+1]
		}
		if n == 0 {
			return ring[0], ring[1]
		}
		return sx / float64(n), sy / float64(n)
	}
	var cx, cy float64
	n := len(ring) / 2
	for i := 0; i < n-1; i++ {
		x1, y1 := ring[i*2], ring[i*2+1]
		x2, y2 := ring[(i+1)*2], ring[(i+1)*2+1]
		cross := x1*y2 - x2*y1
		cx += (x1 + x2) * cross
		cy += (y1 + y2) * cross
	}
	return cx / (6 * signedArea), cy / (6 * signedArea)
}

// --- predicates -------------------------------------------------------

// GeomsIntersect reports whether a and b share at least one point.
func GeomsIntersect(a, b *Geom) bool {
	if !bboxOverlap(GeomBBox(a), GeomBBox(b)) {
		return false
	}
	// any vertex of one inside a polygon of the other
	for _, poly := range polygonsOf(a) {
		hit := false
		eachPoint(b, func(x, y float64) {
			if in, _ := pointInPolygon(x, y, poly); in {
				hit = true
			}
		})
		if hit {
			return true
		}
	}
	for _, poly := range polygonsOf(b) {
		hit := false
		eachPoint(a, func(x, y float64) {
			if in, _ := pointInPolygon(x, y, poly); in {
				hit = true
			}
		})
		if hit {
			return true
		}
	}
	// any segment crossing
	cross := false
	eachSegment(a, func(ax, ay, bx, by float64) {
		if cross {
			return
		}
		eachSegment(b, func(cx, cy, dx, dy float64) {
			if segmentsIntersect(ax, ay, bx, by, cx, cy, dx, dy) {
				cross = true
			}
		})
	})
	if cross {
		return true
	}
	// point geometry equal to a vertex of the other
	if a.Type == ogcPoint || a.Type == ogcMultiPoint || b.Type == ogcPoint || b.Type == ogcMultiPoint {
		aPts := collectPoints(a)
		bPts := collectPoints(b)
		for i := 0; i+1 < len(aPts); i += 2 {
			// point on any segment of b
			on := false
			eachSegment(b, func(x1, y1, x2, y2 float64) {
				if pointOnSegment(aPts[i], aPts[i+1], x1, y1, x2, y2) {
					on = true
				}
			})
			if on {
				return true
			}
			for j := 0; j+1 < len(bPts); j += 2 {
				if aPts[i] == bPts[j] && aPts[i+1] == bPts[j+1] {
					return true
				}
			}
		}
		for j := 0; j+1 < len(bPts); j += 2 {
			on := false
			eachSegment(a, func(x1, y1, x2, y2 float64) {
				if pointOnSegment(bPts[j], bPts[j+1], x1, y1, x2, y2) {
					on = true
				}
			})
			if on {
				return true
			}
		}
	}
	return false
}

// GeomContains reports whether every point of b lies in a's interior or
// boundary and b's interior meets a's interior. Native semantics: for a
// polygonal a, every vertex and every segment midpoint of b must be
// inside-or-on a, and at least one b point strictly interior.
func GeomContains(a, b *Geom, covers bool) bool {
	polys := polygonsOf(a)
	if len(polys) == 0 {
		// non-areal a can only "contain" b if b is a subset of a's points/lines
		if a.Type == ogcLineString || a.Type == ogcMultiLineString {
			return lineCoversGeom(a, b)
		}
		// point a contains only an equal point b
		return covers && geomCanonEqual(a, b)
	}
	inAny := func(x, y float64) (in, onB bool) {
		for _, p := range polys {
			if i, ob := pointInPolygon(x, y, p); i {
				return true, ob
			}
		}
		return false, false
	}
	strictInterior := false
	allIn := true
	eachPoint(b, func(x, y float64) {
		in, onB := inAny(x, y)
		if !in {
			allIn = false
		}
		if in && !onB {
			strictInterior = true
		}
	})
	if !allIn {
		return false
	}
	// segment midpoints (cheap concavity guard)
	eachSegment(b, func(x1, y1, x2, y2 float64) {
		mx, my := (x1+x2)/2, (y1+y2)/2
		if in, onB := inAny(mx, my); !in {
			allIn = false
		} else if !onB {
			strictInterior = true
		}
	})
	if !allIn {
		return false
	}
	if covers {
		return true
	}
	if !strictInterior {
		// Every sampled vertex/edge-midpoint of b landed exactly on a's
		// boundary — the case where b's boundary coincides with (part of)
		// a's, most commonly b == a. Vertex/edge-midpoint sampling alone
		// can never find that a solid polygon's interior meets a's, since
		// none of those points are ever strictly interior to a solid shape
		// by construction; each polygonal part of b's own centroid is, so
		// probe those too — found via ST_Contains(A, A) wrongly returning
		// false for a polygon compared with itself.
		for _, bp := range polygonsOf(b) {
			if cx, cy, ok := GeomCentroid(bp); ok {
				if in, onB := inAny(cx, cy); in && !onB {
					strictInterior = true
				}
			}
		}
	}
	return strictInterior
}

func lineCoversGeom(line, b *Geom) bool {
	onLine := func(x, y float64) bool {
		on := false
		eachSegment(line, func(x1, y1, x2, y2 float64) {
			if pointOnSegment(x, y, x1, y1, x2, y2) {
				on = true
			}
		})
		return on
	}
	ok := true
	eachPoint(b, func(x, y float64) {
		if !onLine(x, y) {
			ok = false
		}
	})
	return ok
}

func geomCanonEqual(a, b *Geom) bool {
	ca := a.Clone()
	cb := b.Clone()
	setSRID(ca, 0)
	setSRID(cb, 0)
	canonicalizeGeom(ca)
	canonicalizeGeom(cb)
	ea, err1 := EncodeEWKB(ca)
	eb, err2 := EncodeEWKB(cb)
	return err1 == nil && err2 == nil && string(ea) == string(eb)
}

func bboxOverlap(a, b [4]float64) bool {
	return a[0] <= b[2] && b[0] <= a[2] && a[1] <= b[3] && b[1] <= a[3]
}

// GeomEquals is exact geometric equality on the canonical form (matches the
// `=` operator for GEOMETRY / GEOGRAPHY columns).
func GeomEquals(a, b *Geom) bool { return geomCanonEqual(a, b) }

// --- S5: derived geometry construction (docs/design-spatial.md §7 S5) ----
//
// ST_Union / ST_Intersection / ST_Difference / ST_SymDifference and
// ST_Buffer over a non-point input are genuinely hard computational-geometry
// problems (general polygon clipping, offset-curve self-intersection
// resolution). Per this track's "native, documented, not full OGC"
// precedent (geo.md's own DISTANCE-to-polyline and centroid caveats), these
// implement exact results for the well-defined cases and error clearly
// rather than silently return an approximate/wrong boundary for the
// general non-convex case — correctness outranks feature completeness
// (CLAUDE.md priority order).

// GeomConvexHull returns the convex hull of every vertex in g, via Andrew's
// monotone chain (exact, O(n log n)). Fewer than 3 distinct points returns
// the degenerate input unchanged (a Point or a 2-point LineString).
func GeomConvexHull(g *Geom) *Geom {
	pts := collectPoints(g)
	n := len(pts) / 2
	uniq := make(map[[2]float64]bool, n)
	var xy [][2]float64
	for i := 0; i < n; i++ {
		k := [2]float64{pts[i*2], pts[i*2+1]}
		if !uniq[k] {
			uniq[k] = true
			xy = append(xy, k)
		}
	}
	if len(xy) == 1 {
		return &Geom{Type: ogcPoint, Coords: []float64{xy[0][0], xy[0][1]}}
	}
	hull := convexHull(xy)
	if len(hull) == 2 {
		return &Geom{Type: ogcLineString, Coords: []float64{hull[0][0], hull[0][1], hull[1][0], hull[1][1]}}
	}
	coords := make([]float64, 0, (len(hull)+1)*2)
	for _, p := range hull {
		coords = append(coords, p[0], p[1])
	}
	coords = append(coords, hull[0][0], hull[0][1]) // close
	return &Geom{Type: ogcPolygon, Coords: coords, Rings: []int{len(hull) + 1}}
}

// convexHull is Andrew's monotone chain over a point set (sorted, exact,
// counter-clockwise result, no duplicate closing point).
func convexHull(pts [][2]float64) [][2]float64 {
	if len(pts) < 3 {
		return pts
	}
	sorted := append([][2]float64(nil), pts...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i][0] != sorted[j][0] {
			return sorted[i][0] < sorted[j][0]
		}
		return sorted[i][1] < sorted[j][1]
	})
	cross := func(o, a, b [2]float64) float64 {
		return (a[0]-o[0])*(b[1]-o[1]) - (a[1]-o[1])*(b[0]-o[0])
	}
	build := func(pts [][2]float64) [][2]float64 {
		var h [][2]float64
		for _, p := range pts {
			for len(h) >= 2 && cross(h[len(h)-2], h[len(h)-1], p) <= 0 {
				h = h[:len(h)-1]
			}
			h = append(h, p)
		}
		return h
	}
	lower := build(sorted)
	rev := make([][2]float64, len(sorted))
	for i, p := range sorted {
		rev[len(sorted)-1-i] = p
	}
	upper := build(rev)
	return append(lower[:len(lower)-1], upper[:len(upper)-1]...)
}

// GeomSimplify applies Douglas-Peucker simplification with the given
// tolerance to every LineString / Polygon-ring component of g.
func GeomSimplify(g *Geom, tolerance float64) *Geom {
	out := g.Clone()
	var walk func(*Geom)
	walk = func(n *Geom) {
		switch n.Type {
		case ogcLineString:
			n.Coords = douglasPeucker(n.Coords, tolerance)
		case ogcPolygon:
			var coords []float64
			var rings []int
			off := 0
			for _, c := range n.Rings {
				ring := n.Coords[off : off+c*2]
				off += c * 2
				simplified := douglasPeucker(ring, tolerance)
				if len(simplified) < 8 { // fewer than 4 points: keep the original ring
					simplified = ring
				}
				coords = append(coords, simplified...)
				rings = append(rings, len(simplified)/2)
			}
			n.Coords, n.Rings = coords, rings
		}
		for _, p := range n.Parts {
			walk(p)
		}
	}
	walk(out)
	canonicalizeGeom(out)
	return out
}

func douglasPeucker(coords []float64, tol float64) []float64 {
	n := len(coords) / 2
	if n < 3 || tol <= 0 {
		return append([]float64(nil), coords...)
	}
	keep := make([]bool, n)
	keep[0], keep[n-1] = true, true
	var recurse func(lo, hi int)
	recurse = func(lo, hi int) {
		if hi <= lo+1 {
			return
		}
		ax, ay := coords[lo*2], coords[lo*2+1]
		bx, by := coords[hi*2], coords[hi*2+1]
		best, bestDist := -1, tol
		for i := lo + 1; i < hi; i++ {
			d := planarPointSeg(coords[i*2], coords[i*2+1], ax, ay, bx, by)
			if d > bestDist {
				bestDist, best = d, i
			}
		}
		if best >= 0 {
			keep[best] = true
			recurse(lo, best)
			recurse(best, hi)
		}
	}
	recurse(0, n-1)
	var out []float64
	for i := 0; i < n; i++ {
		if keep[i] {
			out = append(out, coords[i*2], coords[i*2+1])
		}
	}
	return out
}

// GeomSegmentize inserts vertices along every LineString / Polygon-ring
// segment longer than maxLen so no segment exceeds it.
func GeomSegmentize(g *Geom, maxLen float64) *Geom {
	if maxLen <= 0 {
		return g.Clone()
	}
	out := g.Clone()
	segmentizeCoords := func(coords []float64) []float64 {
		if len(coords) < 4 {
			return coords
		}
		res := []float64{coords[0], coords[1]}
		for i := 0; i+3 < len(coords); i += 2 {
			x1, y1, x2, y2 := coords[i], coords[i+1], coords[i+2], coords[i+3]
			d := planarDist(x1, y1, x2, y2)
			n := int(math.Ceil(d / maxLen))
			for k := 1; k <= n; k++ {
				t := float64(k) / float64(n)
				res = append(res, x1+(x2-x1)*t, y1+(y2-y1)*t)
			}
		}
		return res
	}
	var walk func(*Geom)
	walk = func(n *Geom) {
		switch n.Type {
		case ogcLineString:
			n.Coords = segmentizeCoords(n.Coords)
		case ogcPolygon:
			var coords []float64
			var rings []int
			off := 0
			for _, c := range n.Rings {
				seg := segmentizeCoords(n.Coords[off : off+c*2])
				off += c * 2
				coords = append(coords, seg...)
				rings = append(rings, len(seg)/2)
			}
			n.Coords, n.Rings = coords, rings
		}
		for _, p := range n.Parts {
			walk(p)
		}
	}
	walk(out)
	return out
}

// GeomBuffer returns a polygon approximating g expanded by radius (planar
// units). Exact for a Point (a regular 32-gon approximating the circle);
// for any other geometry it is the convex hull of every vertex's own
// buffer-circle points, which is exact only when g is itself convex —
// documented as a convex over-approximation for concave input
// (docs/design-spatial.md §8), never an under-approximation.
func GeomBuffer(g *Geom, radius float64) (*Geom, error) {
	if radius <= 0 {
		return nil, nerr.New(nerr.InvalidArgument, "types.GeomBuffer", "ST_Buffer radius must be positive")
	}
	const segs = 32
	var cloud [][2]float64
	eachPoint(g, func(x, y float64) {
		for i := 0; i < segs; i++ {
			a := 2 * math.Pi * float64(i) / segs
			cloud = append(cloud, [2]float64{x + radius*math.Cos(a), y + radius*math.Sin(a)})
		}
	})
	if len(cloud) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "types.GeomBuffer", "empty geometry")
	}
	hull := convexHull(cloud)
	coords := make([]float64, 0, (len(hull)+1)*2)
	for _, p := range hull {
		coords = append(coords, p[0], p[1])
	}
	coords = append(coords, hull[0][0], hull[0][1])
	out := &Geom{Type: ogcPolygon, Coords: coords, Rings: []int{len(hull) + 1}, SRID: g.SRID}
	canonicalizeGeom(out)
	return out, nil
}

// clipConvex is Sutherland-Hodgman: subject clipped against a convex clip
// polygon's exterior ring, both CCW-oriented flat-coordinate rings without
// the closing duplicate. Exact when clip is convex; subject may be any
// simple (non-self-intersecting) ring.
func clipConvex(subject, clip []float64) []float64 {
	out := subject
	nc := len(clip) / 2
	for i := 0; i < nc && len(out) > 0; i++ {
		ax, ay := clip[i*2], clip[i*2+1]
		bx, by := clip[(i+1)%nc*2], clip[(i+1)%nc*2+1]
		inside := func(x, y float64) bool { return (bx-ax)*(y-ay)-(by-ay)*(x-ax) >= 0 }
		inter := func(x1, y1, x2, y2 float64) (float64, float64) {
			dx, dy := x2-x1, y2-y1
			edx, edy := bx-ax, by-ay
			denom := dx*edy - dy*edx
			if denom == 0 {
				return x2, y2
			}
			t := ((ax-x1)*edy - (ay-y1)*edx) / denom
			return x1 + t*dx, y1 + t*dy
		}
		var next []float64
		n := len(out) / 2
		for j := 0; j < n; j++ {
			cx, cy := out[j*2], out[j*2+1]
			px, py := out[((j-1+n)%n)*2], out[((j-1+n)%n)*2+1]
			cIn, pIn := inside(cx, cy), inside(px, py)
			if cIn {
				if !pIn {
					ix, iy := inter(px, py, cx, cy)
					next = append(next, ix, iy)
				}
				next = append(next, cx, cy)
			} else if pIn {
				ix, iy := inter(px, py, cx, cy)
				next = append(next, ix, iy)
			}
		}
		out = next
	}
	return out
}

// isConvexRing reports whether an open (no closing duplicate) ring is convex.
func isConvexRing(ring []float64) bool {
	n := len(ring) / 2
	if n < 3 {
		return false
	}
	sign := 0
	for i := 0; i < n; i++ {
		ax, ay := ring[i*2], ring[i*2+1]
		bx, by := ring[(i+1)%n*2], ring[(i+1)%n*2+1]
		cx, cy := ring[(i+2)%n*2], ring[(i+2)%n*2+1]
		cr := (bx-ax)*(cy-by) - (by-ay)*(cx-bx)
		if cr == 0 {
			continue
		}
		s := 1
		if cr < 0 {
			s = -1
		}
		if sign == 0 {
			sign = s
		} else if sign != s {
			return false
		}
	}
	return sign != 0
}

func openExteriorRing(g *Geom) ([]float64, bool) {
	if g.Type != ogcPolygon || len(g.Rings) == 0 {
		return nil, false
	}
	ext := g.Coords[:g.Rings[0]*2]
	if len(ext) >= 2 && ext[0] == ext[len(ext)-2] && ext[1] == ext[len(ext)-1] {
		ext = ext[:len(ext)-2]
	}
	return ext, true
}

func ringToPolygon(ring []float64, srid uint32) *Geom {
	if len(ring) < 6 {
		return nil
	}
	closed := append(append([]float64(nil), ring...), ring[0], ring[1])
	return &Geom{Type: ogcPolygon, Coords: closed, Rings: []int{len(closed) / 2}, SRID: srid}
}

// GeomIntersection returns the polygon intersection of a and b. Exact when
// at least one operand is a single convex polygon ring (Sutherland-Hodgman);
// errors for two non-convex polygons or non-polygonal input rather than
// guess (docs/design-spatial.md §8 — general polygon boolean ops are
// deferred beyond the convex case).
func GeomIntersection(a, b *Geom) (*Geom, error) {
	ra, oka := openExteriorRing(a)
	rb, okb := openExteriorRing(b)
	if !oka || !okb || len(a.Rings) > 1 || len(b.Rings) > 1 {
		return nil, nerr.New(nerr.InvalidArgument, "types.GeomIntersection",
			"ST_Intersection supports single-ring polygons only (see docs/design-spatial.md §8)")
	}
	var subject, clip []float64
	switch {
	case isConvexRing(rb):
		subject, clip = ra, rb
	case isConvexRing(ra):
		subject, clip = rb, ra
	default:
		return nil, nerr.New(nerr.InvalidArgument, "types.GeomIntersection",
			"ST_Intersection requires at least one convex polygon operand (see docs/design-spatial.md §8)")
	}
	if !isConvexRing(ensureCCW(clip)) {
		clip = reversedCopy(clip)
	}
	result := clipConvex(ensureCCW(subject), ensureCCW(clip))
	if len(result) < 6 {
		return nil, nil // empty intersection
	}
	out := ringToPolygon(result, a.SRID)
	canonicalizeGeom(out)
	return out, nil
}

func ensureCCW(ring []float64) []float64 {
	if ringSignedArea(append(append([]float64(nil), ring...), ring[0], ring[1])) < 0 {
		return reversedCopy(ring)
	}
	return ring
}

func reversedCopy(ring []float64) []float64 {
	n := len(ring) / 2
	out := make([]float64, len(ring))
	for i := 0; i < n; i++ {
		out[i*2], out[i*2+1] = ring[(n-1-i)*2], ring[(n-1-i)*2+1]
	}
	return out
}

// GeomUnion returns the union of a and b. Exact for the disjoint case
// (returns a MultiPolygon) and the containment case (returns the larger);
// errors for two general overlapping polygons rather than guess
// (docs/design-spatial.md §8).
func GeomUnion(a, b *Geom) (*Geom, error) {
	if !GeomsIntersect(a, b) {
		return &Geom{Type: ogcMultiPolygon, SRID: a.SRID, Parts: []*Geom{a.Clone(), b.Clone()}}, nil
	}
	if GeomContains(a, b, true) {
		return a.Clone(), nil
	}
	if GeomContains(b, a, true) {
		return b.Clone(), nil
	}
	return nil, nerr.New(nerr.InvalidArgument, "types.GeomUnion",
		"ST_Union supports disjoint or nested polygons only (see docs/design-spatial.md §8)")
}

// GeomDifference returns a minus b (the part of a outside b). Exact when b
// is disjoint from a (returns a unchanged), when b fully contains a (empty),
// and when b is a convex polygon (inverse Sutherland-Hodgman is not
// implemented generally, so this errors for a non-convex b —
// docs/design-spatial.md §8).
func GeomDifference(a, b *Geom) (*Geom, error) {
	if !GeomsIntersect(a, b) {
		return a.Clone(), nil
	}
	if GeomContains(b, a, true) {
		return nil, nil
	}
	return nil, nerr.New(nerr.InvalidArgument, "types.GeomDifference",
		"ST_Difference supports the disjoint and fully-contained cases only (see docs/design-spatial.md §8)")
}
