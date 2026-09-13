package parser

import (
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
)

// Every shape here used to be parsed into a tree as deep as the input, which
// every later stage then walked recursively. A 2 MiB statement of the "flat"
// or "paren" shape exhausted Go's 1 GB goroutine stack and killed nextsqld
// with an unrecoverable fatal error — a remote denial of service available to
// any authenticated user, since the statement is far below max_statement_bytes.
func deepShapes(n int) map[string]string {
	return map[string]string{
		"flat":    "SELECT 1" + strings.Repeat("+1", n),
		"paren":   "SELECT " + strings.Repeat("(", n) + "1" + strings.Repeat(")", n),
		"not":     "SELECT " + strings.Repeat("NOT ", n) + "TRUE",
		"and":     "SELECT 1 WHERE TRUE" + strings.Repeat(" AND TRUE", n),
		"or":      "SELECT 1 WHERE TRUE" + strings.Repeat(" OR TRUE", n),
		"union":   "SELECT 1" + strings.Repeat(" UNION ALL SELECT 1", n),
		"call":    "SELECT " + strings.Repeat("ABS(", n) + "1" + strings.Repeat(")", n),
		"case":    "SELECT " + strings.Repeat("CASE WHEN TRUE THEN ", n) + "1" + strings.Repeat(" END", n),
		"subq":    "SELECT " + strings.Repeat("(SELECT ", n) + "1" + strings.Repeat(")", n),
		"derived": "SELECT * FROM " + strings.Repeat("(SELECT 1 AS c FROM ", n) + "t" + strings.Repeat(") x", n),
		"mul":     "SELECT 1" + strings.Repeat("*1", n),
	}
}

func TestNestingBeyondLimitRefused(t *testing.T) {
	for name, src := range deepShapes(ast.MaxNestingDepth + 64) {
		t.Run(name, func(t *testing.T) {
			stmt, err := Parse(src)
			if err == nil {
				t.Fatalf("%s: parsed a statement nested past the limit", name)
			}
			if stmt != nil {
				t.Fatalf("%s: error with non-nil stmt", name)
			}
			var ne *nerr.Error
			if !asNerr(err, &ne) || ne.Code != nerr.InvalidArgument {
				t.Fatalf("%s: want invalid_argument, got %v", name, err)
			}
			if !strings.Contains(ne.Message, "nesting") {
				t.Fatalf("%s: message does not name the limit: %q", name, ne.Message)
			}
		})
	}
}

// The parser must refuse an over-deep chain while reading it, not after
// building the whole tree: the rejected statement is attacker-controlled and
// up to max_statement_bytes long, so a post-parse-only check still allocated
// hundreds of megabytes per request (measured 646 MiB of RSS for one 2 MiB
// statement) even though it answered with an error.
func TestOverDeepChainStopsEarly(t *testing.T) {
	const terms = 2_000_000
	src := "SELECT 1" + strings.Repeat("+1", terms)
	p := &Parser{}
	_ = p
	stmt, err := Parse(src)
	if err == nil || stmt != nil {
		t.Fatalf("want refusal, got stmt=%v err=%v", stmt != nil, err)
	}
	// The refusal has to come from the depth budget, not from running out of
	// input or memory first.
	var ne *nerr.Error
	if !asNerr(err, &ne) || !strings.Contains(ne.Message, "nesting") {
		t.Fatalf("want a nesting refusal, got %v", err)
	}
}

func TestNestingWithinLimitParses(t *testing.T) {
	// A shape's own grammar may cost more than one level per repetition, so
	// this stays well inside the budget: the point is that ordinary deep
	// generated SQL is still accepted.
	for name, src := range deepShapes(ast.MaxNestingDepth/4 - 8) {
		t.Run(name, func(t *testing.T) {
			stmt, err := Parse(src)
			if err != nil {
				t.Fatalf("%s: refused a statement inside the limit: %v", name, err)
			}
			if ast.ExceedsDepth(stmt, ast.MaxNestingDepth) {
				t.Fatalf("%s: accepted statement is deeper than the limit", name)
			}
		})
	}
}

// ParseDiag must stay in agreement with Parse for a refused statement, since
// Studio's diagnostics endpoint reports the position it returns.
func TestNestingDiagAgrees(t *testing.T) {
	src := "SELECT 1" + strings.Repeat("+1", ast.MaxNestingDepth+64)
	stmt, diag, err := ParseDiag(src)
	if err == nil {
		t.Fatal("ParseDiag accepted an over-deep statement")
	}
	if stmt != nil {
		t.Fatal("ParseDiag returned a statement with an error")
	}
	if diag == nil {
		t.Fatal("ParseDiag returned no diagnostic")
	}
	if diag.Offset < 0 || diag.Offset > len(src) {
		t.Fatalf("diag offset %d out of range [0,%d]", diag.Offset, len(src))
	}
	if diag.Message == "" {
		t.Fatal("diag has no message")
	}
}

func asNerr(err error, out **nerr.Error) bool {
	ne, ok := err.(*nerr.Error)
	if !ok {
		return false
	}
	*out = ne
	return true
}
