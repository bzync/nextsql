package binder

import (
	"strings"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
)

func extractSubjoins(where ast.Expr, outer *catalog.Table, outerAlias string, lookup Lookup, ctes map[string]*CTE) (ast.Expr, []BoundSubjoin, error) {
	if where == nil || outer == nil {
		return where, nil, nil
	}
	var kept []ast.Expr
	var joins []BoundSubjoin
	for _, c := range conjunctsOf(where) {
		sj, ok, err := flattenSubqueryConjunct(c, outer, outerAlias, lookup, ctes)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			joins = append(joins, sj)
			continue
		}
		kept = append(kept, c)
	}
	return andOf(kept), joins, nil
}

func flattenSubqueryConjunct(c ast.Expr, outer *catalog.Table, outerAlias string, lookup Lookup, ctes map[string]*CTE) (BoundSubjoin, bool, error) {
	switch x := c.(type) {
	case ast.ExistsSubquery:
		return flattenExists(x.Query, false, outer, outerAlias, lookup, ctes)
	case ast.Unary:
		if x.Op == "NOT" {
			if inner, ok := x.Right.(ast.ExistsSubquery); ok {
				return flattenExists(inner.Query, true, outer, outerAlias, lookup, ctes)
			}
		}
	case ast.InSubquery:
		return flattenIn(x, outer, outerAlias, lookup, ctes)
	}
	return BoundSubjoin{}, false, nil
}

func flattenExists(query ast.Stmt, anti bool, outer *catalog.Table, outerAlias string, lookup Lookup, ctes map[string]*CTE) (BoundSubjoin, bool, error) {
	sel, ok := simpleSubquery(query)
	if !ok {
		return BoundSubjoin{}, false, nil
	}
	if ctes[sel.Table] != nil {
		return BoundSubjoin{}, false, nil
	}
	tab, err := mustTable(lookup, sel.Table)
	if err != nil {
		return BoundSubjoin{}, false, err
	}
	if hasOuterRef(sel.List, tab, aliasOr(sel.Alias, sel.Table), outer, outerAlias) {
		return BoundSubjoin{}, false, nil
	}
	innerWhere, pred, ok := splitCorrPred(sel.Where, tab, aliasOr(sel.Alias, sel.Table), outer, outerAlias)
	if !ok {
		return BoundSubjoin{}, false, nil
	}
	return bindSubjoin(sel, innerWhere, pred, tab, anti, outer, lookup)
}

func flattenIn(in ast.InSubquery, outer *catalog.Table, outerAlias string, lookup Lookup, ctes map[string]*CTE) (BoundSubjoin, bool, error) {
	sel, ok := simpleSubquery(in.Query)
	if !ok {
		return BoundSubjoin{}, false, nil
	}
	if ctes[sel.Table] != nil {
		return BoundSubjoin{}, false, nil
	}
	tab, err := mustTable(lookup, sel.Table)
	if err != nil {
		return BoundSubjoin{}, false, err
	}
	col, ok := inOutputColumn(sel, tab)
	if !ok {
		return BoundSubjoin{}, false, nil
	}
	if col.ClientEncrypted() {
		return BoundSubjoin{}, false, nerr.New(nerr.InvalidArgument, "sql.binder", "ENCRYPTED CLIENT column cannot be used in a subquery predicate")
	}
	if in.Not && !col.NotNull {
		return BoundSubjoin{}, false, nil
	}
	if hasOuterRef(sel.List, tab, aliasOr(sel.Alias, sel.Table), outer, outerAlias) {
		return BoundSubjoin{}, false, nil
	}
	innerWhere, pred, ok := splitCorrPred(sel.Where, tab, aliasOr(sel.Alias, sel.Table), outer, outerAlias)
	if !ok {
		return BoundSubjoin{}, false, nil
	}
	eq := ast.Binary{
		Op:    "=",
		Left:  in.Expr,
		Right: ast.Ident{Name: aliasOr(sel.Alias, sel.Table) + "." + col.Name},
	}
	pred = andOf([]ast.Expr{pred, eq})
	return bindSubjoin(sel, innerWhere, pred, tab, in.Not, outer, lookup)
}

func bindSubjoin(sel ast.Select, innerWhere, pred ast.Expr, tab *catalog.Table, anti bool, outer *catalog.Table, lookup Lookup) (BoundSubjoin, bool, error) {
	innerAST := sel
	innerAST.Where = innerWhere
	innerAST.Order = nil
	innerAST.Star = true
	innerAST.List = nil
	innerAST.Distinct = false
	bound, err := Bind(innerAST, lookup, 0)
	if err != nil {
		return BoundSubjoin{}, false, err
	}
	kind := ast.JoinSemi
	if anti {
		kind = ast.JoinAnti
	}
	innerAlias := aliasOr(sel.Alias, sel.Table)
	schema := mergeTables(outer.Clone(), qualifyTable(tab, innerAlias, true))
	return BoundSubjoin{Kind: kind, Right: bound, Pred: pred, Schema: schema}, true, nil
}

func simpleSubquery(query ast.Stmt) (ast.Select, bool) {
	sel, ok := query.(ast.Select)
	if !ok {
		return ast.Select{}, false
	}
	if sel.FromQuery != nil || len(sel.Joins) > 0 || sel.Distinct || len(sel.Group) > 0 || sel.Having != nil {
		return ast.Select{}, false
	}
	if sel.Limit != nil || sel.Offset != nil || len(sel.SearchCols) > 0 || sel.NearestCol != "" || sel.Nearest2Col != "" {
		return ast.Select{}, false
	}
	if sel.Table == "" {
		return ast.Select{}, false
	}
	for _, item := range sel.List {
		if containsWindow(item.Expr) {
			return ast.Select{}, false
		}
	}
	return sel, true
}

func inOutputColumn(sel ast.Select, tab *catalog.Table) (catalog.Column, bool) {
	if sel.Star || len(sel.List) != 1 {
		return catalog.Column{}, false
	}
	name := columnRefName(sel.List[0].Expr, aliasOr(sel.Alias, sel.Table), tab.Name)
	if name == "" {
		return catalog.Column{}, false
	}
	i, ok := tab.ColIndex(name)
	if !ok {
		return catalog.Column{}, false
	}
	return tab.Columns[i], true
}

func columnRefName(e ast.Expr, alias, table string) string {
	switch x := e.(type) {
	case ast.Ident:
		if i := strings.LastIndex(x.Name, "."); i >= 0 {
			qual, col := x.Name[:i], x.Name[i+1:]
			if qual == alias || qual == table {
				return col
			}
			return ""
		}
		return x.Name
	case ast.Path:
		if len(x.Parts) == 2 && (x.Parts[0] == alias || x.Parts[0] == table) {
			return x.Parts[1]
		}
		if len(x.Parts) == 1 {
			return x.Parts[0]
		}
	}
	return ""
}

func splitCorrPred(where ast.Expr, inner *catalog.Table, innerAlias string, outer *catalog.Table, outerAlias string) (innerWhere, joinPred ast.Expr, ok bool) {
	var inners, joins []ast.Expr
	for _, c := range conjunctsOf(where) {
		scope, rewritten, good := classifyCorr(c, inner, innerAlias, outer, outerAlias)
		if !good {
			return nil, nil, false
		}
		switch scope {
		case corrInner:
			inners = append(inners, c)
		case corrOuter, corrMixed:
			joins = append(joins, rewritten)
		default:
			return nil, nil, false
		}
	}
	return andOf(inners), andOf(joins), true
}

const (
	corrInner = iota
	corrOuter
	corrMixed
	corrUnknown
)

func classifyCorr(e ast.Expr, inner *catalog.Table, innerAlias string, outer *catalog.Table, outerAlias string) (int, ast.Expr, bool) {
	rewritten, sides, ok := rewriteCorr(e, inner, innerAlias, outer, outerAlias)
	if !ok {
		return corrUnknown, nil, false
	}
	switch {
	case sides[0] && sides[1]:
		return corrMixed, rewritten, true
	case sides[1]:
		return corrInner, rewritten, true
	case sides[0]:
		return corrOuter, rewritten, true
	default:
		return corrInner, rewritten, true
	}
}

func rewriteCorr(e ast.Expr, inner *catalog.Table, innerAlias string, outer *catalog.Table, outerAlias string) (ast.Expr, [2]bool, bool) {
	if e == nil {
		return nil, [2]bool{}, true
	}
	switch x := e.(type) {
	case ast.Ident, ast.Path:
		name, innerSide, ok := resolveCorrRef(e, inner, innerAlias, outer, outerAlias)
		if !ok {
			return nil, [2]bool{}, false
		}
		var sides [2]bool
		if innerSide {
			sides[1] = true
		} else {
			sides[0] = true
		}
		return ast.Ident{Name: name}, sides, true
	case ast.Literal, ast.Param, ast.VectorLit:
		return e, [2]bool{}, true
	case ast.Unary:
		r, s, ok := rewriteCorr(x.Right, inner, innerAlias, outer, outerAlias)
		if !ok {
			return nil, [2]bool{}, false
		}
		x.Right = r
		return x, s, true
	case ast.Binary:
		l, ls, ok := rewriteCorr(x.Left, inner, innerAlias, outer, outerAlias)
		if !ok {
			return nil, [2]bool{}, false
		}
		r, rs, ok := rewriteCorr(x.Right, inner, innerAlias, outer, outerAlias)
		if !ok {
			return nil, [2]bool{}, false
		}
		x.Left, x.Right = l, r
		return x, [2]bool{ls[0] || rs[0], ls[1] || rs[1]}, true
	case ast.Between:
		v, vs, ok := rewriteCorr(x.Expr, inner, innerAlias, outer, outerAlias)
		if !ok {
			return nil, [2]bool{}, false
		}
		lo, ls, ok := rewriteCorr(x.Low, inner, innerAlias, outer, outerAlias)
		if !ok {
			return nil, [2]bool{}, false
		}
		hi, hs, ok := rewriteCorr(x.High, inner, innerAlias, outer, outerAlias)
		if !ok {
			return nil, [2]bool{}, false
		}
		x.Expr, x.Low, x.High = v, lo, hi
		return x, [2]bool{vs[0] || ls[0] || hs[0], vs[1] || ls[1] || hs[1]}, true
	case ast.IsNull:
		r, s, ok := rewriteCorr(x.Expr, inner, innerAlias, outer, outerAlias)
		if !ok {
			return nil, [2]bool{}, false
		}
		x.Expr = r
		return x, s, true
	case ast.Call:
		var sides [2]bool
		args := make([]ast.Expr, len(x.Args))
		for i, a := range x.Args {
			r, s, ok := rewriteCorr(a, inner, innerAlias, outer, outerAlias)
			if !ok {
				return nil, [2]bool{}, false
			}
			args[i] = r
			sides[0] = sides[0] || s[0]
			sides[1] = sides[1] || s[1]
		}
		x.Args = args
		return x, sides, true
	case ast.Window:
		return nil, [2]bool{}, false
	case ast.Case:
		return nil, [2]bool{}, false
	case ast.ScalarSubquery, ast.InSubquery, ast.ExistsSubquery:
		return nil, [2]bool{}, false
	default:
		return nil, [2]bool{}, false
	}
}

func resolveCorrRef(e ast.Expr, inner *catalog.Table, innerAlias string, outer *catalog.Table, outerAlias string) (string, bool, bool) {
	switch x := e.(type) {
	case ast.Ident:
		if i := strings.LastIndex(x.Name, "."); i >= 0 {
			return resolveCorrQual(x.Name[:i], x.Name[i+1:], inner, innerAlias, outer, outerAlias)
		}
		inInner := colOn(inner, x.Name)
		if inInner {
			return innerAlias + "." + x.Name, true, true
		}
		if name, ok := outerColName(outer, x.Name); ok {
			return name, false, true
		}
	case ast.Path:
		if len(x.Parts) == 2 {
			return resolveCorrQual(x.Parts[0], x.Parts[1], inner, innerAlias, outer, outerAlias)
		}
	}
	return "", false, false
}

func resolveCorrQual(qual, col string, inner *catalog.Table, innerAlias string, outer *catalog.Table, outerAlias string) (string, bool, bool) {
	if (qual == innerAlias || (inner != nil && qual == inner.Name)) && colOn(inner, col) {
		return innerAlias + "." + col, true, true
	}
	if name, ok := outerColName(outer, qual+"."+col); ok {
		return name, false, true
	}
	if name, ok := outerColName(outer, col); ok && (qual == outerAlias || (outer != nil && qual == outer.Name) || hasAliasPrefix(outer, qual)) {
		return name, false, true
	}
	return "", false, false
}

func hasOuterRef(items []ast.SelectItem, inner *catalog.Table, innerAlias string, outer *catalog.Table, outerAlias string) bool {
	for _, item := range items {
		_, sides, ok := rewriteCorr(item.Expr, inner, innerAlias, outer, outerAlias)
		if !ok || sides[0] {
			return true
		}
	}
	return false
}

func colOn(tab *catalog.Table, name string) bool {
	if tab == nil || name == "" {
		return false
	}
	_, ok := tab.ColIndex(name)
	return ok
}

func colOnSuffix(tab *catalog.Table, name string) bool {
	if tab == nil || name == "" {
		return false
	}
	for _, c := range tab.Columns {
		if i := strings.LastIndex(c.Name, "."); i >= 0 && c.Name[i+1:] == name {
			return true
		}
	}
	return false
}

func hasAliasPrefix(tab *catalog.Table, alias string) bool {
	if tab == nil || alias == "" {
		return false
	}
	prefix := alias + "."
	for _, c := range tab.Columns {
		if strings.HasPrefix(c.Name, prefix) {
			return true
		}
	}
	return tab.Name == alias
}

func outerColName(outer *catalog.Table, name string) (string, bool) {
	if outer == nil || name == "" {
		return "", false
	}
	if _, ok := outer.ColIndex(name); ok {
		return name, true
	}
	var match string
	for _, c := range outer.Columns {
		if i := strings.LastIndex(c.Name, "."); i >= 0 && c.Name[i+1:] == name {
			if match != "" {
				return "", false
			}
			match = c.Name
		}
	}
	if match != "" {
		return match, true
	}
	return "", false
}

func conjunctsOf(e ast.Expr) []ast.Expr {
	if e == nil {
		return nil
	}
	if b, ok := e.(ast.Binary); ok && b.Op == "AND" {
		return append(conjunctsOf(b.Left), conjunctsOf(b.Right)...)
	}
	return []ast.Expr{e}
}

func andOf(cs []ast.Expr) ast.Expr {
	var out ast.Expr
	for _, c := range cs {
		if c == nil {
			continue
		}
		if out == nil {
			out = c
			continue
		}
		out = ast.Binary{Op: "AND", Left: out, Right: c}
	}
	return out
}
