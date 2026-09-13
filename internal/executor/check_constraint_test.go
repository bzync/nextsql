package executor

import (
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
)

func newCheckSession(t *testing.T) *Session {
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

func mustExec(t *testing.T, sess *Session, sql string) {
	t.Helper()
	if _, err := sess.Exec(sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func wantViolation(t *testing.T, sess *Session, sql, constraint string) {
	t.Helper()
	_, err := sess.Exec(sql)
	if err == nil {
		t.Fatalf("%s: accepted a row the constraint forbids", sql)
	}
	if !strings.Contains(err.Error(), constraint) {
		t.Fatalf("%s: error does not name %q: %v", sql, constraint, err)
	}
}

func TestCheckEnforcedOnInsertAndUpdate(t *testing.T) {
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE t (id INT64 PRIMARY KEY, n INT64 CHECK (n > 0))")
	mustExec(t, sess, "INSERT INTO t VALUES (1, 5)")
	wantViolation(t, sess, "INSERT INTO t VALUES (2, -1)", "ck_t_1")
	wantViolation(t, sess, "UPDATE t SET n = 0 WHERE id = 1", "ck_t_1")
	mustExec(t, sess, "UPDATE t SET n = 9 WHERE id = 1")
	if got := rowsOf(t, sess, "SELECT n FROM t ORDER BY id"); len(got) != 1 || got[0][0] != "9" {
		t.Fatalf("rows after the refused writes = %v", got)
	}
}

// A check refuses a row only when it evaluates to FALSE. UNKNOWN satisfies it,
// so a NULL column passes `CHECK (n > 0)` — NOT NULL is the constraint that
// rejects a NULL.
func TestCheckUnknownPasses(t *testing.T) {
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE t (id INT64 PRIMARY KEY, n INT64 CHECK (n > 0))")
	mustExec(t, sess, "INSERT INTO t VALUES (1, NULL)")
	mustExec(t, sess, "CREATE TABLE u (id INT64 PRIMARY KEY, n INT64 NOT NULL CHECK (n > 0))")
	if _, err := sess.Exec("INSERT INTO u VALUES (1, NULL)"); err == nil {
		t.Fatal("NOT NULL did not reject a NULL beside a CHECK")
	}
}

func TestCheckOnUpsertAndBulkInsert(t *testing.T) {
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE t (id INT64 PRIMARY KEY, n INT64 CHECK (n > 0))")
	// Multi-row INSERT takes the bulk path.
	wantViolation(t, sess, "INSERT INTO t VALUES (1, 5), (2, 6), (3, -1)", "ck_t_1")
	if got := rowsOf(t, sess, "SELECT id FROM t"); len(got) != 0 {
		t.Fatalf("a refused multi-row insert left rows behind: %v", got)
	}
	mustExec(t, sess, "INSERT INTO t VALUES (1, 5)")
	wantViolation(t, sess, "UPSERT INTO t (id, n) VALUES (1, -2)", "ck_t_1")
	wantViolation(t, sess, "UPSERT INTO t (id, n) VALUES (2, -2)", "ck_t_1")
	mustExec(t, sess, "UPSERT INTO t (id, n) VALUES (1, 7)")
}

// A cascade writes child rows on the engine's own initiative; those rows must
// satisfy the child's checks like any other write.
func TestCheckEnforcedOnForeignKeyCascade(t *testing.T) {
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE p (id INT64 PRIMARY KEY)")
	mustExec(t, sess, "CREATE TABLE c (id INT64 PRIMARY KEY, pid INT64 NOT NULL CHECK (pid > 0), FOREIGN KEY (pid) REFERENCES p (id) ON UPDATE CASCADE)")
	mustExec(t, sess, "INSERT INTO p VALUES (5)")
	mustExec(t, sess, "INSERT INTO c VALUES (1, 5)")
	// Moving the parent key to 0 would cascade a child row that violates the
	// child's own check.
	if _, err := sess.Exec("UPDATE p SET id = 0 WHERE id = 5"); err == nil {
		t.Fatal("a cascade wrote a child row its CHECK forbids")
	}
	mustExec(t, sess, "UPDATE p SET id = 6 WHERE id = 5")
	if got := rowsOf(t, sess, "SELECT pid FROM c"); len(got) != 1 || got[0][0] != "6" {
		t.Fatalf("cascade result = %v", got)
	}
}

func TestCheckAddConstraintValidatesExistingRows(t *testing.T) {
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE t (id INT64 PRIMARY KEY, n INT64)")
	mustExec(t, sess, "INSERT INTO t VALUES (1, 5), (2, -3)")
	if _, err := sess.Exec("ALTER TABLE t ADD CONSTRAINT pos CHECK (n > 0)"); err == nil {
		t.Fatal("a constraint was added over data that contradicts it")
	}
	// The refused DDL must leave no constraint behind.
	mustExec(t, sess, "INSERT INTO t VALUES (3, -9)")
	mustExec(t, sess, "DELETE FROM t WHERE n < 0")
	mustExec(t, sess, "ALTER TABLE t ADD CONSTRAINT pos CHECK (n > 0)")
	wantViolation(t, sess, "INSERT INTO t VALUES (4, -1)", "pos")
}

func TestCheckDropConstraint(t *testing.T) {
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE t (id INT64 PRIMARY KEY, n INT64, CONSTRAINT pos CHECK (n > 0))")
	wantViolation(t, sess, "INSERT INTO t VALUES (1, -1)", "pos")
	mustExec(t, sess, "ALTER TABLE t DROP CONSTRAINT pos")
	mustExec(t, sess, "INSERT INTO t VALUES (1, -1)")
	if _, err := sess.Exec("ALTER TABLE t DROP CONSTRAINT pos"); err == nil {
		t.Fatal("dropping an absent constraint succeeded")
	}
}

// A stored predicate must be deterministic and must depend on nothing but the
// row: followers apply the leader's WAL without re-evaluating it, and a
// constraint that reads another table is never re-validated when that table
// changes.
func TestCheckRefusesUnstablePredicates(t *testing.T) {
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE other (id INT64 PRIMARY KEY)")
	for _, sql := range []string{
		"CREATE TABLE t (id INT64 PRIMARY KEY, n INT64 CHECK (n > NOW()))",
		"CREATE TABLE t (id INT64 PRIMARY KEY, s STRING CHECK (s = UUID()))",
		"CREATE TABLE t (id INT64 PRIMARY KEY, n INT64 CHECK (n IN (SELECT id FROM other)))",
		"CREATE TABLE t (id INT64 PRIMARY KEY, n INT64 CHECK (EXISTS (SELECT 1 FROM other)))",
		"CREATE TABLE t (id INT64 PRIMARY KEY, n INT64 CHECK (COUNT(*) > 0))",
		"CREATE TABLE t (id INT64 PRIMARY KEY, n INT64 CHECK (n > $1))",
		"CREATE TABLE t (id INT64 PRIMARY KEY, n INT64 CHECK (missing > 0))",
	} {
		if _, err := sess.Exec(sql); err == nil {
			t.Fatalf("accepted an invalid CHECK: %s", sql)
		}
	}
}

func TestCheckMultipleAndNamed(t *testing.T) {
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE t (id INT64 PRIMARY KEY, a INT64 CHECK (a > 0), b INT64 CHECK (b < 100), CONSTRAINT ab CHECK (a < b))")
	mustExec(t, sess, "INSERT INTO t VALUES (1, 5, 50)")
	wantViolation(t, sess, "INSERT INTO t VALUES (2, -1, 50)", "ck_t_1")
	wantViolation(t, sess, "INSERT INTO t VALUES (3, 5, 500)", "ck_t_2")
	wantViolation(t, sess, "INSERT INTO t VALUES (4, 60, 50)", "ab")
}

// The catalog is the authority after a restart: a reopened database must still
// refuse what the constraint forbids.
func TestCheckSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sess := db.Session()
	mustExec(t, sess, "CREATE TABLE t (id INT64 PRIMARY KEY, n INT64, CONSTRAINT pos CHECK (n > 0))")
	mustExec(t, sess, "INSERT INTO t VALUES (1, 5)")
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, err := Open(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	sess2 := db2.Session()
	wantViolation(t, sess2, "INSERT INTO t VALUES (2, -1)", "pos")
	mustExec(t, sess2, "INSERT INTO t VALUES (2, 1)")
}

func TestCheckVisibleInSystemCatalogAndDDL(t *testing.T) {
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE t (id INT64 PRIMARY KEY, a INT64, b INT64, CONSTRAINT ab CHECK (a < b))")
	rows := rowsOf(t, sess, "SELECT table_name, constraint_name, predicate FROM system.checks")
	if len(rows) != 1 || rows[0][0] != "t" || rows[0][1] != "ab" {
		t.Fatalf("system.checks = %v", rows)
	}
	ddlRows := rowsOf(t, sess, "SELECT ddl FROM system.table_ddl WHERE table_name = 't' AND object_type = 'TABLE'")
	if len(ddlRows) != 1 || !strings.Contains(ddlRows[0][0], "CHECK") {
		t.Fatalf("table_ddl did not render the constraint: %v", ddlRows)
	}
	// The rendered DDL has to rebuild the same constraint.
	rebuilt := strings.Replace(ddlRows[0][0], `"t"`, `"t2"`, 1)
	mustExec(t, sess, rebuilt)
	wantViolation(t, sess, "INSERT INTO t2 VALUES (1, 9, 1)", "ab")
}
