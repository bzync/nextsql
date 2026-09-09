package executor

import (
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
)

func newGroupSession(t *testing.T) *Session {
	t.Helper()
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sess := db.Session()
	// a=1 has two rows summing to 30; a=2 has one row summing to 1, so a
	// predicate over either aggregate can tell the groups apart.
	for _, q := range []string{
		"CREATE TABLE t (id INT64 PRIMARY KEY, a INT64, b INT64)",
		"INSERT INTO t (id,a,b) VALUES (1,1,10),(2,1,20),(3,2,1)",
	} {
		if _, err := sess.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return sess
}

func rowsOf(t *testing.T, sess *Session, sql string) [][]string {
	t.Helper()
	res, err := sess.Exec(sql)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	out := make([][]string, len(res.Rows))
	for i, r := range res.Rows {
		row := make([]string, len(r))
		for j, v := range r {
			row[j] = v.String()
		}
		out[i] = row
	}
	return out
}

// HAVING must accept the aggregate as written. A star aggregate used to be
// unreachable: select items are stored after rewriteQual, which rebuilds every
// call with an empty-but-non-nil Args slice, while the parser leaves Args nil —
// and reflect.DeepEqual separates those. So HAVING COUNT(*) never matched the
// selected COUNT(*) and fell through to per-row evaluation, which cannot
// evaluate an aggregate.
func TestHavingMatchesStarAggregate(t *testing.T) {
	sess := newGroupSession(t)
	got := rowsOf(t, sess, "SELECT a, COUNT(*) FROM t GROUP BY a HAVING COUNT(*) > 1")
	if len(got) != 1 || got[0][0] != "1" || got[0][1] != "2" {
		t.Fatalf("HAVING COUNT(*) > 1 = %v, want the single group [1 2]", got)
	}
}

// The non-star form, the output-column name and an alias must all keep working.
func TestHavingAcceptsEquivalentSpellings(t *testing.T) {
	sess := newGroupSession(t)
	for _, sql := range []string{
		"SELECT a, SUM(b) FROM t GROUP BY a HAVING SUM(b) > 5",
		"SELECT a, SUM(b) FROM t GROUP BY a HAVING sum > 5",
		"SELECT a, SUM(b) AS total FROM t GROUP BY a HAVING total > 5",
		// Qualified references work now that both sides are normalised the
		// same way before being compared.
		"SELECT a, SUM(b) FROM t GROUP BY t.a HAVING SUM(t.b) > 5",
	} {
		got := rowsOf(t, sess, sql)
		if len(got) != 1 || got[0][0] != "1" || got[0][1] != "30" {
			t.Fatalf("%s = %v, want the single group [1 30]", sql, got)
		}
	}
}

// Grouping on a computed expression is materialised into a real column before
// aggregating. The planner used to drop such a group instead, which does not
// disable grouping — it collapses every row into one group and answers from it,
// so GROUP BY a + b reported a single bogus row.
func TestGroupByComputedExpression(t *testing.T) {
	sess := newGroupSession(t)
	// a=1,b=10 / a=1,b=20 / a=2,b=1 -> a+b is 11, 21, 3: three distinct groups.
	got := rowsOf(t, sess, "SELECT a + b, COUNT(*) FROM t GROUP BY a + b")
	if len(got) != 3 {
		t.Fatalf("GROUP BY a + b = %v, want 3 groups", got)
	}
	seen := map[string]string{}
	for _, r := range got {
		seen[r[0]] = r[1]
	}
	for _, k := range []string{"11", "21", "3"} {
		if seen[k] != "1" {
			t.Fatalf("group %s = %q, want count 1 (got %v)", k, seen[k], got)
		}
	}
	// The grouping expression need not be selected, and an expression built on
	// top of it is still one value per group.
	if got := rowsOf(t, sess, "SELECT COUNT(*) FROM t GROUP BY a + b"); len(got) != 3 {
		t.Fatalf("unselected computed group = %v, want 3 rows", got)
	}
	if got := rowsOf(t, sess, "SELECT a + b + 1 FROM t GROUP BY a + b"); len(got) != 3 {
		t.Fatalf("expression over a computed group = %v, want 3 rows", got)
	}
	// Positional grouping is still not implemented, and must stay refused
	// rather than silently mean "no grouping".
	if _, err := sess.Exec("SELECT COUNT(*) FROM t GROUP BY 1"); err == nil {
		t.Fatal("GROUP BY 1 was accepted; positional grouping is not implemented")
	}
}

// A select item that is neither an aggregate nor a grouped column must be
// rejected. exprKey returns "" for any form it cannot identify, and comparing
// those for equality made two different unidentifiable expressions look like
// the same one — so an ungrouped item could pass as grouped.
func TestUngroupedSelectItemIsRejected(t *testing.T) {
	sess := newGroupSession(t)
	if _, err := sess.Exec("SELECT b FROM t GROUP BY a"); err == nil {
		t.Fatal("SELECT b ... GROUP BY a was accepted; b is neither aggregated nor grouped")
	}
}

// Grouping on a plain column is the supported shape and must be unaffected.
func TestGroupByColumnStillCorrect(t *testing.T) {
	sess := newGroupSession(t)
	got := rowsOf(t, sess, "SELECT a, COUNT(*), SUM(b) FROM t GROUP BY a")
	if len(got) != 2 {
		t.Fatalf("GROUP BY a = %v, want 2 groups", got)
	}
	for _, r := range got {
		switch r[0] {
		case "1":
			if r[1] != "2" || r[2] != "30" {
				t.Fatalf("group a=1 = %v, want count 2 sum 30", r)
			}
		case "2":
			if r[1] != "1" || r[2] != "1" {
				t.Fatalf("group a=2 = %v, want count 1 sum 1", r)
			}
		default:
			t.Fatalf("unexpected group %v", r)
		}
	}
}

// An aggregate argument that is a computed expression is materialised into a
// real column before aggregating. It used to be left at the ordinal -1, which
// already means COUNT(*): COUNT(a + b) counted every row rather than the
// non-NULL values of a + b, and MIN/MAX over an expression returned NULL.
func TestAggregateOverComputedExpression(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sess := db.Session()
	// Row 3 leaves a NULL, so a + b is NULL there: COUNT(a + b) is 2, while the
	// star reading it used to get is 3.
	for _, q := range []string{
		"CREATE TABLE t (id INT64 PRIMARY KEY, a INT64, b INT64)",
		"INSERT INTO t (id,a,b) VALUES (1,1,10),(2,2,20)",
		"INSERT INTO t (id,b) VALUES (3,30)",
	} {
		if _, err := sess.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, c := range []struct{ sql, want string }{
		{"SELECT COUNT(*) FROM t", "3"},
		{"SELECT COUNT(a + b) FROM t", "2"},
		{"SELECT SUM(a + b) FROM t", "33"},
		{"SELECT MIN(a + b) FROM t", "11"},
		{"SELECT MAX(a + b) FROM t", "22"},
		{"SELECT SUM(a * 2) FROM t", "6"},
	} {
		got := rowsOf(t, sess, c.sql)
		if len(got) != 1 || got[0][0] != c.want {
			t.Fatalf("%s = %v, want %s", c.sql, got, c.want)
		}
	}
	// The same expression used twice is computed once, and both aggregates
	// still see it.
	got := rowsOf(t, sess, "SELECT SUM(a + b), AVG(a + b) FROM t")
	if len(got) != 1 || got[0][0] != "33" {
		t.Fatalf("shared computed argument = %v, want sum 33", got)
	}
}

// An aggregate inside a larger expression is evaluated after aggregation,
// against the aggregated row. This used to return an *uninitialised* value —
// SELECT COUNT(*) + 1 answered an empty cell instead of 4, with no error —
// because there was no projection over the aggregated row at all.
func TestAggregateInsideExpressionIsEvaluated(t *testing.T) {
	sess := newGroupSession(t)
	for _, c := range []struct{ sql, want string }{
		{"SELECT COUNT(*) + 1 FROM t", "4"},
		{"SELECT MAX(a) + MIN(a) FROM t", "3"},
		{"SELECT -COUNT(*) FROM t", "-3"},
		{"SELECT CASE WHEN COUNT(*) > 2 THEN 'many' ELSE 'few' END FROM t", "many"},
	} {
		got := rowsOf(t, sess, c.sql)
		if len(got) != 1 || got[0][0] != c.want {
			t.Fatalf("%s = %v, want %s", c.sql, got, c.want)
		}
	}
	// Ordering matters: the second item must read the count, not the max. A
	// projection that mapped outputs to aggregate slots by position would get
	// this wrong, because the first item consumes a slot without being a bare
	// aggregate call.
	got := rowsOf(t, sess, "SELECT MAX(a) + 1, COUNT(*) FROM t")
	if len(got) != 1 || got[0][0] != "3" || got[0][1] != "3" {
		t.Fatalf("SELECT MAX(a) + 1, COUNT(*) = %v, want [3 3]", got)
	}
	// Per group, and alongside a grouped column.
	grouped := rowsOf(t, sess, "SELECT a, COUNT(*) + 1 FROM t GROUP BY a")
	if len(grouped) != 2 {
		t.Fatalf("grouped expression = %v, want 2 groups", grouped)
	}
	for _, r := range grouped {
		want := map[string]string{"1": "3", "2": "2"}[r[0]]
		if r[1] != want {
			t.Fatalf("group %s = %v, want %s", r[0], r, want)
		}
	}
}

// A window function is a different path that does project after evaluation, so
// it must keep working inside an expression. containsGroupingAgg ignores a
// window for exactly this reason, and that distinction is what the refusal
// above depends on.
func TestWindowInsideExpressionStillWorks(t *testing.T) {
	sess := newGroupSession(t)
	got := rowsOf(t, sess, "SELECT SUM(b) OVER () * 2 FROM t")
	if len(got) != 3 {
		t.Fatalf("windowed expression returned %v, want one row per input row", got)
	}
	for _, r := range got {
		if r[0] != "62" {
			t.Fatalf("SUM(b) OVER () * 2 = %v, want 62 on every row", got)
		}
	}
	// The window numbers by b, but the statement has no outer ORDER BY, so the
	// rows come back in scan order: assert the set of values, not their order.
	seen := map[string]bool{}
	for _, r := range rowsOf(t, sess, "SELECT ROW_NUMBER() OVER (ORDER BY b) + 1 FROM t") {
		seen[r[0]] = true
	}
	for _, want := range []string{"2", "3", "4"} {
		if !seen[want] {
			t.Fatalf("ROW_NUMBER() OVER (...) + 1 produced %v, missing %s", seen, want)
		}
	}
}
