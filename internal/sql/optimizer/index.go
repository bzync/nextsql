package optimizer

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
)

type accessCand struct {
	plan  planner.Logical
	node  *Node
	cost  int64
	rank  int
	iname string
}

func zeroCand() accessCand {
	return accessCand{}
}

func finishIndexScan(scan planner.IndexScan, idx catalog.Index, tab *catalog.Table, needed []int, cost, out int64, detail, name string, rank int) accessCand {
	if idx.Predicate != nil {
		scan.Residual = andAll(dropImplied(scan.Residual, idx.Predicate))
	}
	cover := coveringCols(idx, tab, needed, scan.Residual)
	covering := idx.Covers(cover, tab)
	if covering {
		scan.IndexOnly = true
		detail += " covering"
		if cost > seqPage {
			cost -= seqPage
		}
	}
	node := &Node{Op: "IndexScan", Detail: detail, EstRows: out, EstCost: cost, Index: name}
	return accessCand{plan: scan, node: node, cost: cost, rank: rank, iname: name}
}

func exprIndexAlt(tab *catalog.Table, idx catalog.Index, pred ast.Expr, needed []int, st *catalog.TableStats, rows uint64) (accessCand, bool) {
	if !idx.HasExpr() || len(idx.Columns) == 0 || idx.KeyIsExpr(0) == false {
		return zeroCand(), false
	}
	lead, used, ok := matchExprRange(pred, idx.Exprs[0])
	if !ok {
		return zeroCand(), false
	}
	var low, high []types.Value
	lowIncl, highIncl := true, true
	eqPrefix := 0
	usedExprs := []ast.Expr{used}
	if lead.eq && lead.low != nil {
		v, err := types.Coerce(*lead.low, idx.KeyType(tab, 0))
		if err != nil {
			return zeroCand(), false
		}
		low = []types.Value{v}
		high = []types.Value{v}
		eqPrefix = 1
		ranges := extractRanges(pred, tab)
		for i := 1; i < len(idx.Columns); i++ {
			if idx.KeyIsExpr(i) {
				cr, u, ok := matchExprRange(pred, idx.Exprs[i])
				if !ok || !cr.eq || cr.low == nil {
					break
				}
				cv, err := types.Coerce(*cr.low, idx.KeyType(tab, i))
				if err != nil {
					break
				}
				low = append(low, cv)
				high = append(high, cv)
				usedExprs = append(usedExprs, u)
				eqPrefix++
				continue
			}
			cr, ok := ranges[idx.Columns[i]]
			if !ok || !cr.eq || cr.low == nil {
				break
			}
			cv, err := coerceBound(*cr.low, tab, idx.Columns[i])
			if err != nil {
				break
			}
			low = append(low, cv)
			high = append(high, cv)
			eqPrefix++
		}
	} else if lead.isNull {
		nv := types.Null(idx.KeyType(tab, 0))
		low = []types.Value{nv}
		high = []types.Value{nv}
	} else {
		if lead.low != nil && !lead.unboundedLow {
			v, err := types.Coerce(*lead.low, idx.KeyType(tab, 0))
			if err != nil {
				return zeroCand(), false
			}
			low = []types.Value{v}
			lowIncl = lead.lowIncl
		}
		if lead.high != nil && !lead.unboundedHigh {
			v, err := types.Coerce(*lead.high, idx.KeyType(tab, 0))
			if err != nil {
				return zeroCand(), false
			}
			high = []types.Value{v}
			highIncl = lead.highIncl
		}
		if len(low) == 0 && len(high) == 0 {
			return zeroCand(), false
		}
	}
	res := pred
	for _, u := range usedExprs {
		res = andAll(dropConjunct(res, u))
	}
	uniqueEq := idx.Unique && eqPrefix == len(idx.Columns)
	idxSel := predSel(used, tab, st)
	if uniqueEq {
		idxSel = sel((uint64(selUnit)) / maxU64(rows, 1))
		if idxSel <= 0 {
			idxSel = 1
		}
	}
	resSel := predSel(res, tab, st)
	cost, out := idxCost(rows, idxSel, resSel, uniqueEq, 0)
	scan := planner.IndexScan{
		Table:     tab,
		IndexName: idx.Name,
		Unique:    idx.Unique,
		Columns:   append([]int(nil), idx.Columns...),
		Low:       low,
		High:      high,
		LowIncl:   lowIncl,
		HighIncl:  highIncl,
		Residual:  res,
		Needed:    needed,
	}
	detail := tableName(tab) + " " + idx.Name + " expr"
	if res != nil {
		detail += " residual=" + formatExpr(res)
	}
	return finishIndexScan(scan, idx, tab, needed, cost, out, detail, idx.Name, 1), true
}

func matchExprRange(pred ast.Expr, expr ast.Expr) (colRange, ast.Expr, bool) {
	for _, c := range conjuncts(pred) {
		if r, ok := oneExprRange(c, expr); ok {
			return r, c, true
		}
	}
	return colRange{}, nil, false
}

func oneExprRange(e ast.Expr, expr ast.Expr) (colRange, bool) {
	switch x := e.(type) {
	case ast.Binary:
		op, side, val, ok := exprCmp(x)
		if !ok || !catalog.ExprEqual(side, expr) {
			return colRange{}, false
		}
		r := colRange{ord: -1, unboundedLow: true, unboundedHigh: true}
		v := val
		switch op {
		case "=":
			r.eq = true
			r.low, r.high = &v, &v
			r.lowIncl, r.highIncl = true, true
			r.unboundedLow, r.unboundedHigh = false, false
		case "<":
			r.high = &v
			r.highIncl = false
			r.unboundedHigh = false
		case "<=":
			r.high = &v
			r.highIncl = true
			r.unboundedHigh = false
		case ">":
			r.low = &v
			r.lowIncl = false
			r.unboundedLow = false
		case ">=":
			r.low = &v
			r.lowIncl = true
			r.unboundedLow = false
		default:
			return colRange{}, false
		}
		return r, true
	case ast.Between:
		if x.Not || !catalog.ExprEqual(x.Expr, expr) {
			return colRange{}, false
		}
		lo, ok1 := constValue(x.Low)
		hi, ok2 := constValue(x.High)
		if !ok1 || !ok2 {
			return colRange{}, false
		}
		return colRange{ord: -1, low: &lo, high: &hi, lowIncl: true, highIncl: true}, true
	case ast.IsNull:
		if x.Not || !catalog.ExprEqual(x.Expr, expr) {
			return colRange{}, false
		}
		return colRange{ord: -1, isNull: true, eq: true}, true
	}
	return colRange{}, false
}

func coveringCols(idx catalog.Index, tab *catalog.Table, needed []int, residual ast.Expr) []int {
	cover := unionNeeded(needed, predColumns(residual, tab))
	if idx.Predicate == nil {
		return cover
	}
	skip := map[int]struct{}{}
	for ord := range impliedConsts(idx.Predicate, tab) {
		skip[ord] = struct{}{}
	}
	if len(skip) == 0 {
		return cover
	}
	var out []int
	for _, ord := range cover {
		if _, ok := skip[ord]; ok {
			continue
		}
		out = append(out, ord)
	}
	return out
}

func impliedConsts(pred ast.Expr, tab *catalog.Table) map[int]types.Value {
	out := map[int]types.Value{}
	if pred == nil || tab == nil {
		return out
	}
	for _, c := range conjuncts(pred) {
		b, ok := c.(ast.Binary)
		if !ok || b.Op != "=" {
			continue
		}
		id, ok := b.Left.(ast.Ident)
		lit, isLit := b.Right.(ast.Literal)
		if !ok {
			id, ok = b.Right.(ast.Ident)
			lit, isLit = b.Left.(ast.Literal)
		}
		if !ok || !isLit {
			continue
		}
		ord, found := tab.ColIndex(id.Name)
		if found {
			out[ord] = lit.Value
		}
	}
	return out
}

func coveringPartialAlt(tab *catalog.Table, idx catalog.Index, pred ast.Expr, needed []int, st *catalog.TableStats, rows uint64) (accessCand, bool) {
	if idx.Predicate == nil || idx.Spatial || idx.Fulltext || idx.Vector {
		return zeroCand(), false
	}
	res := pred
	if impliesPredicate(pred, idx.Predicate, tab) {
		res = andAll(dropImplied(pred, idx.Predicate))
	}
	cover := coveringCols(idx, tab, needed, res)
	if !idx.Covers(cover, tab) {
		return zeroCand(), false
	}
	idxSel := predSel(idx.Predicate, tab, st)
	resSel := predSel(res, tab, st)
	cost, out := idxCost(rows, idxSel, resSel, false, 0)
	scan := planner.IndexScan{
		Table:     tab,
		IndexName: idx.Name,
		Unique:    idx.Unique,
		Columns:   append([]int(nil), idx.Columns...),
		LowIncl:   true,
		HighIncl:  true,
		Residual:  res,
		Needed:    needed,
	}
	detail := tableName(tab) + " " + idx.Name + " partial"
	if res != nil {
		detail += " residual=" + formatExpr(res)
	}
	return finishIndexScan(scan, idx, tab, needed, cost, out, detail, idx.Name, 1), true
}

func dropImplied(pred, partial ast.Expr) []ast.Expr {
	var keep []ast.Expr
	for _, c := range conjuncts(pred) {
		if impliesPredicate(c, partial, nil) || catalog.ExprEqual(c, partial) {
			continue
		}
		implied := false
		for _, p := range conjuncts(partial) {
			if catalog.ExprEqual(c, p) {
				implied = true
				break
			}
		}
		if !implied {
			keep = append(keep, c)
		}
	}
	return keep
}
