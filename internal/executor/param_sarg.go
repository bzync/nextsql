package executor

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/binder"
)

// Access-path selection only recognises a comparison against a constant: the
// optimizer folds the predicate and bakes the constant into the index-scan
// bounds. A bound parameter is not a constant at plan time, so `WHERE id = $1`
// -- the form every driver sends -- planned as a sequential scan with a filter
// while `WHERE id = 5` used the primary key. Measured on a 32K-row table over
// the wire: a primary-key lookup through a parameter took 31 ms, the same as a
// full scan of the table.
//
// bindSargableParams substitutes each such parameter's bound value into the
// comparison before planning, so the statement is planned exactly as its
// literal form is. The plan then depends on the values, so the caller must not
// share it through the plan cache (plans are cached by SQL text).
//
// Only a parameter compared directly with a column is substituted; elsewhere a
// parameter keeps its runtime evaluation, which is what it had before. A
// comparison against a client-encrypted column is left alone: its parameter
// must reach execution as a parameter so the ciphertext checks still apply.
// A NULL value is left alone too: it cannot drive an index, and leaving it
// keeps the NULL comparison semantics exactly as evaluated at run time.
func (s *Session) bindSargableParams(b binder.Bound) (binder.Bound, bool) {
	if len(s.params) == 0 {
		return b, false
	}
	switch st := b.(type) {
	case binder.Select:
		if st.Table == nil || len(st.Joins) > 0 || len(st.Subjoins) > 0 || st.Input != nil || st.Where == nil {
			return b, false
		}
		where, changed := s.substituteParams(st.Where, st.Table)
		if !changed {
			return b, false
		}
		st.Where = where
		return st, true
	case binder.Update:
		if st.Where == nil {
			return b, false
		}
		where, changed := s.substituteParams(st.Where, st.Table)
		if !changed {
			return b, false
		}
		st.Where = where
		return st, true
	case binder.Delete:
		if st.Where == nil {
			return b, false
		}
		where, changed := s.substituteParams(st.Where, st.Table)
		if !changed {
			return b, false
		}
		st.Where = where
		return st, true
	case binder.Explain:
		// EXPLAIN with bound parameters shows the plan the statement would
		// actually run, which is the substituted one.
		inner, changed := s.bindSargableParams(st.Stmt)
		if !changed {
			return b, false
		}
		st.Stmt = inner
		return st, true
	default:
		return b, false
	}
}

func (s *Session) substituteParams(e ast.Expr, tab *catalog.Table) (ast.Expr, bool) {
	x, ok := e.(ast.Binary)
	if !ok || tab == nil {
		return e, false
	}
	switch x.Op {
	case "AND", "OR":
		l, lc := s.substituteParams(x.Left, tab)
		r, rc := s.substituteParams(x.Right, tab)
		if !lc && !rc {
			return e, false
		}
		x.Left, x.Right = l, r
		return x, true
	case "=", "<>", "!=", "<", "<=", ">", ">=":
		if s.plainColumn(x.Left, tab) {
			if lit, ok := s.paramLiteral(x.Right); ok {
				x.Right = lit
				return x, true
			}
		}
		if s.plainColumn(x.Right, tab) {
			if lit, ok := s.paramLiteral(x.Left); ok {
				x.Left = lit
				return x, true
			}
		}
	}
	return e, false
}

// plainColumn reports whether e names a column of tab that is not
// client-encrypted.
func (s *Session) plainColumn(e ast.Expr, tab *catalog.Table) bool {
	id, ok := e.(ast.Ident)
	if !ok {
		return false
	}
	ord, ok := tab.ColIndex(id.Name)
	return ok && !tab.Columns[ord].ClientEncrypted()
}

func (s *Session) paramLiteral(e ast.Expr) (ast.Expr, bool) {
	p, ok := e.(ast.Param)
	if !ok {
		return nil, false
	}
	v, err := s.lookupParam(p.Name)
	if err != nil || v.Null {
		return nil, false
	}
	return ast.Literal{Value: v}, true
}
