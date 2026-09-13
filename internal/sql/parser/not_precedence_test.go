package parser

import (
	"testing"

	"github.com/bzync/nextsql/internal/sql/ast"
)

// NOT used to be parsed at unary precedence, so it bound tighter than the
// comparison it was meant to negate: `NOT a = b` became `(NOT a) = b` and
// `NOT a IS NULL` became `(NOT a) IS NULL`, which is the opposite predicate.
// For a non-boolean operand the engine failed closed with "NOT requires
// boolean"; for a boolean one it silently answered the wrong question.
func whereOf(t *testing.T, sql string) ast.Expr {
	t.Helper()
	stmt, err := Parse(sql)
	if err != nil {
		t.Fatalf("parse %q: %v", sql, err)
	}
	sel, ok := stmt.(ast.Select)
	if !ok {
		t.Fatalf("parse %q: want Select, got %T", sql, stmt)
	}
	return sel.Where
}

func TestNotBindsLooserThanComparison(t *testing.T) {
	cases := []struct {
		sql   string
		check func(ast.Expr) bool
		want  string
	}{
		{
			sql:  "SELECT 1 FROM t WHERE NOT a = b",
			want: "NOT (a = b)",
			check: func(e ast.Expr) bool {
				u, ok := e.(ast.Unary)
				if !ok || u.Op != "NOT" {
					return false
				}
				b, ok := u.Right.(ast.Binary)
				return ok && b.Op == "="
			},
		},
		{
			sql:  "SELECT 1 FROM t WHERE NOT a IS NULL",
			want: "NOT (a IS NULL)",
			check: func(e ast.Expr) bool {
				u, ok := e.(ast.Unary)
				if !ok || u.Op != "NOT" {
					return false
				}
				_, ok = u.Right.(ast.IsNull)
				return ok
			},
		},
		{
			sql:  "SELECT 1 FROM t WHERE NOT a BETWEEN 1 AND 2",
			want: "NOT (a BETWEEN 1 AND 2)",
			check: func(e ast.Expr) bool {
				u, ok := e.(ast.Unary)
				if !ok || u.Op != "NOT" {
					return false
				}
				bt, ok := u.Right.(ast.Between)
				return ok && !bt.Not
			},
		},
		{
			sql:  "SELECT 1 FROM t WHERE NOT a IN (SELECT b FROM u)",
			want: "NOT (a IN (...))",
			check: func(e ast.Expr) bool {
				u, ok := e.(ast.Unary)
				if !ok || u.Op != "NOT" {
					return false
				}
				in, ok := u.Right.(ast.InSubquery)
				return ok && !in.Not
			},
		},
		{
			sql:  "SELECT 1 FROM t WHERE NOT a < b",
			want: "NOT (a < b)",
			check: func(e ast.Expr) bool {
				u, ok := e.(ast.Unary)
				if !ok || u.Op != "NOT" {
					return false
				}
				b, ok := u.Right.(ast.Binary)
				return ok && b.Op == "<"
			},
		},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if !c.check(whereOf(t, c.sql)) {
				t.Fatalf("%q did not parse as %s", c.sql, c.want)
			}
		})
	}
}

// NOT still composes the way it always did everywhere else.
func TestNotStillComposes(t *testing.T) {
	for _, sql := range []string{
		"SELECT 1 FROM t WHERE NOT NOT a = b",
		"SELECT 1 FROM t WHERE NOT EXISTS (SELECT 1 FROM u)",
		"SELECT 1 FROM t WHERE a NOT IN (SELECT b FROM u)",
		"SELECT 1 FROM t WHERE a NOT BETWEEN 1 AND 2",
		"SELECT 1 FROM t WHERE a IS NOT NULL",
		"SELECT 1 FROM t WHERE NOT a = b AND NOT c = d",
		"SELECT 1 FROM t WHERE a = 1 OR NOT b = 2",
		// NOT inside an operand keeps working, so nothing that parsed as an
		// operand before now fails.
		"SELECT 1 FROM t WHERE a = NOT b",
	} {
		if _, err := Parse(sql); err != nil {
			t.Fatalf("parse %q: %v", sql, err)
		}
	}
}

func TestDoubleNotNests(t *testing.T) {
	e := whereOf(t, "SELECT 1 FROM t WHERE NOT NOT a = b")
	outer, ok := e.(ast.Unary)
	if !ok || outer.Op != "NOT" {
		t.Fatalf("want outer NOT, got %T", e)
	}
	inner, ok := outer.Right.(ast.Unary)
	if !ok || inner.Op != "NOT" {
		t.Fatalf("want inner NOT, got %T", outer.Right)
	}
	if b, ok := inner.Right.(ast.Binary); !ok || b.Op != "=" {
		t.Fatalf("want the comparison under both NOTs, got %T", inner.Right)
	}
}
