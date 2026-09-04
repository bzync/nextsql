package executor

import (
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

// isNoFromSelect reports whether stmt is a top-level `SELECT <expr-list>`
// with no FROM clause at all (e.g. `SELECT 1`, `SELECT NOW()`). The parser
// (internal/sql/parser sel()) already rejects NoFrom combined with
// GROUP BY/HAVING/SEARCH/NEAREST/FACET, and NoFrom is only ever set when
// Star/Joins/FromQuery are unset — the extra checks here are defense in
// depth, matching isSystemSelect's own belt-and-suspenders style.
func (s *Session) isNoFromSelect(stmt ast.Stmt) (ast.Select, bool) {
	sel, ok := stmt.(ast.Select)
	if !ok || !sel.NoFrom {
		return ast.Select{}, false
	}
	if sel.Star || len(sel.Joins) > 0 || sel.FromQuery != nil || sel.Table != "" {
		return ast.Select{}, false
	}
	if len(sel.Group) > 0 || sel.Having != nil || sel.SearchQuery != nil || sel.NearestQuery != nil || sel.Nearest2Query != nil || len(sel.FacetCols) > 0 {
		return ast.Select{}, false
	}
	return sel, true
}

// execNoFromSelect evaluates a FROM-less SELECT's list exactly once against
// no row/table context (tab=nil, row=nil — s.eval already fails closed with
// a clear error for any column reference in that context) and returns it as
// a single row, or zero rows if WHERE rejects it. No catalog/index/table is
// touched: this bypasses the normal binder/planner pipeline entirely, the
// same architectural precedent as execSystemSelect's virtual tables.
func (s *Session) execNoFromSelect(sel ast.Select) (*Result, error) {
	// This path bypasses s.authorize(stmt) entirely (see the dispatch site in
	// session.go, which returns before ever reaching it), so RBAC has to be
	// enforced here explicitly — the same architectural precedent as
	// execSystemSelect's own s.require call just below in this package. Fail
	// closed: a session without CONNECT must not be able to execute arbitrary
	// expressions (function calls included) via a FROM-less SELECT.
	if err := s.require(security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		return nil, err
	}
	if len(sel.List) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "executor.noFromSelect", "empty select list")
	}
	if sel.Where != nil {
		v, err := s.eval(sel.Where, nil, nil)
		if err != nil {
			return nil, err
		}
		if v.Null || v.Typ.Kind != types.KindBool || !v.Bool {
			return &Result{Columns: selectListNames(sel.List), Rows: [][]types.Value{}}, nil
		}
	}
	outCols := selectListNames(sel.List)
	row := make([]types.Value, len(sel.List))
	for i, item := range sel.List {
		v, err := s.eval(item.Expr, nil, nil)
		if err != nil {
			return nil, err
		}
		row[i] = v
	}
	// ORDER BY is a no-op on a result of at most one row, but its expressions
	// are still validated (e.g. a stray column reference must still error)
	// rather than silently accepted and ignored.
	for _, o := range sel.Order {
		if _, err := s.eval(o.Expr, nil, nil); err != nil {
			return nil, err
		}
	}
	rows := [][]types.Value{row}
	if sel.Offset != nil {
		if *sel.Offset < 0 {
			return nil, nerr.New(nerr.InvalidArgument, "executor.noFromSelect", "OFFSET must be >=0")
		}
		if *sel.Offset > 0 {
			rows = [][]types.Value{}
		}
	}
	if sel.Limit != nil {
		if *sel.Limit < 0 {
			return nil, nerr.New(nerr.InvalidArgument, "executor.noFromSelect", "LIMIT must be >=0")
		}
		if *sel.Limit == 0 {
			rows = [][]types.Value{}
		}
	}
	return &Result{Columns: outCols, Rows: rows}, nil
}

func selectListNames(list []ast.SelectItem) []string {
	names := make([]string, len(list))
	for i, item := range list {
		switch {
		case item.Alias != "":
			names[i] = item.Alias
		default:
			if id, ok := item.Expr.(ast.Ident); ok {
				names[i] = id.Name
				continue
			}
			if call, ok := item.Expr.(ast.Call); ok {
				names[i] = call.Name
				continue
			}
			names[i] = "?"
		}
	}
	return names
}
