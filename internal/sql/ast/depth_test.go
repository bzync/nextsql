package ast

import (
	"testing"

	"github.com/bzync/nextsql/internal/sql/types"
)

func chain(n int) Expr {
	var e Expr = Literal{Value: types.StringValue("x")}
	for i := 0; i < n; i++ {
		e = Binary{Op: "+", Left: e, Right: Literal{Value: types.StringValue("x")}}
	}
	return e
}

func TestExceedsDepthCountsNesting(t *testing.T) {
	if ExceedsDepth(chain(4), 8) {
		t.Fatal("a shallow tree was reported as too deep")
	}
	if !ExceedsDepth(chain(64), 8) {
		t.Fatal("a deep tree was not reported")
	}
}

// The walk must not recurse: a tree far deeper than any stack could hold has
// to be answered iteratively, which is the whole point of the check.
func TestExceedsDepthHandlesVeryDeepTree(t *testing.T) {
	if !ExceedsDepth(chain(2_000_000), MaxNestingDepth) {
		t.Fatal("a two-million-level tree was not reported as too deep")
	}
}

// Width is not depth: a statement with many sibling expressions is ordinary
// (a bulk INSERT), and must not be refused.
func TestExceedsDepthIgnoresWidth(t *testing.T) {
	rows := make([][]Expr, 0, 2000)
	for i := 0; i < 2000; i++ {
		row := make([]Expr, 0, 16)
		for j := 0; j < 16; j++ {
			row = append(row, Literal{Value: types.StringValue("x")})
		}
		rows = append(rows, row)
	}
	if ExceedsDepth(Insert{Table: "t", Rows: rows}, 8) {
		t.Fatal("a wide, shallow statement was reported as too deep")
	}
}

func TestExceedsDepthWalksStatements(t *testing.T) {
	var s Stmt = Select{}
	for i := 0; i < 64; i++ {
		s = SetOperation{Left: s, Right: Select{}, Op: "union"}
	}
	if !ExceedsDepth(s, 8) {
		t.Fatal("nested set operations were not counted")
	}
	if ExceedsDepth(s, 512) {
		t.Fatal("nested set operations were over-counted")
	}
}

func TestExceedsDepthNilSafe(t *testing.T) {
	if ExceedsDepth(nil, 4) {
		t.Fatal("nil reported as too deep")
	}
	if ExceedsDepth(Select{Where: nil}, 4) {
		t.Fatal("a nil expression field reported as too deep")
	}
}

// A subquery nests a statement inside an expression; both halves count.
func TestExceedsDepthCrossesExprStmtBoundary(t *testing.T) {
	var e Expr = Literal{Value: types.StringValue("x")}
	for i := 0; i < 32; i++ {
		e = ScalarSubquery{Query: Select{List: []SelectItem{{Expr: e}}}}
	}
	if !ExceedsDepth(e, 16) {
		t.Fatal("nested subqueries were not counted across the Expr/Stmt boundary")
	}
}
