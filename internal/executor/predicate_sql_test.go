package executor

import (
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
)

// None of the SQL in this file parsed at all before: `x IN (1, 2)` was
// rejected with "IN currently requires a SELECT subquery", and LIKE and CAST
// did not exist as grammar.
func newPredicateSession(t *testing.T) *Session {
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
	for _, q := range []string{
		"CREATE TABLE t (id INT64 PRIMARY KEY, name STRING, n INT64)",
		"INSERT INTO t (id,name,n) VALUES (1,'alpha',10),(2,'beta',20),(3,'gamma',30)",
		"CREATE TABLE nn (id INT64 PRIMARY KEY, name STRING)",
		"INSERT INTO nn (id,name) VALUES (1,'x'),(2,NULL)",
	} {
		if _, err := sess.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return sess
}

func idsOf(t *testing.T, sess *Session, sql string) []string {
	t.Helper()
	rows := rowsOf(t, sess, sql)
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r[0]
	}
	return out
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestInValueList(t *testing.T) {
	sess := newPredicateSession(t)
	cases := []struct {
		sql  string
		want []string
	}{
		{"SELECT id FROM t WHERE id IN (1,3) ORDER BY id", []string{"1", "3"}},
		{"SELECT id FROM t WHERE id IN (2) ORDER BY id", []string{"2"}},
		{"SELECT id FROM t WHERE id IN (99) ORDER BY id", nil},
		{"SELECT id FROM t WHERE name IN ('alpha','gamma') ORDER BY id", []string{"1", "3"}},
		{"SELECT id FROM t WHERE id NOT IN (1,2) ORDER BY id", []string{"3"}},
		// An expression on either side is still just an expression.
		{"SELECT id FROM t WHERE n IN (10+10, 30) ORDER BY id", []string{"2", "3"}},
		{"SELECT id FROM t WHERE id + 1 IN (2,3) ORDER BY id", []string{"1", "2"}},
	}
	for _, c := range cases {
		if got := idsOf(t, sess, c.sql); !eqStrings(got, c.want) {
			t.Fatalf("%s = %v, want %v", c.sql, got, c.want)
		}
	}
}

// ISO/IEC 9075 defines `x IN (a, b)` as `x = a OR x = b`, so three-valued
// logic follows: an unmatched IN over a list holding NULL is UNKNOWN, and its
// NOT IN is UNKNOWN too — neither returns a row.
func TestInValueListNullSemantics(t *testing.T) {
	sess := newPredicateSession(t)
	cases := []struct {
		sql  string
		want []string
	}{
		{"SELECT id FROM t WHERE id IN (1, NULL) ORDER BY id", []string{"1"}},
		{"SELECT id FROM t WHERE id IN (99, NULL) ORDER BY id", nil},
		{"SELECT id FROM t WHERE id NOT IN (1, NULL) ORDER BY id", nil},
		{"SELECT id FROM t WHERE id NOT IN (99) ORDER BY id", []string{"1", "2", "3"}},
		// A NULL on the left is UNKNOWN for both directions.
		{"SELECT id FROM nn WHERE name IN ('x') ORDER BY id", []string{"1"}},
		{"SELECT id FROM nn WHERE name NOT IN ('x') ORDER BY id", nil},
	}
	for _, c := range cases {
		if got := idsOf(t, sess, c.sql); !eqStrings(got, c.want) {
			t.Fatalf("%s = %v, want %v", c.sql, got, c.want)
		}
	}
}

// The left operand is compared against each value, so one that changes per
// evaluation has no defined meaning and is refused rather than answered.
func TestInValueListRefusesVolatileLeftOperand(t *testing.T) {
	sess := newPredicateSession(t)
	if _, err := sess.Exec("SELECT id FROM t WHERE NOW() IN (1,2)"); err == nil {
		t.Fatal("NOW() IN (...) was accepted")
	}
	if _, err := sess.Exec("SELECT id FROM t WHERE UUID() IN ('a')"); err == nil {
		t.Fatal("UUID() IN (...) was accepted")
	}
}

func TestInSubqueryStillWorks(t *testing.T) {
	sess := newPredicateSession(t)
	got := idsOf(t, sess, "SELECT id FROM t WHERE id IN (SELECT id FROM t WHERE n > 15) ORDER BY id")
	if !eqStrings(got, []string{"2", "3"}) {
		t.Fatalf("IN (subquery) = %v", got)
	}
}

func TestLikePredicate(t *testing.T) {
	sess := newPredicateSession(t)
	cases := []struct {
		sql  string
		want []string
	}{
		{"SELECT id FROM t WHERE name LIKE 'alpha' ORDER BY id", []string{"1"}},
		{"SELECT id FROM t WHERE name LIKE 'a%' ORDER BY id", []string{"1"}},
		{"SELECT id FROM t WHERE name LIKE '%a' ORDER BY id", []string{"1", "2", "3"}},
		{"SELECT id FROM t WHERE name LIKE '%et%' ORDER BY id", []string{"2"}},
		{"SELECT id FROM t WHERE name LIKE '_eta' ORDER BY id", []string{"2"}},
		// Five characters ending in "a": alpha and gamma, not the
		// four-character beta.
		{"SELECT id FROM t WHERE name LIKE '____a' ORDER BY id", []string{"1", "3"}},
		{"SELECT id FROM t WHERE name LIKE '%' ORDER BY id", []string{"1", "2", "3"}},
		// LIKE is case-sensitive: NextSQL does not fold case behind the
		// operator the way a collation-driven engine might.
		{"SELECT id FROM t WHERE name LIKE 'A%' ORDER BY id", nil},
		{"SELECT id FROM t WHERE name NOT LIKE 'a%' ORDER BY id", []string{"2", "3"}},
		{"SELECT id FROM t WHERE LOWER(name) LIKE 'a%' ORDER BY id", []string{"1"}},
	}
	for _, c := range cases {
		if got := idsOf(t, sess, c.sql); !eqStrings(got, c.want) {
			t.Fatalf("%s = %v, want %v", c.sql, got, c.want)
		}
	}
}

func TestLikeEscapeAndNull(t *testing.T) {
	sess := newPredicateSession(t)
	for _, q := range []string{
		"CREATE TABLE lt (id INT64 PRIMARY KEY, s STRING)",
		"INSERT INTO lt (id,s) VALUES (1,'100%'),(2,'1000'),(3,'a_b'),(4,'axb')",
	} {
		if _, err := sess.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	cases := []struct {
		sql  string
		want []string
	}{
		{"SELECT id FROM lt WHERE s LIKE '100!%' ESCAPE '!' ORDER BY id", []string{"1"}},
		{"SELECT id FROM lt WHERE s LIKE 'a!_b' ESCAPE '!' ORDER BY id", []string{"3"}},
		{"SELECT id FROM lt WHERE s LIKE 'a_b' ORDER BY id", []string{"3", "4"}},
		// NULL anywhere in the predicate is UNKNOWN, not false.
		{"SELECT id FROM nn WHERE name LIKE '%' ORDER BY id", []string{"1"}},
		{"SELECT id FROM nn WHERE name NOT LIKE 'zz' ORDER BY id", []string{"1"}},
	}
	for _, c := range cases {
		if got := idsOf(t, sess, c.sql); !eqStrings(got, c.want) {
			t.Fatalf("%s = %v, want %v", c.sql, got, c.want)
		}
	}
	if _, err := sess.Exec("SELECT id FROM lt WHERE s LIKE 'a!b' ESCAPE '!'"); err == nil {
		t.Fatal("an invalid escape sequence was accepted")
	}
	if _, err := sess.Exec("SELECT id FROM lt WHERE s LIKE 'ab!' ESCAPE '!'"); err == nil {
		t.Fatal("a dangling escape was accepted")
	}
	if _, err := sess.Exec("SELECT id FROM lt WHERE s LIKE 'a' ESCAPE 'xy'"); err == nil {
		t.Fatal("a multi-character escape was accepted")
	}
}

// A pattern of alternating wildcards is where a backtracking regular-expression
// engine goes exponential. The matcher resumes from the last `%` instead, so
// this answers immediately.
func TestLikePathologicalPatternIsLinear(t *testing.T) {
	sess := newPredicateSession(t)
	if _, err := sess.Exec("CREATE TABLE big (id INT64 PRIMARY KEY, s STRING)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := sess.Exec("INSERT INTO big (id,s) VALUES (1,'" + strings.Repeat("a", 2000) + "b')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	pattern := strings.Repeat("%a", 200) + "%c"
	rows := rowsOf(t, sess, "SELECT id FROM big WHERE s LIKE '"+pattern+"'")
	if len(rows) != 0 {
		t.Fatalf("pathological pattern matched: %v", rows)
	}
}

func TestCast(t *testing.T) {
	sess := newPredicateSession(t)
	cases := []struct {
		sql  string
		want string
	}{
		{"SELECT CAST(n AS STRING) FROM t WHERE id = 1", "10"},
		{"SELECT CAST('42' AS INT64)", "42"},
		{"SELECT CAST(1 AS DECIMAL(10,2))", "1.00"},
		{"SELECT CAST(NULL AS STRING)", "NULL"},
		{"SELECT CAST(CAST('7' AS INT64) AS STRING)", "7"},
	}
	for _, c := range cases {
		got := rowsOf(t, sess, c.sql)
		if len(got) != 1 || got[0][0] != c.want {
			t.Fatalf("%s = %v, want %q", c.sql, got, c.want)
		}
	}
	// A conversion the engine does not accept is an error, not a silent zero.
	if _, err := sess.Exec("SELECT CAST('abc' AS INT64)"); err == nil {
		t.Fatal("CAST('abc' AS INT64) was accepted")
	}
}

// CAST is contextual, so a column named `cast` still resolves as a column.
func TestCastIsNotReserved(t *testing.T) {
	sess := newPredicateSession(t)
	if _, err := sess.Exec("CREATE TABLE ct (id INT64 PRIMARY KEY, cast STRING)"); err != nil {
		t.Fatalf("a column named cast was refused: %v", err)
	}
	if _, err := sess.Exec("INSERT INTO ct (id,cast) VALUES (1,'x')"); err != nil {
		t.Fatalf("insert into cast column: %v", err)
	}
	got := rowsOf(t, sess, "SELECT cast FROM ct WHERE id = 1")
	if len(got) != 1 || got[0][0] != "x" {
		t.Fatalf("SELECT cast = %v", got)
	}
}

// LIKE is reserved, as in the standard, and quoting is the documented way to
// keep using it as an identifier.
func TestLikeIsReservedButQuotable(t *testing.T) {
	sess := newPredicateSession(t)
	if _, err := sess.Exec(`CREATE TABLE qt (id INT64 PRIMARY KEY, "like" STRING)`); err != nil {
		t.Fatalf("a quoted column named like was refused: %v", err)
	}
	if _, err := sess.Exec(`INSERT INTO qt (id,"like") VALUES (1,'x')`); err != nil {
		t.Fatalf("insert into quoted like column: %v", err)
	}
	got := rowsOf(t, sess, `SELECT "like" FROM qt WHERE id = 1`)
	if len(got) != 1 || got[0][0] != "x" {
		t.Fatalf(`SELECT "like" = %v`, got)
	}
}
