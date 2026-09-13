package executor

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/scheduler"
	sqltypes "github.com/bzync/nextsql/internal/sql/types"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

// `CREATE TABLE name PRIMARY KEY (cols) AS <query>` fills a new table from a
// query and takes the column names and types from that query's output. The key
// is named explicitly: every NextSQL table is clustered on its primary key and
// a query's output carries none.

func newCTASSession(t *testing.T) *Session {
	t.Helper()
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE src (id INT64 PRIMARY KEY, name STRING, qty INT64)")
	mustExec(t, sess, "INSERT INTO src VALUES (1,'a',10),(2,'b',20),(3,'c',30)")
	return sess
}

func colTypes(t *testing.T, sess *Session, table string) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, r := range rowsOf(t, sess, "SELECT column_name, type FROM system.columns WHERE table_name = '"+table+"'") {
		got[r[0]] = r[1]
	}
	return got
}

func TestCreateTableAsCopiesRowsAndInfersTypes(t *testing.T) {
	sess := newCTASSession(t)
	mustExec(t, sess, "CREATE TABLE copy1 PRIMARY KEY (id) AS SELECT id, name, qty FROM src")
	got := rowsOf(t, sess, "SELECT id, name, qty FROM copy1 ORDER BY id")
	want := [][]string{{"1", "a", "10"}, {"2", "b", "20"}, {"3", "c", "30"}}
	if len(got) != len(want) {
		t.Fatalf("copied %v, want %v", got, want)
	}
	for i := range want {
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Fatalf("copied %v, want %v", got, want)
			}
		}
	}
	types := colTypes(t, sess, "copy1")
	for col, want := range map[string]string{"id": "INT64", "name": "STRING", "qty": "INT64"} {
		if types[col] != want {
			t.Fatalf("copy1.%s type = %q, want %q (all: %v)", col, types[col], want, types)
		}
	}
}

// The row count is reported, so an operator can see how much was stored
// without a follow-up count.
func TestCreateTableAsReportsRowCount(t *testing.T) {
	sess := newCTASSession(t)
	res, err := sess.Exec("CREATE TABLE copy2 PRIMARY KEY (id) AS SELECT id, qty FROM src WHERE qty >= 20")
	if err != nil {
		t.Fatal(err)
	}
	if res.Affected != 2 {
		t.Fatalf("affected = %d, want 2", res.Affected)
	}
}

// The named key really is the table's key: it is NOT NULL, it is clustered,
// and a duplicate is refused afterwards.
func TestCreateTableAsKeyIsARealPrimaryKey(t *testing.T) {
	sess := newCTASSession(t)
	mustExec(t, sess, "CREATE TABLE keyed PRIMARY KEY (id) AS SELECT id, name FROM src")
	wantExecError(t, sess, "INSERT INTO keyed VALUES (1, 'dup')", "duplicate")
	wantExecError(t, sess, "INSERT INTO keyed VALUES (NULL, 'x')", "NULL")
	// Clustered order comes from the key, with no ORDER BY in the query.
	got := rowsOf(t, sess, "SELECT id FROM keyed")
	if len(got) != 3 || got[0][0] != "1" || got[2][0] != "3" {
		t.Fatalf("key order = %v", got)
	}
}

// An expression's type is the type its values carry; there is no static
// expression typer to consult, which is exactly why the schema is derived
// from the result.
func TestCreateTableAsInfersExpressionTypes(t *testing.T) {
	sess := newCTASSession(t)
	mustExec(t, sess, `CREATE TABLE derived PRIMARY KEY (id) AS
		SELECT id, qty * 2 AS twice, UPPER(name) AS upper_name, name AS copied FROM src`)
	types := colTypes(t, sess, "derived")
	// twice is DECIMAL, not INT64: this engine's arithmetic over integers is
	// exact-decimal, and its precision comes from the values. The point of the
	// assertion is that the stored column is what the query's values actually
	// are, not a guess.
	for col, want := range map[string]string{"id": "INT64", "twice": "DECIMAL", "upper_name": "STRING", "copied": "STRING"} {
		if types[col] != want {
			t.Fatalf("derived.%s type = %q, want %q (all: %v)", col, types[col], want, types)
		}
	}
	got := rowsOf(t, sess, "SELECT id, twice, upper_name, copied FROM derived ORDER BY id")
	if got[0][1] != "20" || got[0][2] != "A" || got[2][1] != "60" {
		t.Fatalf("derived rows = %v", got)
	}
}

// An unaliased expression reports its name as "?", which no column may be
// called. The fix is for the operator to name it, so the error says so.
func TestCreateTableAsRefusesUnnamedOutputColumn(t *testing.T) {
	sess := newCTASSession(t)
	wantExecError(t, sess, "CREATE TABLE bad PRIMARY KEY (id) AS SELECT id, qty + 1 FROM src", "alias")
	if _, ok := sess.lookup("bad"); ok {
		t.Fatal("table created despite the refusal")
	}
}

func TestCreateTableAsRefusesRepeatedOutputName(t *testing.T) {
	sess := newCTASSession(t)
	wantExecError(t, sess, "CREATE TABLE bad PRIMARY KEY (id) AS SELECT id, id FROM src", "repeated")
}

// No rows means no values, and no values means no type to derive. Inventing
// one would create a table shape the operator never asked for.
func TestCreateTableAsRefusesEmptyResult(t *testing.T) {
	sess := newCTASSession(t)
	wantExecError(t, sess, "CREATE TABLE bad PRIMARY KEY (id) AS SELECT id, name FROM src WHERE qty > 9999", "no rows")
	if _, ok := sess.lookup("bad"); ok {
		t.Fatal("table created from an empty result")
	}
}

// A NULL carries its type, so a column that is NULL in every row is still
// typeable and is accepted. What cannot be typed is the bare NULL literal.
func TestCreateTableAsTypesAnAllNullColumnFromItsSource(t *testing.T) {
	sess := newCTASSession(t)
	mustExec(t, sess, "CREATE TABLE holes (id INT64 PRIMARY KEY, maybe STRING)")
	mustExec(t, sess, "INSERT INTO holes VALUES (1, NULL), (2, NULL)")
	mustExec(t, sess, "CREATE TABLE nulls PRIMARY KEY (id) AS SELECT id, maybe FROM holes")
	if types := colTypes(t, sess, "nulls"); types["maybe"] != "STRING" {
		t.Fatalf("nulls.maybe type = %q, want STRING", types["maybe"])
	}
	if got := rowsOf(t, sess, "SELECT maybe FROM nulls"); len(got) != 2 || got[0][0] != "NULL" {
		t.Fatalf("nulls rows = %v", got)
	}
}

// A bare NULL literal has no type to derive, so it is refused -- and the CAST
// the error recommends genuinely supplies one.
func TestCreateTableAsRefusesUntypedNullColumn(t *testing.T) {
	sess := newCTASSession(t)
	wantExecError(t, sess, "CREATE TABLE bad PRIMARY KEY (id) AS SELECT id, NULL AS nothing FROM src", "untyped NULL")
	if _, ok := sess.lookup("bad"); ok {
		t.Fatal("table created from an untyped NULL column")
	}
	mustExec(t, sess, "CREATE TABLE fixed PRIMARY KEY (id) AS SELECT id, CAST(NULL AS STRING) AS nothing FROM src")
	if types := colTypes(t, sess, "fixed"); types["nothing"] != "STRING" {
		t.Fatalf("fixed.nothing type = %q, want STRING", types["nothing"])
	}
}

// A NULL in a column named as the key cannot be stored, so it is refused
// before the table is created rather than after.
func TestCreateTableAsRefusesNullPrimaryKey(t *testing.T) {
	sess := newCTASSession(t)
	mustExec(t, sess, "CREATE TABLE holes (id INT64 PRIMARY KEY, maybe STRING)")
	mustExec(t, sess, "INSERT INTO holes VALUES (1, 'x'), (2, NULL)")
	wantExecError(t, sess, "CREATE TABLE bad PRIMARY KEY (maybe) AS SELECT maybe, id FROM holes", "PRIMARY KEY")
	if _, ok := sess.lookup("bad"); ok {
		t.Fatal("table created with a NULL key")
	}
}

func TestCreateTableAsRefusesKeyThatIsNotAnOutputColumn(t *testing.T) {
	sess := newCTASSession(t)
	wantExecError(t, sess, "CREATE TABLE bad PRIMARY KEY (missing) AS SELECT id, name FROM src", "missing")
	wantExecError(t, sess, "CREATE TABLE bad PRIMARY KEY (qty) AS SELECT id, name FROM src", "qty")
}

func TestCreateTableAsRefusesRepeatedKeyColumn(t *testing.T) {
	sess := newCTASSession(t)
	wantExecError(t, sess, "CREATE TABLE bad PRIMARY KEY (id, id) AS SELECT id, name FROM src", "repeated")
}

// Without a key there is nothing to cluster the table on, and the grammar
// says so rather than inventing a rowid.
func TestCreateTableAsRequiresAKey(t *testing.T) {
	sess := newCTASSession(t)
	// The standard CTAS spelling omits the key, so the error has to say what
	// is missing rather than complain about a column list the operator never
	// meant to write.
	wantExecError(t, sess, "CREATE TABLE bad AS SELECT id FROM src", "requires PRIMARY KEY")
}

func TestCreateTableAsRefusesAnExistingName(t *testing.T) {
	sess := newCTASSession(t)
	if _, err := sess.Exec("CREATE TABLE src PRIMARY KEY (id) AS SELECT id FROM src"); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("recreating an existing table: %v", err)
	}
}

func TestCreateTableAsRefusesReservedName(t *testing.T) {
	sess := newCTASSession(t)
	wantExecError(t, sess, "CREATE TABLE nsql_x PRIMARY KEY (id) AS SELECT id FROM src", "reserved")
}

// A FROM-less SELECT is served by a restricted evaluator outside the query
// pipeline, so it is refused by name instead of through an obscure
// "unknown table".
func TestCreateTableAsRefusesFromLessSelect(t *testing.T) {
	sess := newCTASSession(t)
	wantExecError(t, sess, "CREATE TABLE bad PRIMARY KEY (n) AS SELECT 1 AS n", "reads a relation")
}

// The source is an ordinary query, so every shape a query can take works
// without a second grammar or a second planner path.
func TestCreateTableAsAcceptsOrdinaryQuerySources(t *testing.T) {
	sess := newCTASSession(t)
	mustExec(t, sess, "CREATE TABLE other (id INT64 PRIMARY KEY, tag STRING)")
	mustExec(t, sess, "INSERT INTO other VALUES (1,'x'),(2,'y')")
	mustExec(t, sess, "CREATE VIEW big AS SELECT id, qty FROM src WHERE qty >= 20")

	for _, tc := range []struct {
		name, sql string
		rows      int
	}{
		{"join", "CREATE TABLE j PRIMARY KEY (id) AS SELECT s.id AS id, o.tag AS tag FROM src s JOIN other o ON s.id = o.id", 2},
		{"aggregate", "CREATE TABLE g PRIMARY KEY (name) AS SELECT name, COUNT(*) AS n FROM src GROUP BY name", 3},
		{"cte", "CREATE TABLE c PRIMARY KEY (id) AS WITH w AS (SELECT id, qty FROM src WHERE qty > 10) SELECT id, qty FROM w", 2},
		{"set operation", "CREATE TABLE u PRIMARY KEY (id) AS SELECT id FROM src UNION SELECT id FROM other", 3},
		{"view", "CREATE TABLE v PRIMARY KEY (id) AS SELECT id, qty FROM big", 2},
		{"order and limit", "CREATE TABLE l PRIMARY KEY (id) AS SELECT id, qty FROM src ORDER BY qty DESC LIMIT 2", 2},
		{"subquery", "CREATE TABLE sq PRIMARY KEY (id) AS SELECT id, name FROM src WHERE id IN (SELECT id FROM other)", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mustExec(t, sess, tc.sql)
			table := strings.Fields(tc.sql)[2]
			if got := rowsOf(t, sess, "SELECT * FROM "+table); len(got) != tc.rows {
				t.Fatalf("%s produced %d rows, want %d (%v)", tc.name, len(got), tc.rows, got)
			}
		})
	}
}

// It is a snapshot, not a view: the rows are stored, so later changes to the
// source do not reach it. This is the difference an operator has to be able
// to rely on when choosing between the two.
func TestCreateTableAsIsASnapshotNotAView(t *testing.T) {
	sess := newCTASSession(t)
	mustExec(t, sess, "CREATE TABLE snap PRIMARY KEY (id) AS SELECT id, qty FROM src")
	mustExec(t, sess, "UPDATE src SET qty = 999 WHERE id = 1")
	mustExec(t, sess, "INSERT INTO src VALUES (4, 'd', 40)")
	got := rowsOf(t, sess, "SELECT id, qty FROM snap ORDER BY id")
	if len(got) != 3 || got[0][1] != "10" {
		t.Fatalf("snapshot changed with its source: %v", got)
	}
}

// Copying a table into itself is not expressible -- the target does not exist
// when the source runs -- but the source may read the table a sibling CTAS
// just created.
func TestCreateTableAsCanReadAPreviousCTASTable(t *testing.T) {
	sess := newCTASSession(t)
	mustExec(t, sess, "CREATE TABLE first PRIMARY KEY (id) AS SELECT id, qty FROM src")
	mustExec(t, sess, "CREATE TABLE second PRIMARY KEY (id) AS SELECT id, qty FROM first")
	if got := rowsOf(t, sess, "SELECT id FROM second ORDER BY id"); len(got) != 3 {
		t.Fatalf("second = %v", got)
	}
}

// The statement is atomic: a source whose key column repeats cannot be stored,
// and the table must not be left behind half-created.
func TestCreateTableAsRollsBackTheTableOnAFailedWrite(t *testing.T) {
	sess := newCTASSession(t)
	mustExec(t, sess, "CREATE TABLE dupes (id INT64 PRIMARY KEY, tag STRING)")
	mustExec(t, sess, "INSERT INTO dupes VALUES (1,'same'),(2,'same')")
	wantExecError(t, sess, "CREATE TABLE bad PRIMARY KEY (tag) AS SELECT tag, id FROM dupes", "duplicate")
	if _, ok := sess.lookup("bad"); ok {
		t.Fatal("the table survived a failed CREATE TABLE ... AS")
	}
	if _, err := sess.Exec("SELECT * FROM bad"); err == nil {
		t.Fatal("the failed table is still queryable")
	}
	// The name is free again, so a corrected statement works.
	mustExec(t, sess, "CREATE TABLE bad PRIMARY KEY (id) AS SELECT id, tag FROM dupes")
}

// A rolled-back transaction takes the new table with it.
func TestCreateTableAsRollsBackWithItsTransaction(t *testing.T) {
	sess := newCTASSession(t)
	mustExec(t, sess, "BEGIN")
	mustExec(t, sess, "CREATE TABLE staged PRIMARY KEY (id) AS SELECT id, qty FROM src")
	if got := rowsOf(t, sess, "SELECT id FROM staged"); len(got) != 3 {
		t.Fatalf("in-transaction rows = %v", got)
	}
	mustExec(t, sess, "ROLLBACK")
	if _, err := sess.Exec("SELECT * FROM staged"); err == nil {
		t.Fatal("the table survived ROLLBACK")
	}
}

// Catalog and rows are both recovered, so the table is there after a restart
// with the rows it was created with.
func TestCreateTableAsSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sess := db.Session()
	mustExec(t, sess, "CREATE TABLE src (id INT64 PRIMARY KEY, name STRING, qty INT64)")
	mustExec(t, sess, "INSERT INTO src VALUES (1,'a',10),(2,'b',20)")
	mustExec(t, sess, "CREATE TABLE kept PRIMARY KEY (id) AS SELECT id, name, qty * 3 AS tripled FROM src")
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	db2, err := Open(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	s2 := db2.Session()
	got := rowsOf(t, s2, "SELECT id, name, tripled FROM kept ORDER BY id")
	if len(got) != 2 || got[0][2] != "30" || got[1][2] != "60" {
		t.Fatalf("after reopen = %v", got)
	}
	if types := colTypes(t, s2, "kept"); types["tripled"] != "DECIMAL" {
		t.Fatalf("recovered tripled type = %q", types["tripled"])
	}
	// The recovered table is writable and still keyed.
	mustExec(t, s2, "INSERT INTO kept VALUES (3, 'c', 90)")
	if _, err := s2.Exec("INSERT INTO kept VALUES (3, 'dup', 0)"); err == nil {
		t.Fatal("the recovered key is not enforced")
	}
}

// CREATE is not enough: the source is read with the invoker's own rights, so
// a user who cannot read a table cannot copy it into one they own.
func TestCreateTableAsRequiresSelectOnTheSource(t *testing.T) {
	db := testDB(t)
	local := db.Session()
	execOK(t, local, `CREATE TABLE secret (id INT64 PRIMARY KEY, v STRING NOT NULL)`)
	execOK(t, local, `INSERT INTO secret VALUES (1, 'classified')`)

	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	if err := acl.Grant("app", security.PrivCreate, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Exec(`CREATE TABLE stolen PRIMARY KEY (id) AS SELECT id, v FROM secret`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("CREATE TABLE ... AS must require SELECT on the source: %v", err)
	}
	if _, ok := local.lookup("stolen"); ok {
		t.Fatal("the refused table was created anyway")
	}
	if err := acl.Grant("app", security.PrivSelect, security.ScopeTable, "secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Exec(`CREATE TABLE stolen PRIMARY KEY (id) AS SELECT id, v FROM secret`); err != nil {
		t.Fatalf("granted CREATE TABLE ... AS: %v", err)
	}
}

// Reading is authorized per relation, so a join cannot smuggle in a table the
// user may not read.
func TestCreateTableAsAuthorizesEveryJoinedSource(t *testing.T) {
	db := testDB(t)
	local := db.Session()
	execOK(t, local, `CREATE TABLE openish (id INT64 PRIMARY KEY)`)
	execOK(t, local, `INSERT INTO openish VALUES (1)`)
	execOK(t, local, `CREATE TABLE closed (id INT64 PRIMARY KEY, v STRING NOT NULL)`)
	execOK(t, local, `INSERT INTO closed VALUES (1, 'no')`)

	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	for _, g := range []struct {
		priv  security.Privilege
		scope security.ScopeKind
		name  string
	}{
		{security.PrivCreate, security.ScopeDatabase, ""},
		{security.PrivSelect, security.ScopeTable, "openish"},
	} {
		if err := acl.Grant("app", g.priv, g.scope, g.name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.Exec(`CREATE TABLE j PRIMARY KEY (id) AS SELECT o.id AS id, c.v AS v FROM openish o JOIN closed c ON o.id = c.id`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("a joined source must require its own SELECT: %v", err)
	}
}

// Creating a table still needs CREATE, even when every source is readable.
func TestCreateTableAsRequiresCreate(t *testing.T) {
	db := testDB(t)
	local := db.Session()
	execOK(t, local, `CREATE TABLE readable (id INT64 PRIMARY KEY)`)
	execOK(t, local, `INSERT INTO readable VALUES (1)`)

	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	if err := acl.Grant("app", security.PrivSelect, security.ScopeTable, "readable"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Exec(`CREATE TABLE t PRIMARY KEY (id) AS SELECT id FROM readable`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("CREATE TABLE ... AS must require CREATE: %v", err)
	}
}

// Whatever shape CTAS derives has to be a shape the dialect can express, or
// the new table would be one no operator could have written and no canonical
// DDL could render. system.table_ddl is the check: its rendering must rebuild
// an identical table.
func TestCreateTableAsProducesExpressibleDDL(t *testing.T) {
	sess := newCTASSession(t)
	mustExec(t, sess, `CREATE TABLE shaped PRIMARY KEY (id) AS
		SELECT id, qty * 2 AS twice, UPPER(name) AS upper_name FROM src`)
	got := rowsOf(t, sess, "SELECT ddl FROM system.table_ddl WHERE table_name = 'shaped'")
	if len(got) != 1 {
		t.Fatalf("no canonical DDL for a CTAS table: %v", got)
	}
	ddl := got[0][0]
	if !strings.Contains(ddl, "PRIMARY KEY") {
		t.Fatalf("rendered DDL lost the key: %s", ddl)
	}
	// Re-create under a second name from the rendered DDL: it must be accepted
	// and describe the same columns.
	mustExec(t, sess, strings.Replace(ddl, "shaped", "shaped2", 1))
	a, b := colTypes(t, sess, "shaped"), colTypes(t, sess, "shaped2")
	if len(a) != len(b) {
		t.Fatalf("round-tripped DDL changed the shape: %v vs %v", a, b)
	}
	for col, typ := range a {
		if b[col] != typ {
			t.Fatalf("round-tripped %s = %q, want %q (%s)", col, b[col], typ, ddl)
		}
	}
}

// A comparison yields BOOL. Until BOOL became a declarable column type (log
// #284) this statement had to be refused, because the derived table's own
// canonical DDL would not re-parse. It is now an ordinary column.
func TestCreateTableAsDerivesABoolColumn(t *testing.T) {
	sess := newCTASSession(t)
	mustExec(t, sess, "CREATE TABLE flagged PRIMARY KEY (id) AS SELECT id, qty > 15 AS big FROM src")
	if types := colTypes(t, sess, "flagged"); types["big"] != "BOOL" {
		t.Fatalf("flagged.big type = %q, want BOOL", types["big"])
	}
	got := rowsOf(t, sess, "SELECT id FROM flagged WHERE big ORDER BY id")
	if len(got) != 2 || got[0][0] != "2" || got[1][0] != "3" {
		t.Fatalf("WHERE big returned %v, want ids 2 and 3", got)
	}
}

// The DDL round-trip guard stays even though no query-derivable type currently
// trips it: it is what stops a type added later from reintroducing a table that
// can be created but not exported. No statement can reach it today, so it is
// exercised directly with the one kind that has no declarable spelling.
func TestCheckExpressibleAsDDLRefusesAnUndeclarableType(t *testing.T) {
	tab := &catalog.Table{Name: "bad", Columns: []catalog.Column{
		{Name: "id", Type: sqltypes.Int64(), NotNull: true, Primary: true},
		{Name: "nothing", Type: sqltypes.NullType()},
	}}
	err := checkExpressibleAsDDL(tab)
	if err == nil || !strings.Contains(err.Error(), "nothing") {
		t.Fatalf("checkExpressibleAsDDL = %v, want a refusal naming the column", err)
	}
	tab.Columns[1].Type = sqltypes.Bool()
	if err := checkExpressibleAsDDL(tab); err != nil {
		t.Fatalf("a BOOL column no longer round-trips: %v", err)
	}
}

// A computed decimal keeps every digit its values carry: the scale is read
// from the values, so nothing is rounded away on the way into the table.
func TestCreateTableAsKeepsComputedDecimalDigits(t *testing.T) {
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE money (id INT64 PRIMARY KEY, amt DECIMAL(12,2))")
	mustExec(t, sess, "INSERT INTO money VALUES (1, 10.25), (2, 3.50)")
	mustExec(t, sess, "CREATE TABLE doubled PRIMARY KEY (id) AS SELECT id, amt * 2 AS total FROM money")
	got := rowsOf(t, sess, "SELECT id, total FROM doubled ORDER BY id")
	if len(got) != 2 || got[0][1] != "20.50" || got[1][1] != "7.00" {
		t.Fatalf("computed decimals = %v, want 20.50 and 7.00", got)
	}
	// A plain copy keeps the source's own declared precision instead of
	// re-deriving one. system.columns reports only the kind, so the canonical
	// DDL is what shows the precision survived.
	mustExec(t, sess, "CREATE TABLE plain PRIMARY KEY (id) AS SELECT id, amt FROM money")
	ddl := rowsOf(t, sess, "SELECT ddl FROM system.table_ddl WHERE table_name = 'plain'")
	if len(ddl) != 1 || !strings.Contains(ddl[0][0], `"amt" DECIMAL(12,2)`) {
		t.Fatalf("a plain copy did not preserve DECIMAL(12,2): %v", ddl)
	}
	// The computed column's derived precision is the dialect maximum with the
	// scale the values needed.
	dddl := rowsOf(t, sess, "SELECT ddl FROM system.table_ddl WHERE table_name = 'doubled'")
	if len(dddl) != 1 || !strings.Contains(dddl[0][0], `"total" DECIMAL(38,2)`) {
		t.Fatalf("derived decimal type = %v, want DECIMAL(38,2)", dddl)
	}
}

// The source is a plan like any other, so EXPLAIN shows it -- and planning is
// not executing: EXPLAIN must not create the table.
func TestCreateTableAsExplainsItsSourceWithoutCreating(t *testing.T) {
	sess := newCTASSession(t)
	res, err := sess.Exec("EXPLAIN CREATE TABLE c PRIMARY KEY (id) AS SELECT id, qty FROM src WHERE qty > 5")
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	for _, row := range res.Rows {
		plan.WriteString(row[0].Str)
		plan.WriteString("\n")
	}
	got := plan.String()
	for _, want := range []string{"CreateTableAs c", "Filter", "SeqScan src"} {
		if !strings.Contains(got, want) {
			t.Fatalf("EXPLAIN output missing %q:\n%s", want, got)
		}
	}
	if _, ok := sess.lookup("c"); ok {
		t.Fatal("EXPLAIN created the table")
	}
}

// The source runs through the query path, so it is bounded like a query: an
// oversized source is an explicit refusal, never a silently truncated table.
func TestCreateTableAsIsBoundedLikeAQuery(t *testing.T) {
	sess := newCTASSession(t)
	lim := scheduler.DefaultLimits()
	lim.ResultRows = 2
	sess.SetLimits(lim)
	if _, err := sess.Exec("CREATE TABLE big PRIMARY KEY (id) AS SELECT id, qty FROM src"); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("a source past max_result_rows must be exhausted, got %v", err)
	}
	if _, ok := sess.lookup("big"); ok {
		t.Fatal("a truncated table was created")
	}
}
