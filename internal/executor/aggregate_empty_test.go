package executor

import (
	"path/filepath"
	"testing"
)

// TestUngroupedAggregateOverEmptyInput pins SQL's rule for an aggregate with
// no GROUP BY: it is defined over one implicit group covering the whole input,
// so it reports exactly one row even when nothing matched — COUNT is 0, every
// other aggregate is NULL. Both ways of emptying the input are covered,
// because they take different executor paths: a filter that matches nothing at
// runtime goes through hash aggregation, while a constant-false filter is
// folded to planner.Empty and short-circuits before it. Returning zero rows
// here reaches drivers as "no rows in result set" instead of a count of 0.
func TestUngroupedAggregateOverEmptyInput(t *testing.T) {
	dir := t.TempDir()
	db, err := Create(filepath.Join(dir, "nextsql.db"), testKeys(t), 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE items (id INT64 PRIMARY KEY, name STRING NOT NULL, n INT64)`)
	execOK(t, s, `INSERT INTO items (id, name, n) VALUES (1, 'alpha', 10)`)
	execOK(t, s, `INSERT INTO items (id, name, n) VALUES (2, 'beta', 20)`)

	for _, q := range []struct {
		label string
		sql   string
	}{
		{"runtime-empty filter", `SELECT COUNT(*), COUNT(name), SUM(n), AVG(n), MIN(name), MAX(name) FROM items WHERE name = 'nope'`},
		{"constant-false filter", `SELECT COUNT(*), COUNT(name), SUM(n), AVG(n), MIN(name), MAX(name) FROM items WHERE 1 = 0`},
	} {
		got, err := s.Exec(q.sql)
		if err != nil {
			t.Fatalf("%s: %v", q.label, err)
		}
		if len(got.Rows) != 1 {
			t.Fatalf("%s: got %d rows, want exactly 1", q.label, len(got.Rows))
		}
		row := got.Rows[0]
		if len(row) != 6 {
			t.Fatalf("%s: got %d columns, want 6", q.label, len(row))
		}
		if row[0].Null || row[0].Dec.String() != "0" {
			t.Fatalf("%s: COUNT(*) = %v, want 0", q.label, row[0])
		}
		if row[1].Null || row[1].Dec.String() != "0" {
			t.Fatalf("%s: COUNT(name) = %v, want 0", q.label, row[1])
		}
		for i, name := range []string{"SUM", "AVG", "MIN", "MAX"} {
			if !row[i+2].Null {
				t.Fatalf("%s: %s = %v, want NULL", q.label, name, row[i+2])
			}
		}
	}

	// A GROUP BY has no group to report on empty input, so it stays zero rows.
	for _, q := range []string{
		`SELECT name, COUNT(*) FROM items WHERE name = 'nope' GROUP BY name`,
		`SELECT name, COUNT(*) FROM items WHERE 1 = 0 GROUP BY name`,
	} {
		got, err := s.Exec(q)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Rows) != 0 {
			t.Fatalf("grouped aggregate over empty input returned %d rows, want 0: %s", len(got.Rows), q)
		}
	}

	// Matching input is unaffected.
	got, err := s.Exec(`SELECT COUNT(*), SUM(n) FROM items WHERE name = 'alpha'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || got.Rows[0][0].Dec.String() != "1" || got.Rows[0][1].Dec.String() != "10" {
		t.Fatalf("non-empty aggregate regressed: %+v", got.Rows)
	}
}
