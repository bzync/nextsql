package executor

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/binder"
	"github.com/bzync/nextsql/internal/sql/lexer"
	"github.com/bzync/nextsql/internal/sql/parser"
	"github.com/bzync/nextsql/internal/sql/planner"
)

// execCreateView stores a named query.
//
// The defining query is validated now — it must parse, its relations must
// resolve, and the caller must be allowed to run it — so a view that is
// created is a view that works. It is validated again at each use, because the
// tables under it can change afterwards; creation-time validation is a
// courtesy to the operator, not a guarantee the engine later relies on.
func (s *Session) execCreateView(p planner.CreateView) (*Result, error) {
	if s == nil || s.db == nil || s.x == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.CreateView", "active transaction required")
	}
	existing, exists := s.lookupView(p.Name)
	if exists && !p.Replace {
		return nil, nerr.New(nerr.AlreadyExists, "executor.CreateView", "view already exists")
	}
	if _, ok := s.lookup(p.Name); ok {
		return nil, nerr.New(nerr.AlreadyExists, "executor.CreateView", "a table with that name already exists")
	}

	body, err := parser.Parse(p.Query)
	if err != nil {
		return nil, nerr.Wrap(nerr.InvalidArgument, "executor.CreateView", "view query does not parse", err)
	}
	if !isQueryStmt(body) {
		return nil, nerr.New(nerr.InvalidArgument, "executor.CreateView", "a view must be defined by a SELECT")
	}
	// Expand with this view already on the stack, so a definition that
	// references itself — directly, or through another view under CREATE OR
	// REPLACE — is refused instead of looping at query time.
	ex := &viewExpander{sess: s, stack: []string{p.Name}}
	expanded, err := ex.stmt(body, nil, 0)
	if err != nil {
		return nil, err
	}
	if err := s.authorize(expanded); err != nil {
		s.auditRecord(security.ActionViewCreate, p.Name, err)
		return nil, err
	}
	// A view is used by expanding it into a CTE, and a CTE's body must read a
	// relation, so a FROM-less SELECT cannot become a usable view. Refuse it
	// here rather than store something that only fails when someone selects
	// from it.
	if isFromLessSelect(expanded) {
		return nil, nerr.New(nerr.InvalidArgument, "executor.CreateView", "a view must select from at least one table")
	}
	{
		bound, err := binder.Bind(expanded, s.lookup, s.db.Cat.PeekNext())
		if err != nil {
			return nil, nerr.Wrap(nerr.InvalidArgument, "executor.CreateView", "view query does not bind", err)
		}
		if len(p.Columns) > 0 {
			names, ok := binder.OutputNames(bound)
			if ok && len(names) != len(p.Columns) {
				return nil, nerr.New(nerr.InvalidArgument, "executor.CreateView", "view column list does not match the query's output columns")
			}
		}
	}

	id := uint32(0)
	if exists && existing != nil {
		id = existing.ID
	} else {
		id = s.db.Cat.NextID()
	}
	// Store the body with every identifier quoted. A view body is SQL text
	// parsed again on every use, so a column or table name that later becomes
	// a reserved word would otherwise break the view the day that keyword is
	// added (`bool` did, log #284). The quoted text lexes to the identical
	// token stream, so the view means exactly what was written.
	query, err := lexer.QuoteIdentifiers(p.Query)
	if err != nil {
		return nil, nerr.Wrap(nerr.Internal, "executor.CreateView", "canonical view text", err)
	}
	if len(query) > catalog.MaxViewDescriptor {
		// Quoting adds two bytes per identifier. A body that only fits
		// unquoted is kept as written rather than refused.
		query = p.Query
	}
	view := &catalog.View{ID: id, Name: p.Name, Owner: s.user, Columns: p.Columns, Query: query}
	raw, err := catalog.EncodeView(view)
	if err != nil {
		return nil, err
	}
	tx := s.x.use(s.db.CatTree)
	key := catalog.ViewKey(view.Name)
	if exists {
		if err := tx.Update(key, raw); err != nil {
			return nil, err
		}
	} else if err := tx.Insert(key, raw); err != nil {
		return nil, err
	}
	s.viewOverlay[view.Name] = view.Clone()
	s.db.Cat.SetNextID(view.ID + 1)
	return &Result{}, nil
}

func (s *Session) execDropView(p planner.DropView) (*Result, error) {
	if s == nil || s.db == nil || s.x == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.DropView", "active transaction required")
	}
	if _, ok := s.lookupView(p.Name); !ok {
		if p.IfExists {
			return &Result{}, nil
		}
		return nil, nerr.New(nerr.NotFound, "executor.DropView", "unknown view")
	}
	if err := s.refuseIfViewsDependOn(p.Name); err != nil {
		return nil, err
	}
	if err := s.x.use(s.db.CatTree).Delete(catalog.ViewKey(p.Name)); err != nil {
		return nil, err
	}
	s.viewOverlay[p.Name] = nil
	return &Result{}, nil
}

// refuseIfViewsDependOn stops a relation from being dropped while a view is
// defined over it. A view is stored as text and resolved at use, so dropping
// what it reads would leave a view that only fails when someone runs it —
// which is exactly the kind of half-broken catalog the engine should not
// produce. The check also covers a view built on another view.
func (s *Session) refuseIfViewsDependOn(name string) error {
	if s == nil || s.db == nil || name == "" {
		return nil
	}
	for _, v := range s.listViews() {
		if v == nil || v.Name == name {
			continue
		}
		body, err := parser.Parse(v.Query)
		if err != nil {
			// A view whose text no longer parses cannot be shown to depend on
			// anything; it is already broken and reports so when used.
			continue
		}
		if stmtReferences(body, name) {
			return nerr.New(nerr.InvalidArgument, "executor.Drop", "view "+v.Name+" depends on "+name)
		}
	}
	return nil
}

// stmtReferences reports whether a parsed statement names the relation
// anywhere a relation can be named.
func stmtReferences(st ast.Stmt, name string) bool {
	found := false
	walkRelationNames(st, 0, func(n string) {
		if n == name {
			found = true
		}
	})
	return found
}

// walkRelationNames visits every relation name a statement reads. It is
// deliberately conservative: a name shadowed by a CTE is still reported, so a
// dependency check can only ever refuse too much, never too little.
func walkRelationNames(st ast.Stmt, depth int, fn func(string)) {
	if st == nil || depth > catalog.MaxViewDepth*4 {
		return
	}
	switch x := st.(type) {
	case ast.Select:
		if x.Table != "" {
			fn(x.Table)
		}
		for _, j := range x.Joins {
			if j.Table != "" {
				fn(j.Table)
			}
		}
		walkRelationNames(x.FromQuery, depth+1, fn)
		walkExprRelations(x.Where, depth+1, fn)
		walkExprRelations(x.Having, depth+1, fn)
		for _, it := range x.List {
			walkExprRelations(it.Expr, depth+1, fn)
		}
		for _, g := range x.Group {
			walkExprRelations(g, depth+1, fn)
		}
		for _, o := range x.Order {
			walkExprRelations(o.Expr, depth+1, fn)
		}
		for _, j := range x.Joins {
			walkExprRelations(j.On, depth+1, fn)
		}
	case ast.With:
		for _, c := range x.CTEs {
			walkRelationNames(c.Query, depth+1, fn)
		}
		walkRelationNames(x.Query, depth+1, fn)
	case ast.SetOperation:
		walkRelationNames(x.Left, depth+1, fn)
		walkRelationNames(x.Right, depth+1, fn)
	case ast.Explain:
		walkRelationNames(x.Stmt, depth+1, fn)
	}
}

func walkExprRelations(e ast.Expr, depth int, fn func(string)) {
	if e == nil || depth > catalog.MaxViewDepth*4 {
		return
	}
	switch x := e.(type) {
	case ast.ScalarSubquery:
		walkRelationNames(x.Query, depth+1, fn)
	case ast.InSubquery:
		walkExprRelations(x.Expr, depth+1, fn)
		walkRelationNames(x.Query, depth+1, fn)
	case ast.ExistsSubquery:
		walkRelationNames(x.Query, depth+1, fn)
	case ast.Unary:
		walkExprRelations(x.Right, depth+1, fn)
	case ast.Binary:
		walkExprRelations(x.Left, depth+1, fn)
		walkExprRelations(x.Right, depth+1, fn)
	case ast.Between:
		walkExprRelations(x.Expr, depth+1, fn)
		walkExprRelations(x.Low, depth+1, fn)
		walkExprRelations(x.High, depth+1, fn)
	case ast.IsNull:
		walkExprRelations(x.Expr, depth+1, fn)
	case ast.Call:
		for _, a := range x.Args {
			walkExprRelations(a, depth+1, fn)
		}
	case ast.Case:
		walkExprRelations(x.Operand, depth+1, fn)
		walkExprRelations(x.Else, depth+1, fn)
		for _, w := range x.Whens {
			walkExprRelations(w.When, depth+1, fn)
			walkExprRelations(w.Then, depth+1, fn)
		}
	}
}

// isFromLessSelect reports whether st is a `SELECT <exprs>` with no FROM, the
// shape the executor evaluates directly rather than through the binder.
func isFromLessSelect(st ast.Stmt) bool {
	sel, ok := st.(ast.Select)
	return ok && sel.NoFrom
}

func isQueryStmt(st ast.Stmt) bool {
	switch st.(type) {
	case ast.Select, ast.With, ast.SetOperation:
		return true
	}
	return false
}
