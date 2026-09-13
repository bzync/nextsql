package executor

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

// correlateSubquery replaces references to the current outer row with typed
// literals. Inner-scope columns always win for unqualified names.
func (s *Session) correlateSubquery(stmt ast.Stmt, outer *catalog.Table, row []types.Value) (ast.Stmt, bool) {
	if outer == nil || len(row) == 0 {
		return stmt, false
	}
	switch q := stmt.(type) {
	case ast.With:
		changed := false
		q.CTEs = append([]ast.CTEDef(nil), q.CTEs...)
		for i, cte := range q.CTEs {
			inner, c := s.correlateSubquery(cte.Query, outer, row)
			q.CTEs[i].Query = inner
			changed = changed || c
		}
		inner, c := s.correlateSubquery(q.Query, outer, row)
		q.Query = inner
		return q, changed || c
	case ast.SetOperation:
		left, lc := s.correlateSubquery(q.Left, outer, row)
		right, rc := s.correlateSubquery(q.Right, outer, row)
		q.Left, q.Right = left, right
		return q, lc || rc
	case ast.Select:
		q.List = append([]ast.SelectItem(nil), q.List...)
		q.Group = append([]ast.Expr(nil), q.Group...)
		q.Order = append([]ast.OrderItem(nil), q.Order...)
		q.Joins = append([]ast.JoinSpec(nil), q.Joins...)
		var inner *catalog.Table
		correlated := false
		// innerOpaque means this scope reads a relation whose columns are not
		// visible here — a CTE, which lookup (a table lookup) cannot resolve.
		// An unqualified name in such a scope may well belong to that relation,
		// so treating it as an outer reference would replace a perfectly good
		// inner column with the outer row's value and silently answer the
		// wrong question. Only an explicitly qualified outer reference is
		// correlated there.
		innerOpaque := false
		if q.FromQuery != nil {
			fq, c := s.correlateSubquery(q.FromQuery, outer, row)
			q.FromQuery = fq
			correlated = c
			inner = derivedSchema(q.FromQuery, q.Alias)
			innerOpaque = inner == nil
		} else if q.Table != "" {
			inner, _ = s.lookup(q.Table)
			innerOpaque = inner == nil
		}
		rewrite := func(e ast.Expr) ast.Expr {
			out, changed := correlateExpr(e, inner, q.Table, q.Alias, outer, row, innerOpaque)
			correlated = correlated || changed
			return out
		}
		for i := range q.List {
			q.List[i].Expr = rewrite(q.List[i].Expr)
		}
		q.Where = rewrite(q.Where)
		q.Having = rewrite(q.Having)
		q.SearchQuery = rewrite(q.SearchQuery)
		q.NearestQuery = rewrite(q.NearestQuery)
		for i := range q.Group {
			q.Group[i] = rewrite(q.Group[i])
		}
		for i := range q.Order {
			q.Order[i].Expr = rewrite(q.Order[i].Expr)
		}
		for i := range q.Joins {
			q.Joins[i].On = rewrite(q.Joins[i].On)
		}
		return q, correlated
	default:
		return stmt, false
	}
}

func derivedSchema(stmt ast.Stmt, alias string) *catalog.Table {
	names := stmtOutputNames(stmt)
	if len(names) == 0 {
		return nil
	}
	tab := &catalog.Table{Name: alias}
	for _, name := range names {
		tab.Columns = append(tab.Columns, catalog.Column{Name: name})
	}
	return tab
}

func stmtOutputNames(stmt ast.Stmt) []string {
	switch s := stmt.(type) {
	case ast.With:
		return stmtOutputNames(s.Query)
	case ast.SetOperation:
		return stmtOutputNames(s.Left)
	case ast.Select:
		if s.Star {
			return stmtOutputNames(s.FromQuery)
		}
		var names []string
		for _, item := range s.List {
			if item.Alias != "" {
				names = append(names, item.Alias)
				continue
			}
			switch x := item.Expr.(type) {
			case ast.Ident:
				names = append(names, x.Name)
			case ast.Path:
				if len(x.Parts) > 0 {
					names = append(names, x.Parts[len(x.Parts)-1])
				} else {
					names = append(names, "?")
				}
			default:
				names = append(names, "?")
			}
		}
		return names
	default:
		return nil
	}
}

func correlateExpr(e ast.Expr, inner *catalog.Table, innerName, innerAlias string, outer *catalog.Table, row []types.Value, innerOpaque bool) (ast.Expr, bool) {
	if e == nil {
		return nil, false
	}
	outerValue := func(name string) (ast.Expr, bool) {
		i, ok := outer.ColIndex(name)
		if !ok || i < 0 || i >= len(row) {
			return nil, false
		}
		return ast.Literal{Value: row[i]}, true
	}
	switch x := e.(type) {
	case ast.Ident:
		if inner != nil {
			if _, ok := inner.ColIndex(x.Name); ok {
				return e, false
			}
		}
		if innerOpaque {
			// The inner relation's columns are unknown here, so an unqualified
			// name is assumed to be one of them.
			return e, false
		}
		if v, ok := outerValue(x.Name); ok {
			return v, true
		}
		return e, false
	case ast.Path:
		if len(x.Parts) == 2 {
			if x.Parts[0] == innerName || x.Parts[0] == innerAlias {
				return e, false
			}
			if v, ok := outerValue(x.Parts[0] + "." + x.Parts[1]); ok {
				return v, true
			}
			if v, ok := outerValue(x.Parts[1]); ok {
				return v, true
			}
		}
		return e, false
	case ast.Unary:
		right, changed := correlateExpr(x.Right, inner, innerName, innerAlias, outer, row, innerOpaque)
		x.Right = right
		return x, changed
	case ast.Binary:
		left, lc := correlateExpr(x.Left, inner, innerName, innerAlias, outer, row, innerOpaque)
		right, rc := correlateExpr(x.Right, inner, innerName, innerAlias, outer, row, innerOpaque)
		x.Left, x.Right = left, right
		return x, lc || rc
	case ast.Between:
		value, vc := correlateExpr(x.Expr, inner, innerName, innerAlias, outer, row, innerOpaque)
		low, lc := correlateExpr(x.Low, inner, innerName, innerAlias, outer, row, innerOpaque)
		high, hc := correlateExpr(x.High, inner, innerName, innerAlias, outer, row, innerOpaque)
		x.Expr, x.Low, x.High = value, low, high
		return x, vc || lc || hc
	case ast.IsNull:
		value, changed := correlateExpr(x.Expr, inner, innerName, innerAlias, outer, row, innerOpaque)
		x.Expr = value
		return x, changed
	case ast.Call:
		changed := false
		for i := range x.Args {
			x.Args[i], changed = correlateOne(x.Args[i], inner, innerName, innerAlias, outer, row, changed, innerOpaque)
		}
		return x, changed
	case ast.Window:
		changed := false
		fn, ch := correlateExpr(x.Fn, inner, innerName, innerAlias, outer, row, innerOpaque)
		if call, ok := fn.(ast.Call); ok {
			x.Fn = call
		}
		changed = changed || ch
		for i := range x.Partition {
			x.Partition[i], changed = correlateOne(x.Partition[i], inner, innerName, innerAlias, outer, row, changed, innerOpaque)
		}
		for i := range x.Order {
			x.Order[i].Expr, changed = correlateOne(x.Order[i].Expr, inner, innerName, innerAlias, outer, row, changed, innerOpaque)
		}
		return x, changed
	case ast.Case:
		changed := false
		x.Operand, changed = correlateOne(x.Operand, inner, innerName, innerAlias, outer, row, changed, innerOpaque)
		for i := range x.Whens {
			x.Whens[i].When, changed = correlateOne(x.Whens[i].When, inner, innerName, innerAlias, outer, row, changed, innerOpaque)
			x.Whens[i].Then, changed = correlateOne(x.Whens[i].Then, inner, innerName, innerAlias, outer, row, changed, innerOpaque)
		}
		x.Else, changed = correlateOne(x.Else, inner, innerName, innerAlias, outer, row, changed, innerOpaque)
		return x, changed
	case ast.InSubquery:
		value, changed := correlateExpr(x.Expr, inner, innerName, innerAlias, outer, row, innerOpaque)
		x.Expr = value
		return x, changed
	default:
		return e, false
	}
}

func correlateOne(e ast.Expr, inner *catalog.Table, innerName, innerAlias string, outer *catalog.Table, row []types.Value, changed, innerOpaque bool) (ast.Expr, bool) {
	out, one := correlateExpr(e, inner, innerName, innerAlias, outer, row, innerOpaque)
	return out, changed || one
}
