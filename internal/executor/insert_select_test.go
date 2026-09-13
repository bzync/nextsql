package executor

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

// `INSERT INTO t <query>` fills a table from a query instead of a literal
// VALUES list. Before it existed, copying or transforming rows meant reading
// every one out to the client and writing it back.

func newInsertSelectSession(t *testing.T) *Session {
	t.Helper()
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE src (id INT64 PRIMARY KEY, name STRING, qty INT64)")
	mustExec(t, sess, "INSERT INTO src VALUES (1,'a',10),(2,'b',20),(3,'c',30)")
	return sess
}

func wantExecError(t *testing.T, sess *Session, sql, substr string) {
	t.Helper()
	_, err := sess.Exec(sql)
	if err == nil {
		t.Fatalf("%s: accepted, want an error naming %q", sql, substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("%s: error %v does not contain %q", sql, err, substr)
	}
}

func TestInsertSelectCopiesRows(t *testing.T) {
	sess := newInsertSelectSession(t)
	mustExec(t, sess, "CREATE TABLE dst (id INT64 PRIMARY KEY, name STRING, qty INT64)")
	mustExec(t, sess, "INSERT INTO dst SELECT id, name, qty FROM src")
	got := rowsOf(t, sess, "SELECT id, name, qty FROM dst ORDER BY id")
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
}

// The query's columns are projected, not the source table's rows: a narrowing
// projection must write the projected values, and a named column list must
// line up against them.
func TestInsertSelectProjectsAndTargetsColumns(t *testing.T) {
	sess := newInsertSelectSession(t)
	mustExec(t, sess, "CREATE TABLE narrow (id INT64 PRIMARY KEY, qty INT64)")
	mustExec(t, sess, "INSERT INTO narrow SELECT id, qty FROM src WHERE qty >= 20")
	if got := idsOf(t, sess, "SELECT id FROM narrow ORDER BY id"); !eqStrings(got, []string{"2", "3"}) {
		t.Fatalf("narrowing projection = %v, want [2 3]", got)
	}
	mustExec(t, sess, "CREATE TABLE named (id INT64 PRIMARY KEY, qty INT64)")
	mustExec(t, sess, "INSERT INTO named (qty, id) SELECT qty, id FROM src WHERE id = 1")
	got := rowsOf(t, sess, "SELECT id, qty FROM named")
	if len(got) != 1 || got[0][0] != "1" || got[0][1] != "10" {
		t.Fatalf("named column list = %v, want [[1 10]]", got)
	}
}

// A column the query does not supply takes its DEFAULT, exactly as it would
// from a VALUES insert.
func TestInsertSelectAppliesDefaults(t *testing.T) {
	sess := newInsertSelectSession(t)
	mustExec(t, sess, "CREATE TABLE withdef (id INT64 PRIMARY KEY, tag STRING DEFAULT 'none')")
	mustExec(t, sess, "INSERT INTO withdef (id) SELECT id FROM src")
	got := rowsOf(t, sess, "SELECT id, tag FROM withdef ORDER BY id")
	if len(got) != 3 {
		t.Fatalf("default insert = %v", got)
	}
	for _, r := range got {
		if r[1] != "none" {
			t.Fatalf("default not applied: %v", got)
		}
	}
}

// Every constraint a VALUES insert is subject to still applies.
func TestInsertSelectEnforcesConstraints(t *testing.T) {
	sess := newInsertSelectSession(t)
	mustExec(t, sess, "CREATE TABLE nn (id INT64 PRIMARY KEY, name STRING NOT NULL)")
	wantExecError(t, sess, "INSERT INTO nn SELECT id, NULL FROM src", "NOT NULL")

	mustExec(t, sess, "CREATE TABLE ck (id INT64 PRIMARY KEY, n INT64 CHECK (n > 0))")
	wantExecError(t, sess, "INSERT INTO ck SELECT id, 0 - qty FROM src", "ck_ck_1")
	mustExec(t, sess, "INSERT INTO ck SELECT id, qty FROM src")
	if got := idsOf(t, sess, "SELECT id FROM ck ORDER BY id"); !eqStrings(got, []string{"1", "2", "3"}) {
		t.Fatalf("CHECK-passing insert = %v", got)
	}
	wantExecError(t, sess, "INSERT INTO ck SELECT id, qty FROM src", "duplicate key")
}

// The source is any query, bound by the ordinary query binder.
func TestInsertSelectAcceptsEveryQueryShape(t *testing.T) {
	sess := newInsertSelectSession(t)
	mustExec(t, sess, "CREATE TABLE pair (id INT64 PRIMARY KEY, label STRING)")
	mustExec(t, sess, "INSERT INTO pair SELECT s.id, s.name FROM src s JOIN src o ON s.id = o.id WHERE s.qty > 10")
	if got := idsOf(t, sess, "SELECT id FROM pair ORDER BY id"); !eqStrings(got, []string{"2", "3"}) {
		t.Fatalf("join source = %v, want [2 3]", got)
	}

	mustExec(t, sess, "CREATE TABLE viacte (id INT64 PRIMARY KEY, qty INT64)")
	mustExec(t, sess, "INSERT INTO viacte WITH big AS (SELECT id, qty FROM src WHERE qty > 15) SELECT id, qty FROM big")
	if got := idsOf(t, sess, "SELECT id FROM viacte ORDER BY id"); !eqStrings(got, []string{"2", "3"}) {
		t.Fatalf("CTE source = %v, want [2 3]", got)
	}

	mustExec(t, sess, "CREATE TABLE viaset (id INT64 PRIMARY KEY)")
	mustExec(t, sess, "INSERT INTO viaset SELECT id FROM src WHERE qty = 10 UNION SELECT id FROM src WHERE qty = 30")
	if got := idsOf(t, sess, "SELECT id FROM viaset ORDER BY id"); !eqStrings(got, []string{"1", "3"}) {
		t.Fatalf("set-operation source = %v, want [1 3]", got)
	}

	mustExec(t, sess, "CREATE VIEW vbig AS SELECT id, qty FROM src WHERE qty >= 20")
	mustExec(t, sess, "CREATE TABLE viaview (id INT64 PRIMARY KEY, qty INT64)")
	mustExec(t, sess, "INSERT INTO viaview SELECT id, qty FROM vbig")
	if got := idsOf(t, sess, "SELECT id FROM viaview ORDER BY id"); !eqStrings(got, []string{"2", "3"}) {
		t.Fatalf("view source = %v, want [2 3]", got)
	}

	mustExec(t, sess, "CREATE TABLE topn (id INT64 PRIMARY KEY, qty INT64)")
	mustExec(t, sess, "INSERT INTO topn SELECT id, qty FROM src ORDER BY qty DESC LIMIT 2")
	if got := idsOf(t, sess, "SELECT id FROM topn ORDER BY id"); !eqStrings(got, []string{"2", "3"}) {
		t.Fatalf("ORDER BY/LIMIT source = %v, want [2 3]", got)
	}
}

// A transaction sees its own writes and there is no statement-level command
// id, so a streaming implementation of `INSERT INTO t SELECT ... FROM t` would
// read back the rows it had just written and feed itself. The source is read
// to completion first, so the statement doubles the table exactly once.
func TestInsertSelectFromItselfTerminates(t *testing.T) {
	sess := newInsertSelectSession(t)
	mustExec(t, sess, "CREATE TABLE selfy (id INT64 PRIMARY KEY, v INT64)")
	mustExec(t, sess, "INSERT INTO selfy VALUES (1,1),(2,2),(3,3)")
	for i, want := range []string{"6", "12", "24"} {
		mustExec(t, sess, "INSERT INTO selfy SELECT id + "+[]string{"100", "1000", "10000"}[i]+", v FROM selfy")
		got := rowsOf(t, sess, "SELECT COUNT(*) FROM selfy")
		if len(got) != 1 || got[0][0] != want {
			t.Fatalf("self-insert round %d = %v, want %s", i+1, got, want)
		}
	}
}

// A self-reference reached through a CTE or a view is the same hazard.
func TestInsertSelectFromItselfThroughCTEAndView(t *testing.T) {
	sess := newInsertSelectSession(t)
	mustExec(t, sess, "CREATE TABLE ring (id INT64 PRIMARY KEY, v INT64)")
	mustExec(t, sess, "INSERT INTO ring VALUES (1,1),(2,2)")
	mustExec(t, sess, "INSERT INTO ring WITH c AS (SELECT id, v FROM ring) SELECT id + 10, v FROM c")
	if got := rowsOf(t, sess, "SELECT COUNT(*) FROM ring"); got[0][0] != "4" {
		t.Fatalf("self-insert through a CTE = %v, want 4", got)
	}
	mustExec(t, sess, "CREATE VIEW vring AS SELECT id, v FROM ring")
	mustExec(t, sess, "INSERT INTO ring SELECT id + 100, v FROM vring")
	if got := rowsOf(t, sess, "SELECT COUNT(*) FROM ring"); got[0][0] != "8" {
		t.Fatalf("self-insert through a view = %v, want 8", got)
	}
}

func TestInsertSelectReturning(t *testing.T) {
	sess := newInsertSelectSession(t)
	mustExec(t, sess, "CREATE TABLE ret (id INT64 PRIMARY KEY, qty INT64)")
	got := rowsOf(t, sess, "INSERT INTO ret SELECT id, qty FROM src RETURNING id, qty")
	if len(got) != 3 || got[0][0] != "1" || got[2][1] != "30" {
		t.Fatalf("RETURNING = %v", got)
	}
}

// A rolled-back INSERT ... SELECT leaves nothing behind.
func TestInsertSelectRollsBack(t *testing.T) {
	sess := newInsertSelectSession(t)
	mustExec(t, sess, "CREATE TABLE tx (id INT64 PRIMARY KEY)")
	mustExec(t, sess, "BEGIN")
	mustExec(t, sess, "INSERT INTO tx SELECT id FROM src")
	if got := rowsOf(t, sess, "SELECT COUNT(*) FROM tx"); got[0][0] != "3" {
		t.Fatalf("in-transaction count = %v, want 3", got)
	}
	mustExec(t, sess, "ROLLBACK")
	if got := rowsOf(t, sess, "SELECT COUNT(*) FROM tx"); got[0][0] != "0" {
		t.Fatalf("after rollback = %v, want 0", got)
	}
	mustExec(t, sess, "BEGIN")
	mustExec(t, sess, "INSERT INTO tx SELECT id FROM src")
	mustExec(t, sess, "COMMIT")
	if got := rowsOf(t, sess, "SELECT COUNT(*) FROM tx"); got[0][0] != "3" {
		t.Fatalf("after commit = %v, want 3", got)
	}
}

// ROLLBACK TO must reverse only the insert that ran after the savepoint.
func TestInsertSelectRollsBackToSavepoint(t *testing.T) {
	sess := newInsertSelectSession(t)
	mustExec(t, sess, "CREATE TABLE sp (id INT64 PRIMARY KEY)")
	mustExec(t, sess, "BEGIN")
	mustExec(t, sess, "INSERT INTO sp SELECT id FROM src")
	mustExec(t, sess, "SAVEPOINT s1")
	mustExec(t, sess, "INSERT INTO sp SELECT id + 50 FROM src")
	if got := rowsOf(t, sess, "SELECT COUNT(*) FROM sp"); got[0][0] != "6" {
		t.Fatalf("before ROLLBACK TO = %v, want 6", got)
	}
	mustExec(t, sess, "ROLLBACK TO s1")
	if got := rowsOf(t, sess, "SELECT COUNT(*) FROM sp"); got[0][0] != "3" {
		t.Fatalf("after ROLLBACK TO = %v, want 3", got)
	}
	mustExec(t, sess, "COMMIT")
	if got := rowsOf(t, sess, "SELECT COUNT(*) FROM sp"); got[0][0] != "3" {
		t.Fatalf("after commit = %v, want 3", got)
	}
}

// `INSERT INTO t SELECT <exprs>` with no FROM is one row of values written a
// different way, and becomes exactly that.
func TestInsertSelectWithoutFrom(t *testing.T) {
	sess := newInsertSelectSession(t)
	mustExec(t, sess, "CREATE TABLE lit (id INT64 PRIMARY KEY, name STRING)")
	mustExec(t, sess, "INSERT INTO lit SELECT 1, 'x'")
	got := rowsOf(t, sess, "SELECT id, name FROM lit")
	if len(got) != 1 || got[0][0] != "1" || got[0][1] != "x" {
		t.Fatalf("FROM-less source = %v, want [[1 x]]", got)
	}
	// A shape that filters or bounds that single row is refused by name
	// rather than through a confusing "unknown table".
	wantExecError(t, sess, "INSERT INTO lit SELECT 2, 'y' WHERE 1 = 1", "FROM-less SELECT")
}

// The column counts must agree, and a bad target column is named.
func TestInsertSelectRejectsMismatchedShape(t *testing.T) {
	sess := newInsertSelectSession(t)
	mustExec(t, sess, "CREATE TABLE two (id INT64 PRIMARY KEY, qty INT64)")
	wantExecError(t, sess, "INSERT INTO two SELECT id FROM src", "column count")
	wantExecError(t, sess, "INSERT INTO two (id) SELECT id, qty FROM src", "column count")
	wantExecError(t, sess, "INSERT INTO two (nope) SELECT id FROM src", "nope")
	wantExecError(t, sess, "INSERT INTO two (id, id) SELECT id, qty FROM src", "duplicate insert column")
	wantExecError(t, sess, "INSERT INTO nosuch SELECT id FROM src", "nosuch")
}

// The VALUES form must be untouched by all of this.
func TestInsertValuesStillWorks(t *testing.T) {
	sess := newInsertSelectSession(t)
	mustExec(t, sess, "CREATE TABLE v (id INT64 PRIMARY KEY, name STRING)")
	mustExec(t, sess, "INSERT INTO v VALUES (1,'a'),(2,'b')")
	mustExec(t, sess, "INSERT INTO v (id, name) VALUES (3,'c')")
	if got := idsOf(t, sess, "SELECT id FROM v ORDER BY id"); !eqStrings(got, []string{"1", "2", "3"}) {
		t.Fatalf("VALUES insert = %v", got)
	}
	got := rowsOf(t, sess, "INSERT INTO v VALUES (4,'d') RETURNING id")
	if len(got) != 1 || got[0][0] != "4" {
		t.Fatalf("VALUES RETURNING = %v", got)
	}
}

// The source is read with the invoker's own rights: an INSERT must not become
// a way to copy out of a table the user may not read.
func TestInsertSelectRequiresSelectOnTheSource(t *testing.T) {
	db := testDB(t)
	local := db.Session()
	execOK(t, local, `CREATE TABLE secret (id INT64 PRIMARY KEY, v STRING NOT NULL)`)
	execOK(t, local, `INSERT INTO secret VALUES (1, 'classified')`)
	execOK(t, local, `CREATE TABLE mine (id INT64 PRIMARY KEY, v STRING)`)

	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	if err := acl.Grant("app", security.PrivInsert, security.ScopeTable, "mine"); err != nil {
		t.Fatal(err)
	}
	// INSERT on the target is not enough: the read must be authorized too.
	if _, err := app.Exec(`INSERT INTO mine SELECT id, v FROM secret`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("INSERT ... SELECT must require SELECT on the source: %v", err)
	}
	if err := acl.Grant("app", security.PrivSelect, security.ScopeTable, "secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Exec(`INSERT INTO mine SELECT id, v FROM secret`); err != nil {
		t.Fatalf("granted INSERT ... SELECT: %v", err)
	}
	if got := rowsOf(t, local, "SELECT v FROM mine"); len(got) != 1 || got[0][0] != "classified" {
		t.Fatalf("copied rows = %v", got)
	}
}

// Reading is authorized even when the target is writable: a source the user
// cannot read stays unreadable through a join too.
func TestInsertSelectAuthorizesEveryJoinedSource(t *testing.T) {
	db := testDB(t)
	local := db.Session()
	execOK(t, local, `CREATE TABLE openish (id INT64 PRIMARY KEY)`)
	execOK(t, local, `INSERT INTO openish VALUES (1)`)
	execOK(t, local, `CREATE TABLE closed (id INT64 PRIMARY KEY, v STRING NOT NULL)`)
	execOK(t, local, `INSERT INTO closed VALUES (1, 'no')`)
	execOK(t, local, `CREATE TABLE sink (id INT64 PRIMARY KEY, v STRING)`)

	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	for _, g := range []struct {
		priv  security.Privilege
		table string
	}{
		{security.PrivInsert, "sink"},
		{security.PrivSelect, "openish"},
	} {
		if err := acl.Grant("app", g.priv, security.ScopeTable, g.table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.Exec(`INSERT INTO sink SELECT o.id, c.v FROM openish o JOIN closed c ON o.id = c.id`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("a joined source must require its own SELECT: %v", err)
	}
}

// A query-sourced row is written by the same path a VALUES row is, so the
// triggers and foreign keys on the target apply to it.
func TestInsertSelectFiresTriggersAndChecksForeignKeys(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE feed (id STRING PRIMARY KEY)`)
	execOK(t, s, `INSERT INTO feed (id) VALUES ('1'), ('2')`)
	execOK(t, s, `CREATE TABLE orders (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE audit_log (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO audit_log (id) VALUES ($id); END`)
	execOK(t, s, `CREATE TRIGGER audit_ins AFTER INSERT ON orders FOR EACH ROW RUN WORKFLOW record(NEW.id)`)

	execOK(t, s, `INSERT INTO orders SELECT id FROM feed`)
	if got := rowsOf(t, s, `SELECT COUNT(*) FROM audit_log`); got[0][0] != "2" {
		t.Fatalf("trigger fired %v times for an INSERT ... SELECT, want 2", got)
	}

	execOK(t, s, `CREATE TABLE child (id STRING PRIMARY KEY, parent STRING REFERENCES orders(id))`)
	if _, err := s.Exec(`INSERT INTO child SELECT '9', 'missing'`); err == nil {
		t.Fatal("INSERT ... SELECT wrote a row with no parent")
	}
	execOK(t, s, `INSERT INTO child SELECT id, id FROM feed`)
	if got := rowsOf(t, s, `SELECT COUNT(*) FROM child`); got[0][0] != "2" {
		t.Fatalf("FK-valid INSERT ... SELECT = %v, want 2", got)
	}
}

// EXPLAIN must describe the source, not hide it. An INSERT's source is a whole
// query plan rather than the bare access tree an UPDATE filters over, so it
// goes through the general physical planner; routing it through the access
// planner instead leaves everything above the scan as an opaque "Plan" node,
// and the optimizer never sees the source at all if an INSERT is treated as a
// leaf.
func TestExplainInsertSelectShowsTheSourcePlan(t *testing.T) {
	sess := newInsertSelectSession(t)
	mustExec(t, sess, "CREATE TABLE sink (id INT64 PRIMARY KEY, name STRING, qty INT64)")
	res, err := sess.Exec("EXPLAIN INSERT INTO sink SELECT id, name, qty FROM src WHERE qty > 10")
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	for _, want := range []string{"Insert", "Project", "Filter", "Scan"} {
		if !explainHas(res, want) {
			t.Fatalf("EXPLAIN of an INSERT ... SELECT has no %s node: %v", want, res.Rows)
		}
	}
	if explainHas(res, "Plan") {
		t.Fatalf("EXPLAIN left the source as an opaque Plan node: %v", res.Rows)
	}

	// A VALUES insert has no source plan and must stay a single node.
	res, err = sess.Exec("EXPLAIN INSERT INTO sink VALUES (99, 'z', 9)")
	if err != nil {
		t.Fatalf("EXPLAIN VALUES: %v", err)
	}
	if len(res.Rows) != 1 || !explainHas(res, "Insert") {
		t.Fatalf("EXPLAIN of a VALUES insert = %v, want a single Insert node", res.Rows)
	}
}
