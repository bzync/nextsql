package executor

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/parser"
)

// expandViews rewrites a parsed statement so every reference to a view becomes
// a common table expression holding the view's defining query.
//
// A view is expanded rather than given a table of its own because its query has
// to re-resolve against the catalog as it is now: the tables under it may have
// gained columns, been re-indexed or been rewritten since the view was created.
// Expanding to a CTE also means a view joins, filters and nests exactly like
// any other relation, with no separate access path to keep correct.
//
// Expansion is per-SELECT rather than once at the top, so a view referenced
// only inside a subquery of an UPDATE or DELETE is expanded there — a CTE
// cannot be attached to a DML statement, but it can be attached to the SELECT
// inside it.
//
// Authorization is the invoker's, not the view owner's: the rewritten
// statement reads the underlying tables by name, so the caller needs the same
// privileges they would need to write the query out by hand. A view is a
// convenience, never a way to reach data the caller could not otherwise read.
func (s *Session) expandViews(stmt ast.Stmt) (ast.Stmt, error) {
	if s == nil || s.db == nil {
		return stmt, nil
	}
	s.db.mu.RLock()
	haveViews := len(s.db.views) > 0
	s.db.mu.RUnlock()
	// The session's own uncommitted views count too: a view created earlier in
	// this transaction has to be usable by it.
	if !haveViews && len(s.viewOverlay) == 0 {
		return stmt, nil
	}
	ex := &viewExpander{sess: s}
	out, err := ex.stmt(stmt, nil, 0)
	if err != nil {
		return nil, err
	}
	return out, nil
}

type viewExpander struct {
	sess *Session
	// stack is the chain of views being expanded, so a view that reaches
	// itself is reported as a cycle rather than expanded forever.
	stack []string
}

func (e *viewExpander) inStack(name string) bool {
	for _, n := range e.stack {
		if n == name {
			return true
		}
	}
	return false
}

// stmt rewrites one statement. visible names are relation names already bound
// in an enclosing scope (CTEs), which shadow a view of the same name.
func (e *viewExpander) stmt(st ast.Stmt, visible map[string]struct{}, depth int) (ast.Stmt, error) {
	if depth > catalog.MaxViewDepth {
		return nil, nerr.New(nerr.InvalidArgument, "executor.view", "view references nest too deeply")
	}
	switch x := st.(type) {
	case ast.Select:
		return e.selectStmt(x, visible, depth)
	case ast.With:
		inner := copySet(visible)
		for i := range x.CTEs {
			q, err := e.stmt(x.CTEs[i].Query, inner, depth+1)
			if err != nil {
				return nil, err
			}
			x.CTEs[i].Query = q
			// A CTE is visible to the CTEs after it and to the body.
			inner[x.CTEs[i].Name] = struct{}{}
		}
		q, err := e.stmt(x.Query, inner, depth+1)
		if err != nil {
			return nil, err
		}
		x.Query = q
		return x, nil
	case ast.SetOperation:
		l, err := e.stmt(x.Left, visible, depth+1)
		if err != nil {
			return nil, err
		}
		r, err := e.stmt(x.Right, visible, depth+1)
		if err != nil {
			return nil, err
		}
		x.Left, x.Right = l, r
		return x, nil
	case ast.Explain:
		inner, err := e.stmt(x.Stmt, visible, depth+1)
		if err != nil {
			return nil, err
		}
		x.Stmt = inner
		return x, nil
	case ast.CreateTable:
		// CREATE TABLE ... AS reads relations like any other query, so a view
		// named in its source is expanded here. The target is a table being
		// created, never a view, so there is nothing to refuse.
		if x.Query == nil {
			return x, nil
		}
		q, err := e.stmt(x.Query, visible, depth+1)
		if err != nil {
			return nil, err
		}
		x.Query = q
		return x, nil
	case ast.Insert:
		if err := e.refuseWrite(x.Table); err != nil {
			return nil, err
		}
		rows, err := e.exprRows(x.Rows, visible, depth)
		if err != nil {
			return nil, err
		}
		x.Rows = rows
		// A query source reads relations like any other query, so a view named
		// in it is expanded here too -- reading a view is always allowed, it
		// is only writing to one that refuseWrite rejects above.
		if x.Query != nil {
			q, err := e.stmt(x.Query, visible, depth+1)
			if err != nil {
				return nil, err
			}
			x.Query = q
		}
		return x, nil
	case ast.Upsert:
		if err := e.refuseWrite(x.Table); err != nil {
			return nil, err
		}
		rows, err := e.exprRows(x.Rows, visible, depth)
		if err != nil {
			return nil, err
		}
		x.Rows = rows
		return x, nil
	case ast.Update:
		if err := e.refuseWrite(x.Table); err != nil {
			return nil, err
		}
		for i := range x.Sets {
			v, err := e.expr(x.Sets[i].Expr, visible, depth)
			if err != nil {
				return nil, err
			}
			x.Sets[i].Expr = v
		}
		w, err := e.expr(x.Where, visible, depth)
		if err != nil {
			return nil, err
		}
		x.Where = w
		return x, nil
	case ast.Delete:
		if err := e.refuseWrite(x.Table); err != nil {
			return nil, err
		}
		w, err := e.expr(x.Where, visible, depth)
		if err != nil {
			return nil, err
		}
		x.Where = w
		return x, nil
	}
	return st, nil
}

// refuseWrite rejects a DML statement whose target is a view. NextSQL views are
// read-only: an updatable view would need a documented row-mapping rule back to
// base-table rows, and silently writing to the wrong place is worse than the
// refusal.
func (e *viewExpander) refuseWrite(table string) error {
	if table == "" {
		return nil
	}
	if _, ok := e.sess.lookupView(table); ok {
		return nerr.New(nerr.InvalidArgument, "executor.view", "cannot write through a view: "+table)
	}
	return nil
}

func (e *viewExpander) selectStmt(sel ast.Select, visible map[string]struct{}, depth int) (ast.Stmt, error) {
	inner := copySet(visible)
	if sel.FromQuery != nil {
		q, err := e.stmt(sel.FromQuery, inner, depth+1)
		if err != nil {
			return nil, err
		}
		sel.FromQuery = q
	}
	// Rewrite every expression that can hold a subquery.
	var err error
	if sel.Where, err = e.expr(sel.Where, inner, depth); err != nil {
		return nil, err
	}
	if sel.Having, err = e.expr(sel.Having, inner, depth); err != nil {
		return nil, err
	}
	for i := range sel.List {
		if sel.List[i].Expr, err = e.expr(sel.List[i].Expr, inner, depth); err != nil {
			return nil, err
		}
	}
	for i := range sel.Group {
		if sel.Group[i], err = e.expr(sel.Group[i], inner, depth); err != nil {
			return nil, err
		}
	}
	for i := range sel.Order {
		if sel.Order[i].Expr, err = e.expr(sel.Order[i].Expr, inner, depth); err != nil {
			return nil, err
		}
	}
	for i := range sel.Joins {
		if sel.Joins[i].On, err = e.expr(sel.Joins[i].On, inner, depth); err != nil {
			return nil, err
		}
	}

	// Collect the relations this SELECT names directly.
	names := make([]string, 0, 1+len(sel.Joins))
	if sel.Table != "" {
		names = append(names, sel.Table)
	}
	for _, j := range sel.Joins {
		if j.Table != "" {
			names = append(names, j.Table)
		}
	}
	defs, err := e.defsFor(names, inner, depth)
	if err != nil {
		return nil, err
	}
	if len(defs) == 0 {
		return sel, nil
	}
	return ast.With{CTEs: defs, Query: sel}, nil
}

// defsFor builds the CTE definitions for whichever of names are views, in
// dependency order: a view a second view depends on is defined first.
func (e *viewExpander) defsFor(names []string, visible map[string]struct{}, depth int) ([]ast.CTEDef, error) {
	var defs []ast.CTEDef
	added := make(map[string]struct{})
	var add func(name string, depth int) error
	add = func(name string, depth int) error {
		if name == "" {
			return nil
		}
		if _, shadowed := visible[name]; shadowed {
			return nil
		}
		if _, done := added[name]; done {
			return nil
		}
		view, ok := e.sess.lookupView(name)
		if !ok {
			return nil
		}
		if e.inStack(name) {
			return nerr.New(nerr.InvalidArgument, "executor.view", "view definition is cyclic: "+name)
		}
		if depth > catalog.MaxViewDepth {
			return nerr.New(nerr.InvalidArgument, "executor.view", "view references nest too deeply")
		}
		body, err := parser.Parse(view.Query)
		if err != nil {
			return nerr.Wrap(nerr.InvalidArgument, "executor.view", "view "+name+" no longer parses", err)
		}
		e.stack = append(e.stack, name)
		expanded, err := e.stmt(body, nil, depth+1)
		e.stack = e.stack[:len(e.stack)-1]
		if err != nil {
			return err
		}
		added[name] = struct{}{}
		defs = append(defs, ast.CTEDef{Name: name, Columns: view.Columns, Query: expanded})
		return nil
	}
	for _, n := range names {
		if err := add(n, depth); err != nil {
			return nil, err
		}
	}
	return defs, nil
}

func (e *viewExpander) exprRows(rows [][]ast.Expr, visible map[string]struct{}, depth int) ([][]ast.Expr, error) {
	for i := range rows {
		for j := range rows[i] {
			v, err := e.expr(rows[i][j], visible, depth)
			if err != nil {
				return nil, err
			}
			rows[i][j] = v
		}
	}
	return rows, nil
}

// expr rewrites the subqueries an expression can hold. Everything else is
// returned unchanged.
func (e *viewExpander) expr(ex ast.Expr, visible map[string]struct{}, depth int) (ast.Expr, error) {
	if ex == nil {
		return nil, nil
	}
	if depth > catalog.MaxViewDepth*2 {
		return nil, nerr.New(nerr.InvalidArgument, "executor.view", "view references nest too deeply")
	}
	switch x := ex.(type) {
	case ast.ScalarSubquery:
		q, err := e.stmt(x.Query, visible, depth+1)
		if err != nil {
			return nil, err
		}
		x.Query = q
		return x, nil
	case ast.InSubquery:
		v, err := e.expr(x.Expr, visible, depth)
		if err != nil {
			return nil, err
		}
		q, err := e.stmt(x.Query, visible, depth+1)
		if err != nil {
			return nil, err
		}
		x.Expr, x.Query = v, q
		return x, nil
	case ast.ExistsSubquery:
		q, err := e.stmt(x.Query, visible, depth+1)
		if err != nil {
			return nil, err
		}
		x.Query = q
		return x, nil
	case ast.Unary:
		r, err := e.expr(x.Right, visible, depth)
		if err != nil {
			return nil, err
		}
		x.Right = r
		return x, nil
	case ast.Binary:
		l, err := e.expr(x.Left, visible, depth)
		if err != nil {
			return nil, err
		}
		r, err := e.expr(x.Right, visible, depth)
		if err != nil {
			return nil, err
		}
		x.Left, x.Right = l, r
		return x, nil
	case ast.Between:
		v, err := e.expr(x.Expr, visible, depth)
		if err != nil {
			return nil, err
		}
		lo, err := e.expr(x.Low, visible, depth)
		if err != nil {
			return nil, err
		}
		hi, err := e.expr(x.High, visible, depth)
		if err != nil {
			return nil, err
		}
		x.Expr, x.Low, x.High = v, lo, hi
		return x, nil
	case ast.IsNull:
		v, err := e.expr(x.Expr, visible, depth)
		if err != nil {
			return nil, err
		}
		x.Expr = v
		return x, nil
	case ast.Call:
		for i := range x.Args {
			v, err := e.expr(x.Args[i], visible, depth)
			if err != nil {
				return nil, err
			}
			x.Args[i] = v
		}
		return x, nil
	case ast.Case:
		v, err := e.expr(x.Operand, visible, depth)
		if err != nil {
			return nil, err
		}
		x.Operand = v
		if x.Else, err = e.expr(x.Else, visible, depth); err != nil {
			return nil, err
		}
		for i := range x.Whens {
			if x.Whens[i].When, err = e.expr(x.Whens[i].When, visible, depth); err != nil {
				return nil, err
			}
			if x.Whens[i].Then, err = e.expr(x.Whens[i].Then, visible, depth); err != nil {
				return nil, err
			}
		}
		return x, nil
	}
	return ex, nil
}

func copySet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in)+2)
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}
