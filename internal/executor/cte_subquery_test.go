package executor

import "testing"

// A CTE inside a subquery answered the wrong question. Correlation resolves an
// unqualified name by looking the FROM relation up as a *table*; a CTE name
// does not resolve that way, so the inner schema came back nil and every
// unqualified column was treated as a reference to the outer row and replaced
// by its value. `IN (WITH c AS (...) SELECT id FROM c)` therefore compared each
// row's id against itself and matched everything, and its NOT IN matched
// nothing.
func newCTESubquerySession(t *testing.T) *Session {
	t.Helper()
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE emp (id INT64 PRIMARY KEY, dept INT64)")
	mustExec(t, sess, "INSERT INTO emp VALUES (1,1),(2,1),(3,2)")
	return sess
}

func TestInSubqueryWithCTE(t *testing.T) {
	sess := newCTESubquerySession(t)
	cases := []struct {
		sql  string
		want []string
	}{
		{"SELECT id FROM emp WHERE id IN (WITH c AS (SELECT id FROM emp WHERE dept = 2) SELECT id FROM c) ORDER BY id", []string{"3"}},
		{"SELECT id FROM emp WHERE id NOT IN (WITH c AS (SELECT id FROM emp WHERE dept = 2) SELECT id FROM c) ORDER BY id", []string{"1", "2"}},
		{"SELECT id FROM emp WHERE id IN (WITH c AS (SELECT id FROM emp WHERE dept = 99) SELECT id FROM c) ORDER BY id", nil},
		// The plain forms must keep answering the same way.
		{"SELECT id FROM emp WHERE id IN (SELECT id FROM emp WHERE dept = 2) ORDER BY id", []string{"3"}},
		{"SELECT id FROM emp WHERE id NOT IN (SELECT id FROM emp WHERE dept = 2) ORDER BY id", []string{"1", "2"}},
	}
	for _, c := range cases {
		if got := idsOf(t, sess, c.sql); !eqStrings(got, c.want) {
			t.Fatalf("%s = %v, want %v", c.sql, got, c.want)
		}
	}
}

func TestExistsAndScalarSubqueryWithCTE(t *testing.T) {
	sess := newCTESubquerySession(t)
	if got := idsOf(t, sess, "SELECT id FROM emp WHERE EXISTS (WITH c AS (SELECT id FROM emp WHERE dept = 99) SELECT id FROM c) ORDER BY id"); len(got) != 0 {
		t.Fatalf("EXISTS over an empty CTE matched %v", got)
	}
	if got := idsOf(t, sess, "SELECT id FROM emp WHERE EXISTS (WITH c AS (SELECT id FROM emp WHERE dept = 2) SELECT id FROM c) ORDER BY id"); !eqStrings(got, []string{"1", "2", "3"}) {
		t.Fatalf("EXISTS over a non-empty CTE = %v", got)
	}
	got := rowsOf(t, sess, "SELECT (WITH c AS (SELECT 7 AS a FROM emp LIMIT 1) SELECT a FROM c) AS v FROM emp ORDER BY id LIMIT 1")
	if len(got) != 1 || got[0][0] != "7" {
		t.Fatalf("scalar subquery over a CTE = %v", got)
	}
}

// Correlation itself must keep working: a subquery over a real table still
// sees the outer row.
func TestCorrelatedSubqueryStillCorrelates(t *testing.T) {
	sess := newCTESubquerySession(t)
	got := idsOf(t, sess, "SELECT id FROM emp e WHERE EXISTS (SELECT 1 FROM emp x WHERE x.dept = e.dept AND x.id <> e.id) ORDER BY id")
	if !eqStrings(got, []string{"1", "2"}) {
		t.Fatalf("correlated EXISTS = %v, want [1 2]", got)
	}
	// An unqualified outer reference against a known inner schema still
	// correlates.
	got = idsOf(t, sess, "SELECT id FROM emp e WHERE (SELECT COUNT(*) FROM emp x WHERE x.dept = e.dept) > 1 ORDER BY id")
	if !eqStrings(got, []string{"1", "2"}) {
		t.Fatalf("correlated scalar subquery = %v, want [1 2]", got)
	}
}
