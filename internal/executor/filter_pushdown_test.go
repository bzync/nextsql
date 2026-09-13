package executor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/parser"
	"github.com/bzync/nextsql/internal/sql/types"
)

// A Filter over a sequential scan now keeps only matching rows while scanning
// (filterDuringScan) instead of materializing the table first. The oracle is
// the same predicate with an uncorrelated scalar subquery ANDed on: a subquery
// makes the predicate ineligible for pushdown, so it runs the original
// collect-then-filter path, and the subquery is always true.

func pushdownSession(t *testing.T) *Session {
	t.Helper()
	s := testDB(t).Session()
	execOK(t, s, `CREATE TABLE f (id INT64 PRIMARY KEY, k INT64, name STRING, amt DECIMAL(10,2), flag BOOL)`)
	for i := 0; i < 6000; i += 500 {
		var b strings.Builder
		b.WriteString(`INSERT INTO f VALUES `)
		for j := i; j < i+500; j++ {
			if j > i {
				b.WriteString(",")
			}
			name := fmt.Sprintf("'n%04d'", j)
			if j%11 == 0 {
				name = "NULL"
			}
			k := fmt.Sprint(j % 97)
			if j%13 == 0 {
				k = "NULL"
			}
			fmt.Fprintf(&b, "(%d, %s, %s, %d.%02d, %v)", j, k, name, j/7, j%100, j%2 == 0)
		}
		execOK(t, s, b.String())
	}
	return s
}

func TestFilterPushdownMatchesCollectThenFilter(t *testing.T) {
	s := pushdownSession(t)
	preds := []string{
		`k = 5`,
		`k = $1`,
		`k IS NULL`,
		`k IS NOT NULL AND id > 5500`,
		`name LIKE 'n00%'`,
		`name IN ('n0001', 'n0002', 'n5999')`,
		`NOT (k = 3)`,
		`flag`,
		`amt BETWEEN 10 AND 20.5`,
		`CASE WHEN k > 50 THEN flag ELSE NOT flag END`,
		`UPPER(name) = 'N0042'`,
		`COALESCE(k, -1) = -1`,
		`id > 5990 OR name IS NULL`,
		`k > 1000`,
	}
	for _, p := range preds {
		pushed := `SELECT id, k, name, amt, flag FROM f WHERE ` + p
		oracle := `SELECT id, k, name, amt, flag FROM f WHERE (` + p + `) AND (SELECT COUNT(*) FROM f WHERE id = 0) = 1`
		params := []Param{{Value: types.Int64Value(42)}}
		got, err := s.ExecContext(context.Background(), pushed, params)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		want, err := s.ExecContext(context.Background(), oracle, params)
		if err != nil {
			t.Fatalf("oracle %s: %v", p, err)
		}
		if g, w := strings.Join(canonicalRows(got), "\n"), strings.Join(canonicalRows(want), "\n"); g != w {
			t.Fatalf("%s: pushdown returned %d rows, collect-then-filter %d", p, len(got.Rows), len(want.Rows))
		}
	}
}

// An error raised by the predicate on some row still fails the statement.
func TestFilterPushdownPropagatesPredicateErrors(t *testing.T) {
	s := pushdownSession(t)
	if _, err := s.Exec(`SELECT id FROM f WHERE CAST(name AS INT64) = 1`); err == nil {
		t.Fatal("a predicate that fails on a row was silently skipped")
	}
}

func TestFilterPushdownOnPartitionedTable(t *testing.T) {
	s := testDB(t).Session()
	execOK(t, s, `CREATE TABLE p (id INT64 PRIMARY KEY, v INT64) PARTITION BY RANGE (id) (PARTITION a VALUES LESS THAN (100), PARTITION b VALUES LESS THAN (1000))`)
	var b strings.Builder
	b.WriteString(`INSERT INTO p VALUES `)
	for i := 0; i < 300; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "(%d, %d)", i, i%7)
	}
	execOK(t, s, b.String())
	got := execOK(t, s, `SELECT id FROM p WHERE v = 3`)
	want := execOK(t, s, `SELECT id FROM p WHERE v = 3 AND (SELECT COUNT(*) FROM p WHERE id = 0) = 1`)
	if strings.Join(canonicalRows(got), ",") != strings.Join(canonicalRows(want), ",") || len(got.Rows) == 0 {
		t.Fatalf("partitioned pushdown %d rows, oracle %d", len(got.Rows), len(want.Rows))
	}
}

func TestParallelSafePredicateRejectsSessionState(t *testing.T) {
	for sql, want := range map[string]bool{
		`SELECT 1 FROM t WHERE a = 1 AND UPPER(b) LIKE 'X%'`:     true,
		`SELECT 1 FROM t WHERE a IN (SELECT a FROM u)`:           false,
		`SELECT 1 FROM t WHERE a = (SELECT 1)`:                   false,
		`SELECT 1 FROM t WHERE EXISTS (SELECT 1 FROM u)`:         false,
		`SELECT 1 FROM t WHERE ts < NOW()`:                       false,
		`SELECT 1 FROM t WHERE HIGHLIGHT(b) = 'x'`:               false,
		`SELECT 1 FROM t WHERE CASE WHEN a > 1 THEN b END = 'x'`: true,
	} {
		stmt, err := parser.Parse(sql)
		if err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		sel, ok := stmt.(ast.Select)
		if !ok {
			t.Fatalf("%s did not parse as a SELECT", sql)
		}
		if got := parallelSafePredicate(sel.Where); got != want {
			t.Fatalf("parallelSafePredicate(%s) = %v, want %v", sql, got, want)
		}
	}
}
