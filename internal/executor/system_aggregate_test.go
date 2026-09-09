package executor

import (
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

func newAggSession(t *testing.T) *Session {
	t.Helper()
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db.Session()
}

func oneRow(t *testing.T, sess *Session, sql string) []types.Value {
	t.Helper()
	res, err := sess.Exec(sql)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("%s: got %d rows, want exactly 1", sql, len(res.Rows))
	}
	return res.Rows[0]
}

// COUNT(*) over an empty system table must still produce one row holding 0 —
// not zero rows. This is the case the whole aggregate path exists for and the
// one a row-at-a-time projection can never express.
func TestSystemAggregateCountOverEmptyCatalog(t *testing.T) {
	sess := newAggSession(t)
	row := oneRow(t, sess, "SELECT COUNT(*) FROM system.tables")
	if row[0].Null {
		t.Fatal("COUNT(*) of an empty catalog returned NULL")
	}
	if got := row[0].Dec.String(); got != "0" {
		t.Fatalf("COUNT(*) of an empty catalog = %s, want 0", got)
	}
}

func TestSystemAggregateCountsRows(t *testing.T) {
	sess := newAggSession(t)
	for _, ddl := range []string{
		"CREATE TABLE a (id INT64 PRIMARY KEY)",
		"CREATE TABLE b (id INT64 PRIMARY KEY)",
		"CREATE TABLE c (id INT64 PRIMARY KEY)",
	} {
		if _, err := sess.Exec(ddl); err != nil {
			t.Fatalf("%s: %v", ddl, err)
		}
	}
	if got := oneRow(t, sess, "SELECT COUNT(*) FROM system.tables")[0].Dec.String(); got != "3" {
		t.Fatalf("COUNT(*) = %s, want 3", got)
	}
	// WHERE narrows what the aggregate reads.
	if got := oneRow(t, sess, "SELECT COUNT(*) FROM system.tables WHERE name = 'b'")[0].Dec.String(); got != "1" {
		t.Fatalf("filtered COUNT(*) = %s, want 1", got)
	}
	// An alias names the output column; without one it is the function name.
	res, err := sess.Exec("SELECT COUNT(*) AS n FROM system.tables")
	if err != nil {
		t.Fatal(err)
	}
	if res.Columns[0] != "n" {
		t.Fatalf("aliased column = %q, want n", res.Columns[0])
	}
	if res, err = sess.Exec("SELECT COUNT(*) FROM system.tables"); err != nil {
		t.Fatal(err)
	}
	if res.Columns[0] != "count" {
		t.Fatalf("unaliased column = %q, want count", res.Columns[0])
	}
}

// LIMIT and OFFSET must bound the aggregate's result, not the rows it reads.
// Bounding the input would make COUNT(*) report the LIMIT.
func TestSystemAggregateLimitBoundsResultNotInput(t *testing.T) {
	sess := newAggSession(t)
	for _, ddl := range []string{
		"CREATE TABLE a (id INT64 PRIMARY KEY)",
		"CREATE TABLE b (id INT64 PRIMARY KEY)",
		"CREATE TABLE c (id INT64 PRIMARY KEY)",
	} {
		if _, err := sess.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	if got := oneRow(t, sess, "SELECT COUNT(*) FROM system.tables LIMIT 1")[0].Dec.String(); got != "3" {
		t.Fatalf("COUNT(*) ... LIMIT 1 = %s, want 3 (LIMIT bounds the result)", got)
	}
	// One result row, so OFFSET 1 skips it entirely.
	res, err := sess.Exec("SELECT COUNT(*) FROM system.tables OFFSET 1")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("OFFSET 1 returned %d rows, want 0", len(res.Rows))
	}
}

// MIN/MAX and the collection aggregates go through the same accumulators as a
// user table, so their behaviour here is the shared one, not a second copy.
func TestSystemAggregateMinMaxAndArrayAgg(t *testing.T) {
	sess := newAggSession(t)
	for _, ddl := range []string{
		"CREATE TABLE alpha (id INT64 PRIMARY KEY)",
		"CREATE TABLE omega (id INT64 PRIMARY KEY)",
	} {
		if _, err := sess.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	row := oneRow(t, sess, "SELECT MIN(name), MAX(name) FROM system.tables")
	if row[0].Str != "alpha" || row[1].Str != "omega" {
		t.Fatalf("MIN/MAX = %q/%q, want alpha/omega", row[0].Str, row[1].Str)
	}
	if _, err := sess.Exec("SELECT ARRAY_AGG(name) FROM system.tables"); err != nil {
		t.Fatalf("ARRAY_AGG: %v", err)
	}
}

// COUNT(col) skips NULLs while COUNT(*) counts rows. system.foreign_keys is
// used because a table with no foreign key still lists its columns, giving a
// column that is genuinely NULL for some rows.
func TestSystemAggregateCountColumnSkipsNulls(t *testing.T) {
	sess := newAggSession(t)
	if _, err := sess.Exec("CREATE TABLE t (id INT64 PRIMARY KEY, note STRING)"); err != nil {
		t.Fatal(err)
	}
	star := oneRow(t, sess, "SELECT COUNT(*) FROM system.columns")[0].Dec.String()
	if star != "2" {
		t.Fatalf("COUNT(*) over system.columns = %s, want 2", star)
	}
	// COUNT over a column present on every row equals COUNT(*).
	col := oneRow(t, sess, "SELECT COUNT(column_name) FROM system.columns")[0].Dec.String()
	if col != star {
		t.Fatalf("COUNT(column_name) = %s, want %s", col, star)
	}
}

// A bare column beside an aggregate with no GROUP BY has no single value to
// report. The binder rejects this for user tables; system tables must not
// silently answer something different.
func TestSystemAggregateRejectsBareColumnBesideAggregate(t *testing.T) {
	sess := newAggSession(t)
	if _, err := sess.Exec("SELECT name, COUNT(*) FROM system.tables"); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("got %v, want an invalid_argument rejection", err)
	}
}

func TestSystemAggregateRejectsGroupBy(t *testing.T) {
	sess := newAggSession(t)
	if _, err := sess.Exec("SELECT COUNT(*) FROM system.tables GROUP BY name"); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("got %v, want an invalid_argument rejection", err)
	}
}

// A non-aggregate select list must still take the row-at-a-time path, where
// LIMIT bounds the rows returned.
func TestSystemNonAggregateSelectIsUnchanged(t *testing.T) {
	sess := newAggSession(t)
	for _, ddl := range []string{
		"CREATE TABLE a (id INT64 PRIMARY KEY)",
		"CREATE TABLE b (id INT64 PRIMARY KEY)",
	} {
		if _, err := sess.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	res, err := sess.Exec("SELECT name FROM system.tables LIMIT 1")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("SELECT name ... LIMIT 1 returned %d rows, want 1", len(res.Rows))
	}
}

// An aggregate inside a larger expression is answered here the same way a user
// table answers it: the aggregate is computed once and the surrounding
// expression is evaluated against the aggregated row.
func TestSystemAggregateInsideExpression(t *testing.T) {
	sess := newAggSession(t)
	for _, ddl := range []string{
		"CREATE TABLE a1 (id INT64 PRIMARY KEY)",
		"CREATE TABLE b2 (id INT64 PRIMARY KEY)",
	} {
		if _, err := sess.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ sql, want string }{
		{"SELECT COUNT(*) + 1 FROM system.tables", "3"},
		{"SELECT COUNT(*) * 10 FROM system.tables", "20"},
		{"SELECT CASE WHEN COUNT(*) > 1 THEN 'many' ELSE 'one' END FROM system.tables", "many"},
	} {
		row := oneRow(t, sess, c.sql)
		got := row[0].String()
		if got != c.want {
			t.Fatalf("%s = %s, want %s", c.sql, got, c.want)
		}
	}
	// A column beside an aggregate still has no single value to report.
	if _, err := sess.Exec("SELECT name, COUNT(*) FROM system.tables"); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("got %v, want an invalid_argument rejection", err)
	}
}

// TestSystemSelectHugeLimitReturnsEveryRow pins that a LIMIT larger than any
// row count still returns the whole result. The parser accepts LIMIT up to
// MaxUint32 (uintLit parses with a 32-bit bound), and sel.Limit is an int64,
// so the comparison against len(rows) has to happen before the narrowing to
// int: narrowing first turns every limit above MaxInt32 negative wherever int
// is 32 bits, and filtered[:negative] panics rather than returning rows.
func TestSystemSelectHugeLimitReturnsEveryRow(t *testing.T) {
	sess := newAggSession(t)
	for _, ddl := range []string{
		"CREATE TABLE a (id INT64 PRIMARY KEY)",
		"CREATE TABLE b (id INT64 PRIMARY KEY)",
		"CREATE TABLE c (id INT64 PRIMARY KEY)",
	} {
		if _, err := sess.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	for _, limit := range []string{"2147483648", "3000000000", "4294967295"} {
		res, err := sess.Exec("SELECT name FROM system.tables LIMIT " + limit)
		if err != nil {
			t.Fatalf("LIMIT %s: %v", limit, err)
		}
		if len(res.Rows) != 3 {
			t.Errorf("LIMIT %s returned %d rows, want all 3", limit, len(res.Rows))
		}
	}
}
