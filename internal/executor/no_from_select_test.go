package executor

import (
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
	"testing"
)

// TestNoFromSelect covers `SELECT <expr-list>` with no FROM clause at all
// (CLAUDE.md's own "Run locally" quickstart uses `-c "SELECT 1"`, which
// otherwise cannot run — see TODO.md log #114/#115 for the finding and this
// fix). It bypasses the normal binder/planner entirely (no table, index, or
// catalog access), evaluating the select list once against no row context,
// the same architectural precedent as execSystemSelect's virtual tables.
func TestNoFromSelect(t *testing.T) {
	db := testDB(t)
	s := db.Session()

	res := execOK(t, s, `SELECT 1`)
	if len(res.Rows) != 1 || len(res.Rows[0]) != 1 || res.Rows[0][0].String() != "1" {
		t.Fatalf("SELECT 1: unexpected result %+v", res)
	}

	res = execOK(t, s, `SELECT 1 AS x, 2+2 AS y, 'hi' AS z`)
	if len(res.Columns) != 3 || res.Columns[0] != "x" || res.Columns[1] != "y" || res.Columns[2] != "z" {
		t.Fatalf("aliases not applied: %+v", res.Columns)
	}
	if len(res.Rows) != 1 || res.Rows[0][1].String() != "4" || res.Rows[0][2].String() != "hi" {
		t.Fatalf("unexpected values: %+v", res.Rows)
	}

	// Deterministic-at-call functions work (no table needed).
	res = execOK(t, s, `SELECT NOW()`)
	if len(res.Rows) != 1 || res.Rows[0][0].Typ.Kind != types.KindTimestampTZ {
		t.Fatalf("NOW(): unexpected result %+v", res)
	}
	res = execOK(t, s, `SELECT UUID()`)
	if len(res.Rows) != 1 || res.Rows[0][0].Typ.Kind != types.KindUUID {
		t.Fatalf("UUID(): unexpected result %+v", res)
	}

	// WHERE filters the single synthetic row to 0 or 1 rows.
	res = execOK(t, s, `SELECT 1 WHERE 1 = 0`)
	if len(res.Rows) != 0 {
		t.Fatalf("WHERE false: expected 0 rows, got %+v", res.Rows)
	}
	res = execOK(t, s, `SELECT 1 WHERE 1 = 1`)
	if len(res.Rows) != 1 {
		t.Fatalf("WHERE true: expected 1 row, got %+v", res.Rows)
	}

	// LIMIT/OFFSET apply trivially to the single row.
	res = execOK(t, s, `SELECT 1 LIMIT 0`)
	if len(res.Rows) != 0 {
		t.Fatalf("LIMIT 0: expected 0 rows, got %+v", res.Rows)
	}
	res = execOK(t, s, `SELECT 1 OFFSET 1`)
	if len(res.Rows) != 0 {
		t.Fatalf("OFFSET 1: expected 0 rows, got %+v", res.Rows)
	}

	// A column reference has no table to resolve against and fails closed,
	// not silently.
	if _, err := s.Exec(`SELECT id`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("SELECT id: expected InvalidArgument, got %v", err)
	}
	if _, err := s.Exec(`SELECT 1 ORDER BY id`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("ORDER BY id: expected InvalidArgument, got %v", err)
	}

	// SELECT * and table-only clauses still require FROM (parser-level).
	if _, err := s.Exec(`SELECT *`); !nerr.HasCode(err, nerr.Syntax) {
		t.Fatalf("SELECT *: expected Syntax error, got %v", err)
	}
	if _, err := s.Exec(`SELECT 1 GROUP BY 1`); !nerr.HasCode(err, nerr.Syntax) {
		t.Fatalf("GROUP BY: expected Syntax error, got %v", err)
	}

	// A real table-backed SELECT is completely unaffected.
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('hello')`)
	res = execOK(t, s, `SELECT n FROM t`)
	if len(res.Rows) != 1 || res.Rows[0][0].String() != "hello" {
		t.Fatalf("SELECT n FROM t: unexpected result %+v", res)
	}
}
