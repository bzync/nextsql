package catalog

import (
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

func nestedNot(n int) ast.Expr {
	var e ast.Expr = ast.Literal{Value: types.StringValue("x")}
	for i := 0; i < n; i++ {
		e = ast.Unary{Op: "NOT", Right: e}
	}
	return e
}

// A stored expression is written by the parser, which now bounds nesting, so
// anything past the stored bound is corrupt or hostile bytes. Decoding it must
// fail closed instead of recursing as deep as the record says.
func TestTakeExprRefusesOverDeepStoredExpression(t *testing.T) {
	buf, err := appendExpr(nil, nestedNot(ast.MaxStoredNestingDepth+16))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, _, err := takeExpr(buf, 0); err == nil {
		t.Fatal("decoded an expression nested past the stored bound")
	} else if !strings.Contains(err.Error(), "too deep") {
		t.Fatalf("want a depth refusal, got %v", err)
	}
}

// Everything a parser could have written stays readable: a database created
// before the bound existed must still open.
func TestTakeExprAcceptsParserDepth(t *testing.T) {
	buf, err := appendExpr(nil, nestedNot(ast.MaxNestingDepth))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, _, err := takeExpr(buf, 0)
	if err != nil {
		t.Fatalf("decode at parser depth: %v", err)
	}
	if got == nil {
		t.Fatal("decode returned no expression")
	}
}
