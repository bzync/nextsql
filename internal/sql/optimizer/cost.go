package optimizer

import (
	"math"
	"strconv"
	"strings"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

// Selectivity in millionths (1_000_000 = 1.0). Integer math keeps plans deterministic.
type sel int64

const (
	selUnit    sel = 1_000_000
	selEq          = 100_000
	selRange       = 330_000
	selNeq         = 900_000
	selIsNull      = 50_000
	selDefault     = 250_000
	selOne         = selUnit
)

const (
	cpuTuple    int64 = 10
	cpuPred     int64 = 2
	cpuProject  int64 = 2
	cpuIndex    int64 = 20
	cpuHNSW     int64 = 40
	cpuDist     int64 = 8
	cpuRerank   int64 = 15
	cpuSort     int64 = 4
	cpuHash     int64 = 8
	cpuProbe    int64 = 2
	seqPage     int64 = 100
	randPage    int64 = 120
	defaultRows       = 1000
	tuplesPage        = 40
	selSearch         = 100_000
)

func satAdd(a, b int64) int64 {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

func satMul(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

func applySel(rows uint64, s sel) int64 {
	if s <= 0 || rows == 0 {
		return 0
	}
	if s > selUnit {
		s = selUnit
	}
	out := int64((rows * uint64(s)) / uint64(selUnit))
	if out < 1 && s > 0 {
		return 1
	}
	return out
}

func mulSel(a, b sel) sel {
	if a <= 0 || b <= 0 {
		return 0
	}
	return sel((int64(a) * int64(b)) / int64(selUnit))
}

func addSel(a, b sel) sel {
	// a + b - a*b
	prod := (int64(a) * int64(b)) / int64(selUnit)
	out := int64(a) + int64(b) - prod
	if out > int64(selUnit) {
		return selUnit
	}
	if out < 0 {
		return 0
	}
	return sel(out)
}

func predSel(e ast.Expr, tab *catalog.Table, st *catalog.TableStats) sel {
	if e == nil || predIsTrue(e) {
		return selOne
	}
	if predIsFalse(e) {
		return 0
	}
	switch x := e.(type) {
	case ast.Binary:
		if x.Op == "AND" {
			return mulSel(predSel(x.Left, tab, st), predSel(x.Right, tab, st))
		}
		if x.Op == "OR" {
			return addSel(predSel(x.Left, tab, st), predSel(x.Right, tab, st))
		}
		if s, ok := distanceCmpSel(x); ok {
			return s
		}
		col, val, op, ok := sargable(x, tab)
		if !ok {
			if pathConstCmp(x) {
				switch x.Op {
				case "=":
					if s, ok := pathIndexSel(x, tab, st); ok {
						return s
					}
					return selEq
				case "<>":
					return selNeq
				case "<", "<=", ">", ">=":
					return selRange
				}
			}
			if x.Op == "<>" {
				return selNeq
			}
			if eop, _, _, eok := exprCmp(x); eok {
				switch eop {
				case "=":
					return selEq
				default:
					return selRange
				}
			}
			return selDefault
		}
		return colSel(col, op, val, nil, tab, st)
	case ast.Between:
		if x.Not {
			return selNeq
		}
		id, ok := x.Expr.(ast.Ident)
		if !ok || tab == nil {
			return selRange
		}
		ord, ok := tab.ColIndex(id.Name)
		if !ok {
			return selRange
		}
		lo, ok1 := constValue(x.Low)
		hi, ok2 := constValue(x.High)
		if !ok1 || !ok2 {
			return selRange
		}
		return colSel(ord, "between", lo, &hi, tab, st)
	case ast.IsNull:
		if tab == nil {
			return selIsNull
		}
		id, ok := x.Expr.(ast.Ident)
		if !ok {
			return selIsNull
		}
		ord, ok := tab.ColIndex(id.Name)
		if !ok {
			return selIsNull
		}
		if cs, ok := statsCol(st, ord); ok && st != nil && st.Rows > 0 {
			s := sel((cs.Nulls * uint64(selUnit)) / st.Rows)
			if x.Not {
				return selUnit - s
			}
			if s <= 0 {
				return 1
			}
			return s
		}
		if x.Not {
			return selUnit - selIsNull
		}
		return selIsNull
	case ast.Call:
		switch strings.ToLower(x.Name) {
		case "st_intersects", "st_contains", "st_within", "st_covers", "st_coveredby":
			if len(x.Args) == 2 {
				for _, a := range x.Args {
					if g, ok := geoConstGeom(a); ok {
						if w, s, e, n, wrap, ok := types.GeoBBox(g); ok && !wrap {
							return boxAreaSel([4]float64{w, s, e, n})
						}
					}
				}
			}
			return selDefault
		case "st_dwithin":
			if len(x.Args) == 3 {
				if r, ok := constValue(x.Args[2]); ok {
					if meters, err := strconv.ParseFloat(r.String(), 64); err == nil && meters >= 0 {
						return radiusSel(meters)
					}
				}
			}
			return selDefault
		}
		switch types.CanonGeoName(x.Name) {
		case "dwithin":
			if len(x.Args) == 3 {
				if r, ok := constValue(x.Args[2]); ok {
					meters, err := strconv.ParseFloat(r.String(), 64)
					if err == nil && meters >= 0 {
						return radiusSel(meters)
					}
				}
			}
			return selDefault
		case "within":
			if len(x.Args) == 2 {
				if b, ok := constValue(x.Args[1]); ok {
					if b.Typ.Kind == types.KindBox {
						return boxAreaSel(b.Box)
					}
					if w, s, e, n, wrap, ok := types.GeoBBox(b); ok && !wrap {
						return boxAreaSel([4]float64{w, s, e, n})
					}
				}
			}
			return selIsNull
		case "covers":
			if len(x.Args) == 2 {
				if b, ok := constValue(x.Args[0]); ok {
					if b.Typ.Kind == types.KindBox {
						return boxAreaSel(b.Box)
					}
					if w, s, e, n, wrap, ok := types.GeoBBox(b); ok && !wrap {
						return boxAreaSel([4]float64{w, s, e, n})
					}
				}
			}
			return selIsNull
		default:
			return selDefault
		}
	case ast.Unary:
		if x.Op == "NOT" {
			s := predSel(x.Right, tab, st)
			if s >= selUnit {
				return 0
			}
			return selUnit - s
		}
		return selDefault
	default:
		return selDefault
	}
}

func distanceCmpSel(x ast.Binary) (sel, bool) {
	if x.Op != "<" && x.Op != "<=" {
		return 0, false
	}
	call, ok := x.Left.(ast.Call)
	if !ok || types.CanonGeoName(call.Name) != "distance" {
		return 0, false
	}
	r, ok := constValue(x.Right)
	if !ok {
		return 0, false
	}
	meters, err := strconv.ParseFloat(r.String(), 64)
	if err != nil || meters < 0 {
		return selDefault, true
	}
	return radiusSel(meters), true
}

func boxAreaSel(b [4]float64) sel {
	dlat := math.Abs(b[3] - b[1])
	dlon := b[2] - b[0]
	if dlon < 0 {
		dlon += 360
	}
	if dlon > 360 {
		dlon = 360
	}
	frac := (dlat / 180) * (dlon / 360)
	s := sel(uint64(frac * float64(selUnit)))
	if s < 1 {
		s = 1
	}
	if s > selRange {
		return selRange
	}
	return s
}

func radiusSel(meters float64) sel {
	frac := (math.Pi * meters * meters) / (4 * math.Pi * types.EarthRadiusM * types.EarthRadiusM)
	s := sel(uint64(frac * float64(selUnit)))
	if s < 1 {
		s = 1
	}
	if s > selRange {
		return selRange
	}
	return s
}

func statsCol(st *catalog.TableStats, ord int) (catalog.ColumnStats, bool) {
	if st == nil {
		return catalog.ColumnStats{}, false
	}
	return st.Column(ord)
}

func colSel(ord int, op string, val types.Value, hi *types.Value, tab *catalog.Table, st *catalog.TableStats) sel {
	cs, ok := statsCol(st, ord)
	rows := uint64(defaultRows)
	if st != nil && st.Rows > 0 {
		rows = st.Rows
	}
	if !ok {
		switch op {
		case "=":
			return selEq
		case "<>":
			return selNeq
		default:
			return selRange
		}
	}
	if op == "=" {
		if s, ok := mcvSel(cs, val, rows); ok {
			return s
		}
		if cs.NDV > 0 {
			mcvRows := uint64(0)
			for _, m := range cs.MCV {
				mcvRows += m.Freq
			}
			rest := rows - cs.Nulls
			if rest > mcvRows {
				rest -= mcvRows
			} else {
				rest = 0
			}
			ndv := cs.NDV
			if uint64(len(cs.MCV)) < ndv {
				ndv -= uint64(len(cs.MCV))
			}
			if ndv == 0 {
				ndv = 1
			}
			s := sel((rest * uint64(selUnit)) / (rows * ndv))
			if s <= 0 {
				return 1
			}
			return s
		}
		return selEq
	}
	if op == "<>" {
		eq := colSel(ord, "=", val, nil, tab, st)
		if eq >= selUnit {
			return 0
		}
		return selUnit - eq
	}
	if !cs.HasMinMax || val.Null {
		return selRange
	}
	if op == "between" && hi != nil {
		return betweenSel(cs, val, *hi, rows)
	}
	return rangeSel(cs, op, val, rows)
}

func mcvSel(cs catalog.ColumnStats, val types.Value, rows uint64) (sel, bool) {
	if rows == 0 || val.Null {
		return 0, false
	}
	for _, m := range cs.MCV {
		if eqVal(m.Value, val) {
			s := sel((m.Freq * uint64(selUnit)) / rows)
			if s <= 0 {
				return 1, true
			}
			return s, true
		}
	}
	return 0, false
}

func eqVal(a, b types.Value) bool {
	if a.Null || b.Null {
		return a.Null && b.Null
	}
	c, err := a.Cmp(b)
	return err == nil && c == 0
}

func betweenSel(cs catalog.ColumnStats, lo, hi types.Value, rows uint64) sel {
	if len(cs.Histogram) == 0 {
		return selRange
	}
	var n uint64
	for _, b := range cs.Histogram {
		if cmpGE(b.Upper, lo) && cmpLE(b.Lower, hi) {
			n += b.Count
		}
	}
	if rows == 0 {
		return selRange
	}
	s := sel((n * uint64(selUnit)) / rows)
	if s <= 0 {
		return 1
	}
	return s
}

func rangeSel(cs catalog.ColumnStats, op string, v types.Value, rows uint64) sel {
	if len(cs.Histogram) == 0 {
		return selRange
	}
	var n uint64
	for _, b := range cs.Histogram {
		switch op {
		case "<":
			if cmpLT(b.Upper, v) {
				n += b.Count
			} else if cmpLT(b.Lower, v) {
				n += b.Count / 2
			}
		case "<=":
			if cmpLE(b.Upper, v) {
				n += b.Count
			} else if cmpLE(b.Lower, v) {
				n += b.Count / 2
			}
		case ">":
			if cmpGT(b.Lower, v) {
				n += b.Count
			} else if cmpGT(b.Upper, v) {
				n += b.Count / 2
			}
		case ">=":
			if cmpGE(b.Lower, v) {
				n += b.Count
			} else if cmpGE(b.Upper, v) {
				n += b.Count / 2
			}
		}
	}
	if rows == 0 {
		return selRange
	}
	s := sel((n * uint64(selUnit)) / rows)
	if s <= 0 {
		return 1
	}
	return s
}

func cmpLT(a, b types.Value) bool { c, err := a.Cmp(b); return err == nil && c < 0 }
func cmpLE(a, b types.Value) bool { c, err := a.Cmp(b); return err == nil && c <= 0 }
func cmpGT(a, b types.Value) bool { c, err := a.Cmp(b); return err == nil && c > 0 }
func cmpGE(a, b types.Value) bool { c, err := a.Cmp(b); return err == nil && c >= 0 }

func pathConstCmp(x ast.Binary) bool {
	if _, ok := x.Left.(ast.Path); ok {
		if _, isC := constValue(x.Right); isC {
			return true
		}
	}
	if _, ok := x.Right.(ast.Path); ok {
		if _, isC := constValue(x.Left); isC {
			return true
		}
	}
	return false
}

func sargable(x ast.Binary, tab *catalog.Table) (ord int, val types.Value, op string, ok bool) {
	if tab == nil {
		return 0, types.Value{}, "", false
	}
	op = x.Op
	if id, isID := x.Left.(ast.Ident); isID {
		if v, isC := constValue(x.Right); isC {
			ord, ok = tab.ColIndex(id.Name)
			return ord, v, op, ok
		}
	}
	if id, isID := x.Right.(ast.Ident); isID {
		if v, isC := constValue(x.Left); isC {
			ord, ok = tab.ColIndex(id.Name)
			if !ok {
				return 0, types.Value{}, "", false
			}
			return ord, v, flipOp(op), true
		}
	}
	return 0, types.Value{}, "", false
}

func flipOp(op string) string {
	switch op {
	case "<":
		return ">"
	case ">":
		return "<"
	case "<=":
		return ">="
	case ">=":
		return "<="
	default:
		return op
	}
}

func constValue(e ast.Expr) (types.Value, bool) {
	e = foldExpr(e)
	lit, ok := e.(ast.Literal)
	if !ok {
		return types.Value{}, false
	}
	return lit.Value, true
}

func tableRows(st *catalog.TableStats) uint64 {
	if st == nil || st.Rows == 0 {
		if st != nil && st.Rows == 0 {
			return 0
		}
		return defaultRows
	}
	return st.Rows
}

func seqCost(rows uint64, s sel) (cost, out int64) {
	if rows == 0 {
		return 0, 0
	}
	pages := int64((rows + tuplesPage - 1) / tuplesPage)
	if pages < 1 {
		pages = 1
	}
	out = applySel(rows, s)
	cost = pages*seqPage + int64(rows)*cpuTuple + int64(rows)*cpuPred
	return cost, out
}

func idxCost(rows uint64, idxSel, resSel sel, uniqueEq bool, corr float64) (cost, out int64) {
	idxRows := applySel(rows, idxSel)
	if uniqueEq {
		idxRows = 1
	}
	if idxRows < 1 && idxSel > 0 && rows > 0 {
		idxRows = 1
	}
	if corr > 1 {
		corr = 1
	}
	if corr < -1 {
		corr = -1
	}
	abs := math.Abs(corr)
	io := int64(float64(idxRows) * (float64(randPage)*(1-abs) + float64(seqPage)*abs))
	cost = idxRows*cpuIndex + io
	out = applySel(uint64(idxRows), resSel)
	if uniqueEq {
		out = 1
	}
	return cost, out
}

// clusteredRangeCost models a PK range as a seek followed by sequential leaf
// reads. Unlike a secondary index, it does not perform one random heap lookup
// per matching row because the clustered leaf already contains the row.
func clusteredRangeCost(rows uint64, idxSel, resSel sel) (cost, out int64) {
	idxRows := applySel(rows, idxSel)
	if idxRows < 1 && idxSel > 0 && rows > 0 {
		idxRows = 1
	}
	pages := (idxRows + tuplesPage - 1) / tuplesPage
	if pages < 1 && idxRows > 0 {
		pages = 1
	}
	cost = int64(pages)*seqPage + int64(idxRows)*(cpuTuple+cpuPred)
	out = applySel(uint64(idxRows), resSel)
	return cost, out
}

func dimScale(dim int) int64 {
	if dim < 1 {
		dim = 32
	}
	return int64((dim + 7) / 8)
}

func log2i(n uint64) int64 {
	if n <= 1 {
		return 1
	}
	var bits int64
	for n > 1 {
		n >>= 1
		bits++
	}
	return bits
}

func hnswCost(rows uint64, k int64, dim int) int64 {
	if rows == 0 {
		return 0
	}
	ef := k
	if ef < 64 {
		ef = 64
	}
	visited := ef * log2i(rows)
	if uint64(visited) > rows {
		visited = int64(rows)
	}
	return visited*cpuHNSW + visited*cpuDist*dimScale(dim)
}

func flatANNCost(rows int64, dim int) int64 {
	if rows <= 0 {
		return 0
	}
	return rows * cpuDist * dimScale(dim)
}

func rerankCost(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return n * cpuRerank
}

func overfetch(k int64, residual sel, rows uint64) int64 {
	if k <= 0 {
		if rows == 0 {
			return 10
		}
		return int64(rows)
	}
	need := k
	if residual > 0 && residual < selUnit {
		need = (k*int64(selUnit) + int64(residual) - 1) / int64(residual)
	}
	capN := k * 16
	if capN < 64 {
		capN = 64
	}
	if need > capN {
		need = capN
	}
	if rows > 0 && uint64(need) > rows {
		need = int64(rows)
	}
	if need < k {
		need = k
	}
	return need
}

func vecDim(tab *catalog.Table, col int, st *catalog.TableStats) int {
	if st != nil {
		if vs, ok := st.Vector(col); ok && vs.Dim > 0 {
			return int(vs.Dim)
		}
	}
	if tab != nil && col >= 0 && col < len(tab.Columns) {
		if d := tab.Columns[col].Type.Precision; d > 0 {
			return int(d)
		}
	}
	return 32
}

func pathIndexSel(x ast.Binary, tab *catalog.Table, st *catalog.TableStats) (sel, bool) {
	if tab == nil || st == nil {
		return 0, false
	}
	for _, idx := range tab.Indexes {
		if len(idx.Path) == 0 || len(idx.Columns) == 0 {
			continue
		}
		if !pathExprMatch(x.Left, tab, idx.Columns[0], idx.Path) && !pathExprMatch(x.Right, tab, idx.Columns[0], idx.Path) {
			continue
		}
		if is, ok := st.Index(idx.Name); ok && is.Selectivity > 0 && is.Selectivity <= 1 {
			s := sel(uint64(is.Selectivity * float64(selUnit)))
			if s < 1 {
				s = 1
			}
			return s, true
		}
	}
	return 0, false
}

func searchSel(st *catalog.TableStats, idxName string) sel {
	if st != nil && idxName != "" {
		if is, ok := st.Index(idxName); ok && is.Selectivity > 0 && is.Selectivity <= 1 {
			s := sel(uint64(is.Selectivity * float64(selUnit)))
			if s < 1 {
				return 1
			}
			return s
		}
	}
	return selSearch
}
