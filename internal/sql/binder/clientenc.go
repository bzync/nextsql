package binder

import (
	"strings"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
)

func rejectClientEncryptedExpr(e ast.Expr, tab *catalog.Table, context string) error {
	if exprUsesClientEncrypted(e, tab) {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "ENCRYPTED CLIENT column cannot be used in "+context)
	}
	return nil
}

// checkClientEncryptedAssignment permits only an opaque parameter/NULL or a
// direct ciphertext copy. It rejects server-side expressions over plaintext-
// logical columns and prevents literals from accidentally being stored as if
// they were ciphertext (the executor also validates the NSCE1 envelope).
func checkClientEncryptedAssignment(e ast.Expr, tab *catalog.Table, target catalog.Column) error {
	if !target.ClientEncrypted() {
		return rejectClientEncryptedExpr(e, tab, "an assignment")
	}
	switch x := e.(type) {
	case ast.Param:
		return nil
	case ast.Literal:
		if x.Value.Null {
			return nil
		}
	case ast.Ident, ast.Path:
		if clientEncryptedCopyMatchesTarget(e, tab, target) {
			return nil
		}
	}
	return nerr.New(nerr.InvalidArgument, "sql.binder", "ENCRYPTED CLIENT assignment requires an encrypted parameter, NULL, or direct ciphertext copy")
}

func clientEncryptedCopyMatchesTarget(e ast.Expr, tab *catalog.Table, target catalog.Column) bool {
	ord, ok := encryptedColumnOrdinal(e, tab)
	if !ok || ord < 0 || ord >= len(tab.Columns) {
		return false
	}
	source := tab.Columns[ord]
	name := source.Name
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return name == target.Name && source.ClientType.Equals(target.ClientType)
}

func bareClientEncryptedColumn(e ast.Expr, tab *catalog.Table) bool {
	_, ok := encryptedColumnOrdinal(e, tab)
	return ok
}

func encryptedColumnOrdinal(e ast.Expr, tab *catalog.Table) (int, bool) {
	if tab == nil {
		return -1, false
	}
	var names []string
	switch x := e.(type) {
	case ast.Ident:
		names = []string{x.Name}
	case ast.Path:
		if len(x.Parts) == 0 {
			return -1, false
		}
		if len(x.Parts) == 2 {
			names = append(names, strings.Join(x.Parts, "."), x.Parts[1])
		}
		names = append(names, x.Parts[0])
	default:
		return -1, false
	}
	for _, name := range names {
		if ord, ok := tab.ColIndex(name); ok && tab.Columns[ord].ClientEncrypted() {
			return ord, true
		}
	}
	return -1, false
}

func exprUsesClientEncrypted(e ast.Expr, tab *catalog.Table) bool {
	if e == nil {
		return false
	}
	if bareClientEncryptedColumn(e, tab) {
		return true
	}
	switch x := e.(type) {
	case ast.Unary:
		return exprUsesClientEncrypted(x.Right, tab)
	case ast.Binary:
		return exprUsesClientEncrypted(x.Left, tab) || exprUsesClientEncrypted(x.Right, tab)
	case ast.Between:
		return exprUsesClientEncrypted(x.Expr, tab) || exprUsesClientEncrypted(x.Low, tab) || exprUsesClientEncrypted(x.High, tab)
	case ast.IsNull:
		return exprUsesClientEncrypted(x.Expr, tab)
	case ast.Call:
		for _, a := range x.Args {
			if exprUsesClientEncrypted(a, tab) {
				return true
			}
		}
	case ast.Window:
		if exprUsesClientEncrypted(x.Fn, tab) {
			return true
		}
		for _, a := range x.Partition {
			if exprUsesClientEncrypted(a, tab) {
				return true
			}
		}
		for _, o := range x.Order {
			if exprUsesClientEncrypted(o.Expr, tab) {
				return true
			}
		}
		if x.Frame != nil && (exprUsesClientEncrypted(x.Frame.Start.Offset, tab) || exprUsesClientEncrypted(x.Frame.End.Offset, tab)) {
			return true
		}
	case ast.Case:
		if exprUsesClientEncrypted(x.Operand, tab) || exprUsesClientEncrypted(x.Else, tab) {
			return true
		}
		for _, arm := range x.Whens {
			if exprUsesClientEncrypted(arm.When, tab) || exprUsesClientEncrypted(arm.Then, tab) {
				return true
			}
		}
	case ast.InSubquery:
		return exprUsesClientEncrypted(x.Expr, tab)
	}
	return false
}

func tableHasClientEncrypted(tab *catalog.Table) bool {
	if tab == nil {
		return false
	}
	for _, col := range tab.Columns {
		if col.ClientEncrypted() {
			return true
		}
	}
	return false
}

func boundHasClientEncryptedOutput(b Bound) bool {
	switch x := b.(type) {
	case Select:
		if x.Star {
			return tableHasClientEncrypted(x.Schema)
		}
		for _, ex := range x.OutExprs {
			if exprUsesClientEncrypted(ex, x.Schema) {
				return true
			}
		}
	case CTERef:
		return tableHasClientEncrypted(x.Schema)
	case With:
		return boundHasClientEncryptedOutput(x.Query)
	case SetOperation:
		return boundHasClientEncryptedOutput(x.Left) || boundHasClientEncryptedOutput(x.Right)
	}
	return false
}

func rejectClientEncryptedSubqueryExpr(e ast.Expr, lookup Lookup, ctes map[string]*CTE) error {
	if exprHasClientEncryptedSubquery(e, lookup, ctes) {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "subqueries cannot expose ENCRYPTED CLIENT columns")
	}
	return nil
}

func exprHasClientEncryptedSubquery(e ast.Expr, lookup Lookup, ctes map[string]*CTE) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case ast.ScalarSubquery:
		return stmtOutputsClientEncrypted(x.Query, lookup, ctes)
	case ast.InSubquery:
		return exprHasClientEncryptedSubquery(x.Expr, lookup, ctes) || stmtOutputsClientEncrypted(x.Query, lookup, ctes)
	case ast.ExistsSubquery:
		return false // EXISTS exposes only a boolean; its query binds independently.
	case ast.Unary:
		return exprHasClientEncryptedSubquery(x.Right, lookup, ctes)
	case ast.Binary:
		return exprHasClientEncryptedSubquery(x.Left, lookup, ctes) || exprHasClientEncryptedSubquery(x.Right, lookup, ctes)
	case ast.Between:
		return exprHasClientEncryptedSubquery(x.Expr, lookup, ctes) || exprHasClientEncryptedSubquery(x.Low, lookup, ctes) || exprHasClientEncryptedSubquery(x.High, lookup, ctes)
	case ast.IsNull:
		return exprHasClientEncryptedSubquery(x.Expr, lookup, ctes)
	case ast.Call:
		for _, arg := range x.Args {
			if exprHasClientEncryptedSubquery(arg, lookup, ctes) {
				return true
			}
		}
	case ast.Window:
		if exprHasClientEncryptedSubquery(x.Fn, lookup, ctes) {
			return true
		}
		for _, part := range x.Partition {
			if exprHasClientEncryptedSubquery(part, lookup, ctes) {
				return true
			}
		}
		for _, order := range x.Order {
			if exprHasClientEncryptedSubquery(order.Expr, lookup, ctes) {
				return true
			}
		}
		return x.Frame != nil && (exprHasClientEncryptedSubquery(x.Frame.Start.Offset, lookup, ctes) || exprHasClientEncryptedSubquery(x.Frame.End.Offset, lookup, ctes))
	case ast.Case:
		if exprHasClientEncryptedSubquery(x.Operand, lookup, ctes) || exprHasClientEncryptedSubquery(x.Else, lookup, ctes) {
			return true
		}
		for _, arm := range x.Whens {
			if exprHasClientEncryptedSubquery(arm.When, lookup, ctes) || exprHasClientEncryptedSubquery(arm.Then, lookup, ctes) {
				return true
			}
		}
	}
	return false
}

func stmtOutputsClientEncrypted(stmt ast.Stmt, lookup Lookup, ctes map[string]*CTE) bool {
	switch s := stmt.(type) {
	case ast.Select:
		if s.FromQuery != nil && stmtOutputsClientEncrypted(s.FromQuery, lookup, ctes) {
			return true
		}
		var schema *catalog.Table
		if c := ctes[s.Table]; c != nil {
			schema = c.Schema
		} else if lookup != nil {
			schema, _ = lookup(s.Table)
		}
		if schema != nil {
			schema = qualifyTable(schema, aliasOr(s.Alias, s.Table), len(s.Joins) > 0)
			for _, join := range s.Joins {
				var right *catalog.Table
				if c := ctes[join.Table]; c != nil {
					right = c.Schema
				} else if lookup != nil {
					right, _ = lookup(join.Table)
				}
				if right != nil {
					schema = mergeTables(schema, qualifyTable(right, aliasOr(join.Alias, join.Table), true))
				}
			}
			if s.Star && tableHasClientEncrypted(schema) {
				return true
			}
			for _, item := range s.List {
				if bareClientEncryptedColumn(item.Expr, schema) {
					return true
				}
			}
		}
		for _, item := range s.List {
			if exprHasClientEncryptedSubquery(item.Expr, lookup, ctes) {
				return true
			}
		}
	case ast.SetOperation:
		return stmtOutputsClientEncrypted(s.Left, lookup, ctes) || stmtOutputsClientEncrypted(s.Right, lookup, ctes)
	case ast.With:
		for _, cte := range s.CTEs {
			if stmtOutputsClientEncrypted(cte.Query, lookup, ctes) {
				return true
			}
		}
		return stmtOutputsClientEncrypted(s.Query, lookup, ctes)
	}
	return false
}

func rejectClientEncryptedSubqueriesInSelect(s ast.Select, lookup Lookup, ctes map[string]*CTE) error {
	var exprs []ast.Expr
	exprs = append(exprs, s.Where, s.Having, s.SearchQuery, s.NearestQuery, s.Nearest2Query)
	for _, item := range s.List {
		exprs = append(exprs, item.Expr)
	}
	exprs = append(exprs, s.Group...)
	for _, item := range s.Order {
		exprs = append(exprs, item.Expr)
	}
	for _, join := range s.Joins {
		exprs = append(exprs, join.On)
	}
	for _, e := range exprs {
		if err := rejectClientEncryptedSubqueryExpr(e, lookup, ctes); err != nil {
			return err
		}
	}
	return nil
}
