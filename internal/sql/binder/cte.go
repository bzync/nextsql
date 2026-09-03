package binder

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

func bindWith(w ast.With, lookup Lookup, nextID uint32, outer map[string]*CTE) (Bound, error) {
	if len(w.CTEs) == 0 {
		return nil, nerr.New(nerr.Syntax, "sql.binder", "WITH requires a CTE")
	}
	if len(w.CTEs) > security.MaxCTEs {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "WITH exceeds CTE limit")
	}
	scope := copyCTEs(outer)
	out := make([]CTE, 0, len(w.CTEs))
	seen := make(map[string]struct{}, len(w.CTEs))
	var next uint64
	for _, existing := range scope {
		if existing != nil && existing.ID >= next {
			next = existing.ID + 1
		}
	}
	for _, def := range w.CTEs {
		if def.Name == "" {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "empty CTE name")
		}
		if _, ok := seen[def.Name]; ok {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "duplicate CTE name")
		}
		seen[def.Name] = struct{}{}
		id := next
		next++
		bound, err := bindOneCTE(def, w.Recursive, lookup, nextID, scope, id)
		if err != nil {
			return nil, err
		}
		cp := bound
		scope[def.Name] = &cp
		out = append(out, bound)
	}
	queryAST := rewriteNestedCTERefs(w.Query, scope)
	query, err := bind(queryAST, lookup, nextID, scope)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if c := scope[out[i].Name]; c != nil {
			out[i].Refs = c.Refs
		}
	}
	return With{CTEs: out, Query: query}, nil
}

func bindOneCTE(def ast.CTEDef, recursiveWith bool, lookup Lookup, nextID uint32, scope map[string]*CTE, id uint64) (CTE, error) {
	anchorAST, recAST, all, recursive, err := splitRecursiveQuery(def.Query, def.Name)
	if err != nil {
		return CTE{}, err
	}
	if recursive && !recursiveWith {
		return CTE{}, nerr.New(nerr.InvalidArgument, "sql.binder", "self-referencing CTE requires WITH RECURSIVE")
	}
	if recursive {
		anchorAST = rewriteNestedCTERefs(anchorAST, scope)
		anchor, err := bind(anchorAST, lookup, nextID, scope)
		if err != nil {
			return CTE{}, err
		}
		names, schema, err := cteSchema(def, anchor)
		if err != nil {
			return CTE{}, err
		}
		self := &CTE{Name: def.Name, ID: id, Names: names, Schema: schema, Recursive: true, Distinct: !all, Materialize: ast.CTEAlways, QueryAST: def.Query}
		inner := copyCTEs(scope)
		inner[def.Name] = self
		recAST = rewriteNestedCTERefs(recAST, inner)
		rec, err := bind(recAST, lookup, nextID, inner)
		if err != nil {
			return CTE{}, err
		}
		if err := checkRecursiveTerm(rec); err != nil {
			return CTE{}, err
		}
		self.Query = rec
		self.Anchor = anchor
		self.RecursiveQ = rec
		self.Refs = 0
		return *self, nil
	}
	queryAST := rewriteNestedCTERefs(def.Query, scope)
	query, err := bind(queryAST, lookup, nextID, scope)
	if err != nil {
		return CTE{}, err
	}
	names, schema, err := cteSchema(def, query)
	if err != nil {
		return CTE{}, err
	}
	return CTE{
		Name:        def.Name,
		ID:          id,
		Query:       query,
		QueryAST:    queryAST,
		Names:       names,
		Schema:      schema,
		Materialize: def.Materialize,
	}, nil
}

func cteSchema(def ast.CTEDef, query Bound) ([]string, *catalog.Table, error) {
	names, ok := boundNames(query)
	if !ok || len(names) == 0 {
		return nil, nil, nerr.New(nerr.InvalidArgument, "sql.binder", "CTE must expose columns")
	}
	if len(def.Columns) > 0 {
		if len(def.Columns) != len(names) {
			return nil, nil, nerr.New(nerr.InvalidArgument, "sql.binder", "CTE column count mismatch")
		}
		seen := make(map[string]struct{}, len(def.Columns))
		for _, name := range def.Columns {
			if name == "" {
				return nil, nil, nerr.New(nerr.InvalidArgument, "sql.binder", "empty CTE column name")
			}
			if _, dup := seen[name]; dup {
				return nil, nil, nerr.New(nerr.InvalidArgument, "sql.binder", "duplicate CTE column name")
			}
			seen[name] = struct{}{}
		}
		names = append([]string(nil), def.Columns...)
	}
	tab := &catalog.Table{Name: def.Name, Columns: make([]catalog.Column, len(names))}
	for i, name := range names {
		tab.Columns[i] = catalog.Column{Name: name, Type: types.Type{Kind: types.KindInvalid}}
	}
	fillCTETypes(tab, query)
	return names, tab, nil
}

func fillCTETypes(tab *catalog.Table, query Bound) {
	switch q := query.(type) {
	case Select:
		if q.Schema == nil {
			return
		}
		for i, ex := range q.OutExprs {
			if i >= len(tab.Columns) {
				return
			}
			if id, ok := ex.(ast.Ident); ok {
				if j, found := q.Schema.ColIndex(id.Name); found {
					name := tab.Columns[i].Name
					tab.Columns[i] = q.Schema.Columns[j]
					tab.Columns[i].Name = name
				}
			}
		}
	case SetOperation:
		fillCTETypes(tab, q.Left)
	case With:
		fillCTETypes(tab, q.Query)
	}
}

func splitRecursiveQuery(stmt ast.Stmt, name string) (anchor, rec ast.Stmt, all, recursive bool, err error) {
	if !referencesCTE(stmt, name) {
		return stmt, nil, false, false, nil
	}
	arms, allFlags, ok := unionArms(stmt)
	if !ok {
		return nil, nil, false, false, nerr.New(nerr.InvalidArgument, "sql.binder", "recursive CTE requires UNION [ALL]")
	}
	var anchors, recs []ast.Stmt
	all = true
	sawNotAll := false
	sawAll := false
	for i, arm := range arms {
		if i > 0 {
			if allFlags[i] {
				sawAll = true
			} else {
				sawNotAll = true
				all = false
			}
		}
		if referencesCTE(arm, name) {
			recs = append(recs, arm)
		} else {
			anchors = append(anchors, arm)
		}
	}
	if sawAll && sawNotAll {
		return nil, nil, false, false, nerr.New(nerr.InvalidArgument, "sql.binder", "recursive CTE cannot mix UNION and UNION ALL")
	}
	if len(anchors) == 0 {
		return nil, nil, false, false, nerr.New(nerr.InvalidArgument, "sql.binder", "recursive CTE is missing a non-recursive term")
	}
	if len(recs) == 0 {
		return stmt, nil, false, false, nil
	}
	return combineUnion(anchors, all), combineUnion(recs, all), all, true, nil
}

func unionArms(stmt ast.Stmt) ([]ast.Stmt, []bool, bool) {
	switch s := stmt.(type) {
	case ast.SetOperation:
		if s.Op != "union" {
			return nil, nil, false
		}
		left, lall, ok := unionArms(s.Left)
		if !ok {
			return nil, nil, false
		}
		right, rall, ok := unionArms(s.Right)
		if !ok {
			return nil, nil, false
		}
		all := make([]bool, 0, len(lall)+len(rall))
		all = append(all, lall...)
		if len(rall) > 0 {
			rall[0] = s.All
		}
		all = append(all, rall...)
		return append(left, right...), all, true
	case ast.Select, ast.With:
		return []ast.Stmt{s}, []bool{false}, true
	default:
		return nil, nil, false
	}
}

func combineUnion(arms []ast.Stmt, all bool) ast.Stmt {
	if len(arms) == 0 {
		return nil
	}
	out := arms[0]
	for _, arm := range arms[1:] {
		out = ast.SetOperation{Left: out, Right: arm, Op: "union", All: all}
	}
	return out
}

func referencesCTE(stmt ast.Stmt, name string) bool {
	if stmt == nil || name == "" {
		return false
	}
	switch s := stmt.(type) {
	case ast.SetOperation:
		return referencesCTE(s.Left, name) || referencesCTE(s.Right, name)
	case ast.With:
		for _, cte := range s.CTEs {
			if cte.Name == name {
				return false
			}
			if referencesCTE(cte.Query, name) {
				return true
			}
		}
		return referencesCTE(s.Query, name)
	case ast.Select:
		if s.Table == name {
			return true
		}
		if s.FromQuery != nil && referencesCTE(s.FromQuery, name) {
			return true
		}
		for _, j := range s.Joins {
			if j.Table == name {
				return true
			}
		}
		if referencesExprCTE(s.Where, name) || referencesExprCTE(s.Having, name) {
			return true
		}
		for _, item := range s.List {
			if referencesExprCTE(item.Expr, name) {
				return true
			}
		}
		for _, g := range s.Group {
			if referencesExprCTE(g, name) {
				return true
			}
		}
		for _, o := range s.Order {
			if referencesExprCTE(o.Expr, name) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func referencesExprCTE(e ast.Expr, name string) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case ast.ScalarSubquery:
		return referencesCTE(x.Query, name)
	case ast.InSubquery:
		return referencesCTE(x.Query, name) || referencesExprCTE(x.Expr, name)
	case ast.ExistsSubquery:
		return referencesCTE(x.Query, name)
	case ast.Unary:
		return referencesExprCTE(x.Right, name)
	case ast.Binary:
		return referencesExprCTE(x.Left, name) || referencesExprCTE(x.Right, name)
	case ast.Between:
		return referencesExprCTE(x.Expr, name) || referencesExprCTE(x.Low, name) || referencesExprCTE(x.High, name)
	case ast.Call:
		for _, a := range x.Args {
			if referencesExprCTE(a, name) {
				return true
			}
		}
		return false
	case ast.Window:
		if referencesExprCTE(x.Fn, name) {
			return true
		}
		for _, p := range x.Partition {
			if referencesExprCTE(p, name) {
				return true
			}
		}
		for _, o := range x.Order {
			if referencesExprCTE(o.Expr, name) {
				return true
			}
		}
		if x.Frame != nil {
			if referencesExprCTE(x.Frame.Start.Offset, name) || referencesExprCTE(x.Frame.End.Offset, name) {
				return true
			}
		}
		return false
	case ast.Case:
		if referencesExprCTE(x.Operand, name) || referencesExprCTE(x.Else, name) {
			return true
		}
		for _, arm := range x.Whens {
			if referencesExprCTE(arm.When, name) || referencesExprCTE(arm.Then, name) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func checkRecursiveTerm(b Bound) error {
	switch s := b.(type) {
	case Select:
		if s.Distinct || s.HasAgg || s.Limit != nil || s.Offset != nil || len(s.Order) > 0 || len(s.Windows) > 0 {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "recursive term cannot use DISTINCT, aggregation, ORDER BY, LIMIT, OFFSET, or window functions")
		}
		for _, j := range s.Joins {
			if j.Kind == ast.JoinLeft || j.Kind == ast.JoinRight || j.Kind == ast.JoinFull {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "recursive term cannot use outer joins")
			}
		}
		return nil
	case SetOperation:
		if err := checkRecursiveTerm(s.Left); err != nil {
			return err
		}
		return checkRecursiveTerm(s.Right)
	case With:
		return checkRecursiveTerm(s.Query)
	case CTERef:
		return nil
	default:
		return nerr.New(nerr.InvalidArgument, "sql.binder", "unsupported recursive term")
	}
}

func copyCTEs(src map[string]*CTE) map[string]*CTE {
	out := make(map[string]*CTE, len(src)+1)
	for k, v := range src {
		out[k] = v
	}
	return out
}

func rewriteNestedCTERefs(stmt ast.Stmt, ctes map[string]*CTE) ast.Stmt {
	if stmt == nil || len(ctes) == 0 {
		return stmt
	}
	switch s := stmt.(type) {
	case ast.SetOperation:
		s.Left = rewriteNestedCTERefs(s.Left, ctes)
		s.Right = rewriteNestedCTERefs(s.Right, ctes)
		return s
	case ast.With:
		s.CTEs = append([]ast.CTEDef(nil), s.CTEs...)
		inner := copyCTEs(ctes)
		for _, cte := range s.CTEs {
			delete(inner, cte.Name)
		}
		for i, cte := range s.CTEs {
			s.CTEs[i].Query = rewriteNestedCTERefs(cte.Query, inner)
		}
		s.Query = rewriteNestedCTERefs(s.Query, inner)
		return s
	case ast.Select:
		s.Where = rewriteExprCTERefs(s.Where, ctes)
		s.Having = rewriteExprCTERefs(s.Having, ctes)
		s.SearchQuery = rewriteExprCTERefs(s.SearchQuery, ctes)
		s.NearestQuery = rewriteExprCTERefs(s.NearestQuery, ctes)
		s.Nearest2Query = rewriteExprCTERefs(s.Nearest2Query, ctes)
		for i := range s.List {
			s.List[i].Expr = rewriteExprCTERefs(s.List[i].Expr, ctes)
		}
		for i := range s.Group {
			s.Group[i] = rewriteExprCTERefs(s.Group[i], ctes)
		}
		for i := range s.Order {
			s.Order[i].Expr = rewriteExprCTERefs(s.Order[i].Expr, ctes)
		}
		for i := range s.Joins {
			s.Joins[i].On = rewriteExprCTERefs(s.Joins[i].On, ctes)
		}
		if s.FromQuery != nil {
			s.FromQuery = rewriteNestedCTERefs(s.FromQuery, ctes)
		}
		return s
	default:
		return stmt
	}
}

func rewriteExprCTERefs(e ast.Expr, ctes map[string]*CTE) ast.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case ast.ScalarSubquery:
		x.Query = expandCTEFrom(x.Query, ctes)
		x.Query = rewriteNestedCTERefs(x.Query, ctes)
		return x
	case ast.InSubquery:
		x.Expr = rewriteExprCTERefs(x.Expr, ctes)
		x.Query = expandCTEFrom(x.Query, ctes)
		x.Query = rewriteNestedCTERefs(x.Query, ctes)
		return x
	case ast.ExistsSubquery:
		x.Query = expandCTEFrom(x.Query, ctes)
		x.Query = rewriteNestedCTERefs(x.Query, ctes)
		return x
	case ast.Unary:
		x.Right = rewriteExprCTERefs(x.Right, ctes)
		return x
	case ast.Binary:
		x.Left = rewriteExprCTERefs(x.Left, ctes)
		x.Right = rewriteExprCTERefs(x.Right, ctes)
		return x
	case ast.Between:
		x.Expr = rewriteExprCTERefs(x.Expr, ctes)
		x.Low = rewriteExprCTERefs(x.Low, ctes)
		x.High = rewriteExprCTERefs(x.High, ctes)
		return x
	case ast.Call:
		for i := range x.Args {
			x.Args[i] = rewriteExprCTERefs(x.Args[i], ctes)
		}
		return x
	case ast.Window:
		fn := rewriteExprCTERefs(x.Fn, ctes)
		if call, ok := fn.(ast.Call); ok {
			x.Fn = call
		}
		for i := range x.Partition {
			x.Partition[i] = rewriteExprCTERefs(x.Partition[i], ctes)
		}
		for i := range x.Order {
			x.Order[i].Expr = rewriteExprCTERefs(x.Order[i].Expr, ctes)
		}
		if x.Frame != nil {
			x.Frame.Start.Offset = rewriteExprCTERefs(x.Frame.Start.Offset, ctes)
			x.Frame.End.Offset = rewriteExprCTERefs(x.Frame.End.Offset, ctes)
		}
		return x
	case ast.Case:
		x.Operand = rewriteExprCTERefs(x.Operand, ctes)
		x.Else = rewriteExprCTERefs(x.Else, ctes)
		for i := range x.Whens {
			x.Whens[i].When = rewriteExprCTERefs(x.Whens[i].When, ctes)
			x.Whens[i].Then = rewriteExprCTERefs(x.Whens[i].Then, ctes)
		}
		return x
	default:
		return e
	}
}

func expandCTEFrom(stmt ast.Stmt, ctes map[string]*CTE) ast.Stmt {
	if stmt == nil {
		return nil
	}
	switch s := stmt.(type) {
	case ast.SetOperation:
		s.Left = expandCTEFrom(s.Left, ctes)
		s.Right = expandCTEFrom(s.Right, ctes)
		return s
	case ast.Select:
		if s.FromQuery == nil {
			if c := ctes[s.Table]; c != nil && c.QueryAST != nil && !c.Recursive {
				s.FromQuery = c.QueryAST
				s.Alias = aliasOr(s.Alias, s.Table)
				s.Table = ""
			}
		} else {
			s.FromQuery = expandCTEFrom(s.FromQuery, ctes)
		}
		return s
	default:
		return stmt
	}
}
