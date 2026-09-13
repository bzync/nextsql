package binder

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

// validateChecks type-checks every stored CHECK predicate against the table it
// belongs to. A check is evaluated per written row, by the leader only, and its
// result is durable in the sense that rows already written satisfied it — so a
// predicate must be deterministic and must depend on nothing but the row.
func validateChecks(t *catalog.Table) error {
	if t == nil {
		return nil
	}
	if len(t.Checks) > catalog.MaxChecksPerTable {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "too many CHECK constraints")
	}
	for _, c := range t.Checks {
		if err := validateCheckExpr(t, c.Expr); err != nil {
			return err
		}
	}
	return nil
}

// validateCheckExpr rejects everything a stored row predicate must not contain
// and then type-checks the remainder against the table's own columns.
func validateCheckExpr(t *catalog.Table, e ast.Expr) error {
	if e == nil {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "CHECK requires a predicate")
	}
	if err := checkExprShape(e, 0); err != nil {
		return err
	}
	// The server holds only ciphertext for an ENCRYPTED CLIENT column, so a
	// predicate over one would constrain ciphertext bytes, not the value the
	// client wrote.
	for _, col := range t.Columns {
		if col.ClientEncrypted() && catalog.ExprUsesIdent(e, col.Name) {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "CHECK cannot reference an ENCRYPTED CLIENT column")
		}
	}
	return checkExpr(e, t, types.Type{}, false)
}

// checkExprShape walks the predicate for constructs a CHECK may not use. Each
// refusal is a property the constraint model depends on:
//
//   - a subquery would make the constraint depend on another table's contents,
//     which nothing re-validates when that table changes;
//   - an aggregate or window would make it depend on other rows;
//   - UUID(), NOW() and AI() would make it non-deterministic, so a row that
//     passed once could fail an identical later evaluation, and a replica or a
//     recovery replay could disagree with the leader;
//   - a parameter has no value at DDL time.
func checkExprShape(e ast.Expr, depth int) error {
	if e == nil || depth > ast.MaxNestingDepth {
		return nil
	}
	switch x := e.(type) {
	case ast.ScalarSubquery, ast.InSubquery, ast.ExistsSubquery:
		return nerr.New(nerr.InvalidArgument, "sql.binder", "CHECK cannot use a subquery")
	case ast.Window:
		return nerr.New(nerr.InvalidArgument, "sql.binder", "CHECK cannot use a window function")
	case ast.Param:
		return nerr.New(nerr.InvalidArgument, "sql.binder", "CHECK cannot use a parameter")
	case ast.Call:
		switch x.Name {
		case "uuid", "now", "ai":
			return nerr.New(nerr.InvalidArgument, "sql.binder", "CHECK must be deterministic: UUID(), NOW() and AI() are not allowed")
		case "count", "sum", "avg", "min", "max", "array_agg", "map_agg":
			return nerr.New(nerr.InvalidArgument, "sql.binder", "CHECK cannot use an aggregate")
		}
		if x.Star {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "CHECK cannot use an aggregate")
		}
		for _, a := range x.Args {
			if err := checkExprShape(a, depth+1); err != nil {
				return err
			}
		}
	case ast.Unary:
		return checkExprShape(x.Right, depth+1)
	case ast.Binary:
		if err := checkExprShape(x.Left, depth+1); err != nil {
			return err
		}
		return checkExprShape(x.Right, depth+1)
	case ast.Between:
		if err := checkExprShape(x.Expr, depth+1); err != nil {
			return err
		}
		if err := checkExprShape(x.Low, depth+1); err != nil {
			return err
		}
		return checkExprShape(x.High, depth+1)
	case ast.IsNull:
		return checkExprShape(x.Expr, depth+1)
	case ast.Case:
		if err := checkExprShape(x.Operand, depth+1); err != nil {
			return err
		}
		if err := checkExprShape(x.Else, depth+1); err != nil {
			return err
		}
		for _, arm := range x.Whens {
			if err := checkExprShape(arm.When, depth+1); err != nil {
				return err
			}
			if err := checkExprShape(arm.Then, depth+1); err != nil {
				return err
			}
		}
	case ast.FieldAccess:
		return checkExprShape(x.Base, depth+1)
	case ast.Subscript:
		if err := checkExprShape(x.Coll, depth+1); err != nil {
			return err
		}
		return checkExprShape(x.Index, depth+1)
	case ast.ArrayCtor:
		for _, el := range x.Elems {
			if err := checkExprShape(el, depth+1); err != nil {
				return err
			}
		}
	case ast.StructCtor:
		for _, el := range x.Elems {
			if err := checkExprShape(el, depth+1); err != nil {
				return err
			}
		}
	case ast.MapCtor:
		for _, el := range x.Keys {
			if err := checkExprShape(el, depth+1); err != nil {
				return err
			}
		}
		for _, el := range x.Vals {
			if err := checkExprShape(el, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
