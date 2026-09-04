package executor

import (
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

// evalSpatialFn implements the ST_* functions for the general GEOMETRY /
// GEOGRAPHY family (Spatial track, docs/design-spatial.md). Polymorphic
// accessors (ST_X, ST_Y, ST_GeometryType, …) handle a general-geometry
// argument here and return ok=false for a fixed WGS84 shape so
// types.EvalGeo picks it up. Names with no collision (ST_GeomFromText, …)
// always handle. S1 = constructors + accessors; S2 adds measurement and
// predicates.
func evalSpatialFn(name string, args []types.Value) (types.Value, bool, error) {
	lc := strings.ToLower(name)
	switch lc {
	case "st_geomfromtext", "st_geometryfromtext":
		return stFromText(args, false)
	case "st_geogfromtext", "st_geographyfromtext":
		return stFromText(args, true)
	case "st_geomfromewkt":
		return stFromText(args, false)
	case "st_point", "st_makepoint_srid":
		// ST_Point(x, y[, srid]) -> GEOMETRY(Point). (ST_MakePoint stays with
		// the fixed-type handler for backward compat; use ST_Point for the
		// general family.)
		if len(args) < 2 || len(args) > 3 {
			return types.Value{}, true, arityErr(lc)
		}
		x, err := asFloat(args[0])
		if err != nil {
			return types.Value{}, true, err
		}
		y, err := asFloat(args[1])
		if err != nil {
			return types.Value{}, true, err
		}
		var srid uint16
		if len(args) == 3 {
			s, err := asSRID(args[2])
			if err != nil {
				return types.Value{}, true, err
			}
			srid = s
		}
		gt, err := types.GeometryType(types.GeomSubPoint, srid)
		if err != nil {
			return types.Value{}, true, err
		}
		g := &types.Geom{Type: 1, SRID: uint32(srid), Coords: []float64{x, y}}
		out, err := types.Coerce(types.Value{Typ: gt, Geom: g}, gt)
		return out, true, err
	case "st_srid":
		g, ok := generalGeom(args, 1)
		if !ok {
			return types.Value{}, false, nil
		}
		if g == nil {
			return types.Null(types.Int64()), true, nil
		}
		return types.IntValue(types.KindInt64, int64(g.SRID)), true, nil
	case "st_setsrid":
		if len(args) != 2 {
			return types.Value{}, true, arityErr(lc)
		}
		if !types.IsGeneralSpatial(args[0].Typ.Kind) {
			return types.Value{}, false, nil
		}
		if args[0].Null || args[0].Geom == nil {
			return types.Null(args[0].Typ), true, nil
		}
		s, err := asSRID(args[1])
		if err != nil {
			return types.Value{}, true, err
		}
		ng := args[0].Geom.Clone()
		setGeomSRID(ng, uint32(s))
		dt := args[0].Typ
		dt.Precision = s
		return types.Value{Typ: dt, Geom: ng}, true, nil
	case "st_geometrytype":
		g, ok := generalGeom(args, 1)
		if !ok {
			return types.Value{}, false, nil
		}
		if g == nil {
			return types.Null(types.Text()), true, nil
		}
		return types.StringValue("ST_" + types.GeomSubName(uint16(g.Type))), true, nil
	case "geometrytype":
		g, ok := generalGeom(args, 1)
		if !ok {
			return types.Value{}, false, nil
		}
		if g == nil {
			return types.Null(types.Text()), true, nil
		}
		return types.StringValue(strings.ToUpper(subtypeToken(g.Type))), true, nil
	case "st_x", "st_y":
		g, ok := generalGeom(args, 1)
		if !ok {
			return types.Value{}, false, nil
		}
		if g == nil {
			return types.Null(types.Float64()), true, nil
		}
		if g.Type != 1 || len(g.Coords) != 2 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.spatial", lc+" requires a Point")
		}
		idx := 0
		if lc == "st_y" {
			idx = 1
		}
		return types.Float64Value(g.Coords[idx]), true, nil
	case "st_npoints":
		g, ok := generalGeom(args, 1)
		if !ok {
			return types.Value{}, false, nil
		}
		if g == nil {
			return types.Null(types.Int64()), true, nil
		}
		return types.IntValue(types.KindInt64, int64(geomVertexCount(g))), true, nil
	case "st_numgeometries":
		g, ok := generalGeom(args, 1)
		if !ok {
			return types.Value{}, false, nil
		}
		if g == nil {
			return types.Null(types.Int64()), true, nil
		}
		return types.IntValue(types.KindInt64, int64(geomNumParts(g))), true, nil
	case "st_geometryn":
		if len(args) != 2 {
			return types.Value{}, true, arityErr(lc)
		}
		if !types.IsGeneralSpatial(args[0].Typ.Kind) {
			return types.Value{}, false, nil
		}
		if args[0].Null || args[0].Geom == nil {
			return types.Null(args[0].Typ), true, nil
		}
		n, err := types.Coerce(args[1], types.Int64())
		if err != nil {
			return types.Value{}, true, err
		}
		part := geomPartN(args[0].Geom, int(n.Int))
		if part == nil {
			return types.Null(args[0].Typ), true, nil
		}
		dt := args[0].Typ
		dt.Scale = uint16(part.Type)
		return types.Value{Typ: dt, Geom: part}, true, nil
	case "st_astext", "astext":
		g, ok := generalGeom(args, 1)
		if !ok {
			return types.Value{}, false, nil
		}
		if g == nil {
			return types.Null(types.Text()), true, nil
		}
		return types.StringValue(types.FormatGeomWKT(g)), true, nil
	case "st_asewkt":
		g, ok := generalGeom(args, 1)
		if !ok {
			return types.Value{}, false, nil
		}
		if g == nil {
			return types.Null(types.Text()), true, nil
		}
		return types.StringValue(types.FormatGeomEWKT(g)), true, nil
	case "st_asbinary", "st_aswkb", "st_asewkb":
		g, ok := generalGeom(args, 1)
		if !ok {
			return types.Value{}, false, nil
		}
		if g == nil {
			return types.Null(types.Blob()), true, nil
		}
		ewkb, err := types.EncodeEWKB(g)
		if err != nil {
			return types.Value{}, true, err
		}
		return types.BlobValue(ewkb), true, nil
	case "st_dimension":
		g, ok := generalGeom(args, 1)
		if !ok {
			return types.Value{}, false, nil
		}
		if g == nil {
			return types.Null(types.Int64()), true, nil
		}
		return types.IntValue(types.KindInt64, int64(geomTopoDimension(g))), true, nil
	case "st_isempty":
		g, ok := generalGeom(args, 1)
		if !ok {
			return types.Value{}, false, nil
		}
		if g == nil {
			return types.Null(types.Bool()), true, nil
		}
		return types.BoolValue(geomVertexCount(g) == 0), true, nil

	// --- S2: measurement ---
	case "st_distance":
		a, b, geodetic, ok, null := twoGeoms(args)
		if !ok {
			return types.Value{}, false, nil
		}
		if null {
			return types.Null(types.Float64()), true, nil
		}
		d, err := types.GeomDistance(a, b, geodetic)
		if err != nil {
			return types.Value{}, true, err
		}
		return types.Float64Value(d), true, nil
	case "st_dwithin":
		if len(args) != 3 {
			return types.Value{}, true, arityErr(lc)
		}
		a, b, geodetic, ok, null := twoGeoms(args[:2])
		if !ok {
			return types.Value{}, false, nil
		}
		if null || args[2].Null {
			return types.Null(types.Bool()), true, nil
		}
		r, err := asFloat(args[2])
		if err != nil {
			return types.Value{}, true, err
		}
		d, err := types.GeomDistance(a, b, geodetic)
		if err != nil {
			return types.Value{}, true, err
		}
		return types.BoolValue(d <= r), true, nil
	case "st_length":
		g, geodetic, ok, null := oneGeom(args)
		if !ok {
			return types.Value{}, false, nil
		}
		if null {
			return types.Null(types.Float64()), true, nil
		}
		return types.Float64Value(types.GeomLength(g, geodetic)), true, nil
	case "st_perimeter":
		g, geodetic, ok, null := oneGeom(args)
		if !ok {
			return types.Value{}, false, nil
		}
		if null {
			return types.Null(types.Float64()), true, nil
		}
		return types.Float64Value(types.GeomPerimeter(g, geodetic)), true, nil
	case "st_area":
		g, geodetic, ok, null := oneGeom(args)
		if !ok {
			return types.Value{}, false, nil
		}
		if null {
			return types.Null(types.Float64()), true, nil
		}
		return types.Float64Value(types.GeomArea(g, geodetic)), true, nil

	// --- S2: predicates ---
	case "st_intersects", "st_disjoint", "st_contains", "st_within",
		"st_covers", "st_coveredby", "st_touches", "st_crosses",
		"st_overlaps", "st_equals":
		a, b, _, ok, null := twoGeoms(args)
		if !ok {
			return types.Value{}, false, nil
		}
		if null {
			return types.Null(types.Bool()), true, nil
		}
		return types.BoolValue(spatialPredicate(lc, a, b)), true, nil

	// --- S2: derived geometries ---
	case "st_envelope":
		g, _, ok, null := oneGeom(args)
		if !ok {
			return types.Value{}, false, nil
		}
		if null || g == nil {
			return types.Null(args[0].Typ), true, nil
		}
		bb := types.GeomBBox(g)
		env := &types.Geom{Type: 3, SRID: g.SRID, Rings: []int{5}, Coords: []float64{
			bb[0], bb[1], bb[2], bb[1], bb[2], bb[3], bb[0], bb[3], bb[0], bb[1]}}
		return wrapGeom(args[0].Typ, env), true, nil
	case "st_centroid":
		g, _, ok, null := oneGeom(args)
		if !ok {
			return types.Value{}, false, nil
		}
		if null || g == nil {
			return types.Null(args[0].Typ), true, nil
		}
		cx, cy, cok := types.GeomCentroid(g)
		if !cok {
			return types.Null(args[0].Typ), true, nil
		}
		return wrapGeom(args[0].Typ, &types.Geom{Type: 1, SRID: g.SRID, Coords: []float64{cx, cy}}), true, nil
	case "st_pointn", "st_startpoint", "st_endpoint":
		if !types.IsGeneralSpatial(args[0].Typ.Kind) {
			return types.Value{}, false, nil
		}
		if args[0].Null || args[0].Geom == nil {
			return types.Null(args[0].Typ), true, nil
		}
		g := args[0].Geom
		if g.Type != 2 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.spatial", lc+" requires a LineString")
		}
		np := len(g.Coords) / 2
		idx := 0
		switch lc {
		case "st_startpoint":
			idx = 0
		case "st_endpoint":
			idx = np - 1
		default:
			if len(args) != 2 {
				return types.Value{}, true, arityErr(lc)
			}
			n, err := types.Coerce(args[1], types.Int64())
			if err != nil {
				return types.Value{}, true, err
			}
			idx = int(n.Int) - 1
		}
		if idx < 0 || idx >= np {
			return types.Null(args[0].Typ), true, nil
		}
		return wrapGeom(args[0].Typ, &types.Geom{Type: 1, SRID: g.SRID, Coords: []float64{g.Coords[idx*2], g.Coords[idx*2+1]}}), true, nil
	case "st_exteriorring":
		if !types.IsGeneralSpatial(args[0].Typ.Kind) {
			return types.Value{}, false, nil
		}
		if args[0].Null || args[0].Geom == nil {
			return types.Null(args[0].Typ), true, nil
		}
		g := args[0].Geom
		if g.Type != 3 || len(g.Rings) == 0 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.spatial", "st_exteriorring requires a Polygon")
		}
		ring := &types.Geom{Type: 2, SRID: g.SRID, Coords: append([]float64(nil), g.Coords[:g.Rings[0]*2]...)}
		return wrapGeom(args[0].Typ, ring), true, nil
	case "st_interiorringn":
		if len(args) != 2 || !types.IsGeneralSpatial(args[0].Typ.Kind) {
			return types.Value{}, false, nil
		}
		if args[0].Null || args[0].Geom == nil {
			return types.Null(args[0].Typ), true, nil
		}
		g := args[0].Geom
		if g.Type != 3 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.spatial", "st_interiorringn requires a Polygon")
		}
		n, err := types.Coerce(args[1], types.Int64())
		if err != nil {
			return types.Value{}, true, err
		}
		idx := int(n.Int) // 1-based, over the hole rings (ring 0 is exterior)
		if idx < 1 || idx >= len(g.Rings) {
			return types.Null(args[0].Typ), true, nil
		}
		off := 0
		for i := 0; i < idx; i++ {
			off += g.Rings[i] * 2
		}
		ring := &types.Geom{Type: 2, SRID: g.SRID, Coords: append([]float64(nil), g.Coords[off:off+g.Rings[idx]*2]...)}
		return wrapGeom(args[0].Typ, ring), true, nil
	case "st_numinteriorrings":
		g, _, ok, null := oneGeom(args)
		if !ok {
			return types.Value{}, false, nil
		}
		if null || g == nil {
			return types.Null(types.Int64()), true, nil
		}
		if g.Type != 3 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.spatial", "st_numinteriorrings requires a Polygon")
		}
		return types.IntValue(types.KindInt64, int64(len(g.Rings)-1)), true, nil
	case "st_boundary":
		g, _, ok, null := oneGeom(args)
		if !ok {
			return types.Value{}, false, nil
		}
		if null || g == nil {
			return types.Null(args[0].Typ), true, nil
		}
		b, err := geomBoundary(g)
		if err != nil {
			return types.Value{}, true, err
		}
		return wrapGeom(args[0].Typ, b), true, nil
	case "st_reverse":
		if !types.IsGeneralSpatial(args[0].Typ.Kind) {
			return types.Value{}, false, nil
		}
		if args[0].Null || args[0].Geom == nil {
			return types.Null(args[0].Typ), true, nil
		}
		return types.Value{Typ: args[0].Typ, Geom: reverseGeom(args[0].Geom)}, true, nil

	// --- S5: overlay / derived-geometry operators ---
	case "st_convexhull":
		g, _, ok, null := oneGeom(args)
		if !ok {
			return types.Value{}, false, nil
		}
		if null || g == nil {
			return types.Null(args[0].Typ), true, nil
		}
		return wrapGeom(args[0].Typ, types.GeomConvexHull(g)), true, nil
	case "st_simplify":
		if len(args) != 2 || !types.IsGeneralSpatial(args[0].Typ.Kind) {
			return types.Value{}, false, nil
		}
		if args[0].Null || args[0].Geom == nil {
			return types.Null(args[0].Typ), true, nil
		}
		tol, err := asFloat(args[1])
		if err != nil {
			return types.Value{}, true, err
		}
		return types.Value{Typ: args[0].Typ, Geom: types.GeomSimplify(args[0].Geom, tol)}, true, nil
	case "st_segmentize":
		if len(args) != 2 || !types.IsGeneralSpatial(args[0].Typ.Kind) {
			return types.Value{}, false, nil
		}
		if args[0].Null || args[0].Geom == nil {
			return types.Null(args[0].Typ), true, nil
		}
		maxLen, err := asFloat(args[1])
		if err != nil {
			return types.Value{}, true, err
		}
		return types.Value{Typ: args[0].Typ, Geom: types.GeomSegmentize(args[0].Geom, maxLen)}, true, nil
	case "st_buffer":
		if len(args) != 2 || !types.IsGeneralSpatial(args[0].Typ.Kind) {
			return types.Value{}, false, nil
		}
		if args[0].Null || args[0].Geom == nil {
			return types.Null(args[0].Typ), true, nil
		}
		r, err := asFloat(args[1])
		if err != nil {
			return types.Value{}, true, err
		}
		buf, err := types.GeomBuffer(args[0].Geom, r)
		if err != nil {
			return types.Value{}, true, err
		}
		return wrapGeom(args[0].Typ, buf), true, nil
	case "st_intersection", "st_union", "st_difference", "st_symdifference":
		a, b, _, ok, null := twoGeoms(args)
		if !ok {
			return types.Value{}, false, nil
		}
		if null {
			return types.Null(args[0].Typ), true, nil
		}
		var out *types.Geom
		var err error
		switch lc {
		case "st_intersection":
			out, err = types.GeomIntersection(a, b)
		case "st_union":
			out, err = types.GeomUnion(a, b)
		case "st_difference":
			out, err = types.GeomDifference(a, b)
		case "st_symdifference":
			var ab, ba *types.Geom
			ab, err = types.GeomDifference(a, b)
			if err == nil {
				ba, err = types.GeomDifference(b, a)
			}
			if err == nil {
				switch {
				case ab == nil:
					out = ba
				case ba == nil:
					out = ab
				default:
					// ab/ba are not guaranteed to be polygons (GeomDifference's
					// disjoint case returns a clone of the original operand
					// unchanged, whatever its type), so this must be wrapped as
					// a heterogeneous GeometryCollection, not hardcoded as
					// MultiPolygon — validateGeom only enforces uniform part
					// types for the Multi* kinds, not GeometryCollection.
					out = &types.Geom{Type: uint32(types.GeomSubGeometryCollection), SRID: a.SRID, Parts: []*types.Geom{ab, ba}}
				}
			}
		}
		if err != nil {
			return types.Value{}, true, err
		}
		if out == nil {
			return types.Null(args[0].Typ), true, nil
		}
		return wrapGeom(args[0].Typ, out), true, nil

	case "st_asgeojson":
		g, _, ok, null := oneGeom(args)
		if !ok {
			return types.Value{}, false, nil
		}
		if null || g == nil {
			return types.Null(types.Text()), true, nil
		}
		b, err := types.GeomToGeoJSON(g)
		if err != nil {
			return types.Value{}, true, err
		}
		return types.TextValue(string(b)), true, nil
	case "st_geomfromgeojson":
		if len(args) < 1 || len(args) > 2 {
			return types.Value{}, true, arityErr(lc)
		}
		txt, err := types.Coerce(args[0], types.Text())
		if err != nil {
			return types.Value{}, true, err
		}
		var srid uint32
		if len(args) == 2 && !args[1].Null {
			s, err := asSRID(args[1])
			if err != nil {
				return types.Value{}, true, err
			}
			srid = uint32(s)
		}
		g, err := types.ParseGeoJSON([]byte(txt.Str), srid)
		if err != nil {
			return types.Value{}, true, err
		}
		gt, err := types.GeometryType(0, uint16(srid))
		if err != nil {
			return types.Value{}, true, err
		}
		return types.Value{Typ: gt, Geom: g}, true, nil

	case "st_transform":
		if len(args) != 2 || !types.IsGeneralSpatial(args[0].Typ.Kind) {
			return types.Value{}, false, nil
		}
		if args[0].Null || args[0].Geom == nil {
			return types.Null(args[0].Typ), true, nil
		}
		s, err := asSRID(args[1])
		if err != nil {
			return types.Value{}, true, err
		}
		ng, err := types.TransformGeom(args[0].Geom, uint32(s))
		if err != nil {
			return types.Value{}, true, err
		}
		dt := args[0].Typ
		dt.Precision = s
		return types.Value{Typ: dt, Geom: ng}, true, nil

	default:
		return types.Value{}, false, nil
	}
}

// oneGeom resolves a single general-geometry argument; geodetic is true for
// GEOGRAPHY. ok=false hands off to the fixed-type handler.
func oneGeom(args []types.Value) (g *types.Geom, geodetic, ok, null bool) {
	if len(args) != 1 || !types.IsGeneralSpatial(args[0].Typ.Kind) {
		return nil, false, false, false
	}
	if args[0].Null {
		return nil, false, true, true
	}
	return args[0].Geom, args[0].Typ.Kind == types.KindGeography, true, false
}

// twoGeoms resolves two geometry arguments. The first must be a general
// GEOMETRY/GEOGRAPHY; the second is coerced to match (a fixed shape, a WKT
// string, or another general geometry). geodetic follows the first arg.
func twoGeoms(args []types.Value) (a, b *types.Geom, geodetic, ok, null bool) {
	if len(args) != 2 || !types.IsGeneralSpatial(args[0].Typ.Kind) {
		return nil, nil, false, false, false
	}
	geodetic = args[0].Typ.Kind == types.KindGeography
	if args[0].Null || args[1].Null {
		return nil, nil, geodetic, true, true
	}
	a = args[0].Geom
	// Coerce arg1 to a subtype-agnostic geometry of the same Kind + SRID as
	// arg0's column (dropping the column's declared subtype, which only
	// constrains what may be *stored*, not what a predicate may compare).
	dst := types.Type{Kind: args[0].Typ.Kind, Precision: args[0].Typ.Precision}
	cb, err := types.Coerce(args[1], dst)
	if err != nil || cb.Geom == nil {
		return nil, nil, geodetic, false, false
	}
	return a, cb.Geom, geodetic, true, false
}

func wrapGeom(base types.Type, g *types.Geom) types.Value {
	dt := base
	dt.Scale = uint16(g.Type)
	return types.Value{Typ: dt, Geom: g}
}

func reverseGeom(g *types.Geom) *types.Geom {
	c := g.Clone()
	var walk func(*types.Geom)
	walk = func(n *types.Geom) {
		switch n.Type {
		case 2:
			revCoords(n.Coords)
		case 3:
			off := 0
			for _, cnt := range n.Rings {
				revCoords(n.Coords[off : off+cnt*2])
				off += cnt * 2
			}
		}
		for _, p := range n.Parts {
			walk(p)
		}
	}
	walk(c)
	return c
}

func revCoords(c []float64) {
	n := len(c) / 2
	for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
		c[i*2], c[j*2] = c[j*2], c[i*2]
		c[i*2+1], c[j*2+1] = c[j*2+1], c[i*2+1]
	}
}

// spatialPredicate dispatches the named DE-9IM-family predicate. Native
// semantics (docs/design-spatial.md §8), not full OGC.
func spatialPredicate(name string, a, b *types.Geom) bool {
	switch name {
	case "st_intersects":
		return types.GeomsIntersect(a, b)
	case "st_disjoint":
		return !types.GeomsIntersect(a, b)
	case "st_equals":
		return types.GeomEquals(a, b)
	case "st_contains":
		return types.GeomContains(a, b, false)
	case "st_within":
		return types.GeomContains(b, a, false)
	case "st_covers":
		return types.GeomContains(a, b, true)
	case "st_coveredby":
		return types.GeomContains(b, a, true)
	case "st_touches":
		// covers (not strict contains) on both sides: touching requires
		// disjoint interiors, and a strict-contains check alone misses the
		// case where a and b are equal or one exactly covers the other with
		// no strict-interior sample point ever landing inside the shared
		// region (every vertex/edge-midpoint sample lies on the shared
		// boundary) — found via a two-identical-squares test, which
		// GeomContains(_, _, false) wrongly called "touching".
		return types.GeomsIntersect(a, b) &&
			!types.GeomContains(a, b, true) && !types.GeomContains(b, a, true)
	case "st_overlaps":
		return types.GeomsIntersect(a, b) &&
			!types.GeomContains(a, b, true) && !types.GeomContains(b, a, true) &&
			!types.GeomEquals(a, b)
	case "st_crosses":
		return types.GeomsIntersect(a, b) &&
			!types.GeomContains(a, b, true) && !types.GeomContains(b, a, true)
	default:
		return false
	}
}

func stFromText(args []types.Value, geog bool) (types.Value, bool, error) {
	if len(args) < 1 || len(args) > 2 {
		return types.Value{}, true, arityErr("st_geomfromtext")
	}
	txt, err := types.Coerce(args[0], types.Text())
	if err != nil {
		return types.Value{}, true, err
	}
	var srid uint16
	if geog {
		srid = types.SRIDWGS84
	}
	if len(args) == 2 && !args[1].Null {
		s, err := asSRID(args[1])
		if err != nil {
			return types.Value{}, true, err
		}
		srid = s
	}
	g, err := types.ParseGeneralWKT(txt.Str, uint32(srid))
	if err != nil {
		return types.Value{}, true, err
	}
	var dt types.Type
	if geog {
		dt, err = types.GeographyType(0, uint16(g.SRID))
	} else {
		dt, err = types.GeometryType(0, uint16(g.SRID))
	}
	if err != nil {
		return types.Value{}, true, err
	}
	out, err := types.Coerce(types.Value{Typ: types.Type{Kind: dt.Kind}, Geom: g}, dt)
	return out, true, err
}

// generalGeom returns the *Geom for a general GEOMETRY/GEOGRAPHY first
// argument, ok=false when it is not one (so a fixed-type handler runs
// instead). A NULL general-geometry value returns (nil, true).
func generalGeom(args []types.Value, want int) (*types.Geom, bool) {
	if len(args) != want || !types.IsGeneralSpatial(args[0].Typ.Kind) {
		return nil, false
	}
	if args[0].Null {
		return nil, true
	}
	return args[0].Geom, true
}

func asFloat(v types.Value) (float64, error) {
	f, err := types.Coerce(v, types.Float64())
	if err != nil {
		return 0, err
	}
	return f.Flt, nil
}

// asSRID coerces a function-argument SRID value to Int64 and validates it
// fits a u16, matching sql/parser.geoTypeArgs's own range check exactly so a
// GEOMETRY(Point, 99999) DDL and an ST_Point(x, y, 99999) call fail the same
// way instead of the latter silently wrapping to a different SRID.
func asSRID(v types.Value) (uint16, error) {
	s, err := types.Coerce(v, types.Int64())
	if err != nil {
		return 0, err
	}
	if s.Int < 0 || s.Int > 0xFFFF {
		return 0, nerr.New(nerr.InvalidArgument, "executor.eval", "SRID out of range")
	}
	return uint16(s.Int), nil
}

func setGeomSRID(g *types.Geom, srid uint32) {
	g.SRID = srid
	for _, p := range g.Parts {
		setGeomSRID(p, srid)
	}
}

func geomVertexCount(g *types.Geom) int {
	n := len(g.Coords) / 2
	for _, p := range g.Parts {
		n += geomVertexCount(p)
	}
	return n
}

func geomNumParts(g *types.Geom) int {
	switch g.Type {
	case 4, 5, 6, 7:
		return len(g.Parts)
	default:
		return 1
	}
}

func geomPartN(g *types.Geom, n int) *types.Geom {
	if n < 1 {
		return nil
	}
	switch g.Type {
	case 4, 5, 6, 7:
		if n > len(g.Parts) {
			return nil
		}
		return g.Parts[n-1]
	default:
		if n == 1 {
			return g
		}
		return nil
	}
}

func geomTopoDimension(g *types.Geom) int {
	switch g.Type {
	case 1, 4:
		return 0
	case 2, 5:
		return 1
	case 3, 6:
		return 2
	case 7:
		max := 0
		for _, p := range g.Parts {
			if d := geomTopoDimension(p); d > max {
				max = d
			}
		}
		return max
	default:
		return 0
	}
}

// geomBoundary is the OGC boundary: empty for a Point, the two endpoints for
// a LineString, and the ring set (as a MultiLineString) for a Polygon.
func geomBoundary(g *types.Geom) (*types.Geom, error) {
	switch g.Type {
	case 1: // Point
		return &types.Geom{Type: 7, SRID: g.SRID}, nil // empty GeometryCollection
	case 2: // LineString
		np := len(g.Coords) / 2
		if np < 2 {
			return nil, nerr.New(nerr.InvalidArgument, "executor.spatial", "malformed LineString")
		}
		return &types.Geom{Type: 4, SRID: g.SRID, Parts: []*types.Geom{
			{Type: 1, SRID: g.SRID, Coords: []float64{g.Coords[0], g.Coords[1]}},
			{Type: 1, SRID: g.SRID, Coords: []float64{g.Coords[(np-1)*2], g.Coords[(np-1)*2+1]}},
		}}, nil
	case 3: // Polygon
		var parts []*types.Geom
		off := 0
		for _, n := range g.Rings {
			parts = append(parts, &types.Geom{Type: 2, SRID: g.SRID, Coords: append([]float64(nil), g.Coords[off:off+n*2]...)})
			off += n * 2
		}
		return &types.Geom{Type: 5, SRID: g.SRID, Parts: parts}, nil
	default:
		return nil, nerr.New(nerr.InvalidArgument, "executor.spatial", "st_boundary is not defined for this geometry type")
	}
}

func subtypeToken(t uint32) string {
	name := types.GeomSubName(uint16(t))
	if name == "" {
		return "GEOMETRY"
	}
	return name
}
