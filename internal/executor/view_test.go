package executor

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

// NextSQL had no views: a named query had to be pasted into each statement
// that wanted it.
func newViewSession(t *testing.T) *Session {
	t.Helper()
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE emp (id INT64 PRIMARY KEY, name STRING, dept INT64, salary INT64)")
	mustExec(t, sess, "INSERT INTO emp VALUES (1,'a',1,100),(2,'b',1,200),(3,'c',2,300)")
	return sess
}

func TestViewSelect(t *testing.T) {
	sess := newViewSession(t)
	mustExec(t, sess, "CREATE VIEW dept1 AS SELECT id, name, salary FROM emp WHERE dept = 1")
	if got := idsOf(t, sess, "SELECT id FROM dept1 ORDER BY id"); !eqStrings(got, []string{"1", "2"}) {
		t.Fatalf("SELECT from a view = %v", got)
	}
	// The view is a relation like any other: it filters, aggregates and
	// orders.
	if got := rowsOf(t, sess, "SELECT COUNT(*) FROM dept1"); got[0][0] != "2" {
		t.Fatalf("COUNT over a view = %v", got)
	}
	if got := idsOf(t, sess, "SELECT id FROM dept1 WHERE salary > 150"); !eqStrings(got, []string{"2"}) {
		t.Fatalf("filtered view = %v", got)
	}
}

func TestViewJoinsAndNests(t *testing.T) {
	sess := newViewSession(t)
	mustExec(t, sess, "CREATE VIEW dept1 AS SELECT id, name, salary FROM emp WHERE dept = 1")
	got := rowsOf(t, sess, "SELECT e.name FROM emp e JOIN dept1 d ON e.id = d.id ORDER BY e.id")
	if len(got) != 2 || got[0][0] != "a" || got[1][0] != "b" {
		t.Fatalf("join with a view = %v", got)
	}
	// A view over a view.
	mustExec(t, sess, "CREATE VIEW rich AS SELECT id FROM dept1 WHERE salary > 150")
	if ids := idsOf(t, sess, "SELECT id FROM rich"); !eqStrings(ids, []string{"2"}) {
		t.Fatalf("view over a view = %v", ids)
	}
	// A view inside a subquery of a read and of a write.
	if ids := idsOf(t, sess, "SELECT id FROM emp WHERE id IN (SELECT id FROM rich) ORDER BY id"); !eqStrings(ids, []string{"2"}) {
		t.Fatalf("view in an IN subquery = %v", ids)
	}
	mustExec(t, sess, "DELETE FROM emp WHERE id IN (SELECT id FROM rich)")
	if ids := idsOf(t, sess, "SELECT id FROM emp ORDER BY id"); !eqStrings(ids, []string{"1", "3"}) {
		t.Fatalf("DELETE using a view subquery = %v", ids)
	}
}

// A view reads the tables as they are now, not as they were when it was
// created.
func TestViewFollowsTableChanges(t *testing.T) {
	sess := newViewSession(t)
	mustExec(t, sess, "CREATE VIEW dept1 AS SELECT id, name FROM emp WHERE dept = 1")
	mustExec(t, sess, "INSERT INTO emp VALUES (4,'d',1,400)")
	if got := idsOf(t, sess, "SELECT id FROM dept1 ORDER BY id"); !eqStrings(got, []string{"1", "2", "4"}) {
		t.Fatalf("view did not see a new row: %v", got)
	}
	mustExec(t, sess, "ALTER TABLE emp ADD COLUMN note STRING")
	if got := idsOf(t, sess, "SELECT id FROM dept1 ORDER BY id"); !eqStrings(got, []string{"1", "2", "4"}) {
		t.Fatalf("view broke after ALTER TABLE: %v", got)
	}
}

// NextSQL views are read-only.
func TestViewRefusesWrites(t *testing.T) {
	sess := newViewSession(t)
	mustExec(t, sess, "CREATE VIEW dept1 AS SELECT id, name, salary FROM emp WHERE dept = 1")
	for _, sql := range []string{
		"INSERT INTO dept1 VALUES (9,'x',1)",
		"UPDATE dept1 SET name = 'z' WHERE id = 1",
		"DELETE FROM dept1 WHERE id = 1",
		"UPSERT INTO dept1 (id, name, salary) VALUES (1,'z',1)",
	} {
		_, err := sess.Exec(sql)
		if err == nil {
			t.Fatalf("a write through a view was accepted: %s", sql)
		}
		if !strings.Contains(err.Error(), "view") {
			t.Fatalf("%s: error does not explain the refusal: %v", sql, err)
		}
	}
}

// A view stored as text would otherwise be left pointing at something that no
// longer exists.
func TestViewBlocksDropOfWhatItReads(t *testing.T) {
	sess := newViewSession(t)
	mustExec(t, sess, "CREATE VIEW dept1 AS SELECT id, name FROM emp WHERE dept = 1")
	if _, err := sess.Exec("DROP TABLE emp"); err == nil {
		t.Fatal("dropped a table a view depends on")
	}
	mustExec(t, sess, "CREATE VIEW nested AS SELECT id FROM dept1")
	if _, err := sess.Exec("DROP VIEW dept1"); err == nil {
		t.Fatal("dropped a view another view depends on")
	}
	mustExec(t, sess, "DROP VIEW nested")
	mustExec(t, sess, "DROP VIEW dept1")
	mustExec(t, sess, "DROP TABLE emp")
}

func TestViewCreateReplaceAndDrop(t *testing.T) {
	sess := newViewSession(t)
	mustExec(t, sess, "CREATE VIEW v AS SELECT id FROM emp WHERE dept = 1")
	if _, err := sess.Exec("CREATE VIEW v AS SELECT id FROM emp"); err == nil {
		t.Fatal("a duplicate view name was accepted")
	}
	mustExec(t, sess, "CREATE OR REPLACE VIEW v AS SELECT id FROM emp WHERE dept = 2")
	if got := idsOf(t, sess, "SELECT id FROM v"); !eqStrings(got, []string{"3"}) {
		t.Fatalf("after CREATE OR REPLACE = %v, want [3]", got)
	}
	if _, err := sess.Exec("DROP VIEW nope"); err == nil {
		t.Fatal("dropped a view that does not exist")
	}
	mustExec(t, sess, "DROP VIEW IF EXISTS nope")
	mustExec(t, sess, "DROP VIEW v")
	if _, err := sess.Exec("SELECT id FROM v"); err == nil {
		t.Fatal("a dropped view still resolved")
	}
}

func TestViewRefusals(t *testing.T) {
	sess := newViewSession(t)
	for _, sql := range []string{
		// A view and a table share one relation namespace.
		"CREATE VIEW emp AS SELECT id FROM emp",
		// Must be a query.
		"CREATE VIEW bad AS DELETE FROM emp",
		// Must read a relation: a view is used by expanding it into a CTE.
		"CREATE VIEW bad AS SELECT 1",
		// Must resolve now.
		"CREATE VIEW bad AS SELECT id FROM missing_table",
		"CREATE VIEW bad AS SELECT missing_column FROM emp",
		// A declared column list has to match the query's output.
		"CREATE VIEW bad (a, b) AS SELECT id FROM emp",
	} {
		if _, err := sess.Exec(sql); err == nil {
			t.Fatalf("accepted an invalid view: %s", sql)
		}
	}
	// Self-reference is a cycle, not a table.
	if _, err := sess.Exec("CREATE VIEW selfref AS SELECT id FROM selfref"); err == nil {
		t.Fatal("accepted a self-referencing view")
	}
}

func TestViewColumnList(t *testing.T) {
	sess := newViewSession(t)
	mustExec(t, sess, "CREATE VIEW v (ident, who) AS SELECT id, name FROM emp WHERE dept = 1")
	got := rowsOf(t, sess, "SELECT ident, who FROM v ORDER BY ident")
	if len(got) != 2 || got[0][0] != "1" || got[0][1] != "a" {
		t.Fatalf("renamed view columns = %v", got)
	}
}

// A CTE of the same name shadows the view, as a local definition should.
func TestCTEShadowsView(t *testing.T) {
	sess := newViewSession(t)
	mustExec(t, sess, "CREATE VIEW v AS SELECT id FROM emp WHERE dept = 1")
	got := idsOf(t, sess, "WITH v AS (SELECT id FROM emp WHERE dept = 2) SELECT id FROM v")
	if !eqStrings(got, []string{"3"}) {
		t.Fatalf("a CTE did not shadow the view: %v", got)
	}
}

func TestViewSurvivesReopenAndIsListed(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sess := db.Session()
	mustExec(t, sess, "CREATE TABLE emp (id INT64 PRIMARY KEY, dept INT64)")
	mustExec(t, sess, "INSERT INTO emp VALUES (1,1),(2,2)")
	mustExec(t, sess, "CREATE VIEW v AS SELECT id FROM emp WHERE dept = 1")
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, err := Open(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	sess2 := db2.Session()
	if got := idsOf(t, sess2, "SELECT id FROM v"); !eqStrings(got, []string{"1"}) {
		t.Fatalf("view after reopen = %v", got)
	}
	rows := rowsOf(t, sess2, "SELECT view_name, definition FROM system.views")
	// The definition is stored with its identifiers quoted (see
	// TestViewDefinitionIsStoredKeywordProof).
	if len(rows) != 1 || rows[0][0] != "v" || rows[0][1] != `SELECT "id" FROM "emp" WHERE "dept" = 1` {
		t.Fatalf("system.views = %v", rows)
	}
}

// A view created inside a transaction is usable by it, and disappears if the
// transaction rolls back.
func TestViewInTransaction(t *testing.T) {
	sess := newViewSession(t)
	mustExec(t, sess, "BEGIN")
	mustExec(t, sess, "CREATE VIEW v AS SELECT id FROM emp WHERE dept = 1")
	if got := idsOf(t, sess, "SELECT id FROM v ORDER BY id"); !eqStrings(got, []string{"1", "2"}) {
		t.Fatalf("view inside its own transaction = %v", got)
	}
	mustExec(t, sess, "ROLLBACK")
	if _, err := sess.Exec("SELECT id FROM v"); err == nil {
		t.Fatal("a rolled-back view still resolved")
	}
}

// A view must not become a way around RBAC. NextSQL views run with the
// invoker's privileges: the expansion reads the underlying tables by name, so
// a caller who cannot select from a table cannot select from a view over it
// either, no matter who created the view.
func TestViewUsesInvokerPrivileges(t *testing.T) {
	db := testDB(t)
	owner := db.Session()
	execOK(t, owner, `CREATE TABLE secret (id STRING PRIMARY KEY, value STRING NOT NULL)`)
	execOK(t, owner, `INSERT INTO secret (id, value) VALUES ('1', 'a'), ('2', 'b')`)
	execOK(t, owner, `CREATE VIEW secret_view AS SELECT id FROM secret`)

	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)

	if _, err := app.Exec(`SELECT id FROM secret_view`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("a view handed out access to a table the caller cannot read: %v", err)
	}
	// system.views is gated like every other system table.
	if _, err := app.Exec(`SELECT view_name FROM system.views`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("system.views was readable without a grant: %v", err)
	}
	if err := acl.Grant("app", security.PrivSelect, security.ScopeTable, "secret"); err != nil {
		t.Fatal(err)
	}
	got := execOK(t, app, `SELECT id FROM secret_view ORDER BY id`)
	if len(got.Rows) != 2 {
		t.Fatalf("granted view rows: %+v", got.Rows)
	}
}

// A view body is SQL text parsed again on every use. Stored as written, a view
// that used a word as a name stopped parsing when that word became a keyword
// (`bool`, log #284). Stored with every identifier quoted, it keeps parsing:
// here the table and column are named with words that are identifiers today,
// and the stored text is checked to hold them only in quoted form.
func TestViewDefinitionIsStoredKeywordProof(t *testing.T) {
	db := testDB(t)
	sess := db.Session()
	mustExec(t, sess, `CREATE TABLE ledger (entry INT64 PRIMARY KEY, amount INT64, Posted INT64)`)
	mustExec(t, sess, `INSERT INTO ledger VALUES (1, 10, 1), (2, 20, 0)`)
	mustExec(t, sess, `CREATE VIEW posted_totals AS SELECT entry, amount AS "Amount" FROM ledger WHERE posted = 1 -- keep`)
	rows := rowsOf(t, sess, `SELECT definition FROM system.views`)
	want := `SELECT "entry", "amount" AS "Amount" FROM "ledger" WHERE "posted" = 1 -- keep`
	if len(rows) != 1 || rows[0][0] != want {
		t.Fatalf("stored definition = %v, want %q", rows, want)
	}
	if got := idsOf(t, sess, `SELECT entry FROM posted_totals`); !eqStrings(got, []string{"1"}) {
		t.Fatalf("view over the quoted definition returned %v", got)
	}
	if len(db.UnparseableViews()) != 0 {
		t.Fatalf("a freshly created view was reported unparseable: %v", db.UnparseableViews())
	}
}

// A view stored before definitions were quoted, whose body uses a word that is
// now reserved, is reported rather than failing silently until queried, and
// CREATE OR REPLACE VIEW repairs it.
func TestUnparseableLegacyViewIsReportedAndRepairable(t *testing.T) {
	db := testDB(t)
	sess := db.Session()
	mustExec(t, sess, `CREATE TABLE flags ("bool" INT64 PRIMARY KEY)`)
	mustExec(t, sess, `INSERT INTO flags VALUES (1)`)
	mustExec(t, sess, `CREATE VIEW legacy AS SELECT "bool" FROM flags`)
	// Rewrite the stored body to the unquoted form an older release kept.
	db.mu.Lock()
	v := db.views["legacy"]
	v.Query = `SELECT bool FROM flags`
	db.mu.Unlock()

	bad := db.UnparseableViews()
	if _, ok := bad["legacy"]; !ok || len(bad) != 1 {
		t.Fatalf("UnparseableViews = %v, want exactly legacy", bad)
	}
	mustExec(t, sess, `CREATE OR REPLACE VIEW legacy AS SELECT "bool" FROM flags`)
	if bad := db.UnparseableViews(); len(bad) != 0 {
		t.Fatalf("after CREATE OR REPLACE, UnparseableViews = %v", bad)
	}
	if got := idsOf(t, sess, `SELECT "bool" FROM legacy`); !eqStrings(got, []string{"1"}) {
		t.Fatalf("repaired view returned %v", got)
	}
}
