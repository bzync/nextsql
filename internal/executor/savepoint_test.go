package executor

import (
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
)

// NextSQL had no savepoints: a transaction was all-or-nothing, so a client
// that wanted to retry one failed statement had to replay the whole
// transaction.
func newSavepointSession(t *testing.T) *Session {
	t.Helper()
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE t (id INT64 PRIMARY KEY, n INT64)")
	return sess
}

func idList(t *testing.T, sess *Session) []string {
	t.Helper()
	return idsOf(t, sess, "SELECT id FROM t ORDER BY id")
}

func TestSavepointRollbackKeepsEarlierWork(t *testing.T) {
	sess := newSavepointSession(t)
	mustExec(t, sess, "BEGIN")
	mustExec(t, sess, "INSERT INTO t VALUES (1, 1)")
	mustExec(t, sess, "SAVEPOINT a")
	mustExec(t, sess, "INSERT INTO t VALUES (2, 2)")
	mustExec(t, sess, "ROLLBACK TO SAVEPOINT a")
	// The savepoint's own work survives; only what followed it is gone.
	if got := idList(t, sess); !eqStrings(got, []string{"1"}) {
		t.Fatalf("after ROLLBACK TO = %v, want [1]", got)
	}
	mustExec(t, sess, "INSERT INTO t VALUES (3, 3)")
	mustExec(t, sess, "COMMIT")
	if got := idList(t, sess); !eqStrings(got, []string{"1", "3"}) {
		t.Fatalf("after COMMIT = %v, want [1 3]", got)
	}
}

// The committed state has to match what the session saw, which is the case a
// page-image redo has to get right: the reversal is part of the committed
// page, not replayed away.
func TestSavepointRollbackSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sess := db.Session()
	mustExec(t, sess, "CREATE TABLE t (id INT64 PRIMARY KEY, n INT64)")
	mustExec(t, sess, "BEGIN")
	mustExec(t, sess, "INSERT INTO t VALUES (1, 1)")
	mustExec(t, sess, "SAVEPOINT a")
	mustExec(t, sess, "INSERT INTO t VALUES (2, 2)")
	mustExec(t, sess, "UPDATE t SET n = 99 WHERE id = 1")
	mustExec(t, sess, "ROLLBACK TO SAVEPOINT a")
	mustExec(t, sess, "COMMIT")
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, err := Open(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	sess2 := db2.Session()
	got := rowsOf(t, sess2, "SELECT id, n FROM t ORDER BY id")
	if len(got) != 1 || got[0][0] != "1" || got[0][1] != "1" {
		t.Fatalf("after reopen = %v, want the pre-savepoint row [1 1]", got)
	}
}

// An UPDATE and a DELETE after the savepoint must both be reversed, restoring
// the row versions the savepoint saw.
func TestSavepointReversesUpdateAndDelete(t *testing.T) {
	sess := newSavepointSession(t)
	mustExec(t, sess, "INSERT INTO t VALUES (1, 10), (2, 20)")
	mustExec(t, sess, "BEGIN")
	mustExec(t, sess, "SAVEPOINT a")
	mustExec(t, sess, "UPDATE t SET n = 999 WHERE id = 1")
	mustExec(t, sess, "DELETE FROM t WHERE id = 2")
	mustExec(t, sess, "ROLLBACK TO SAVEPOINT a")
	mustExec(t, sess, "COMMIT")
	got := rowsOf(t, sess, "SELECT id, n FROM t ORDER BY id")
	if len(got) != 2 || got[0][1] != "10" || got[1][1] != "20" {
		t.Fatalf("after reversal = %v, want the original rows", got)
	}
}

// Rolling back to the same savepoint twice, with work in between, must be
// correct both times: the undo records are re-applied, which has to stay
// idempotent.
func TestSavepointRepeatedRollback(t *testing.T) {
	sess := newSavepointSession(t)
	mustExec(t, sess, "BEGIN")
	mustExec(t, sess, "INSERT INTO t VALUES (1, 1)")
	mustExec(t, sess, "SAVEPOINT a")
	mustExec(t, sess, "INSERT INTO t VALUES (2, 2)")
	mustExec(t, sess, "ROLLBACK TO SAVEPOINT a")
	if got := idList(t, sess); !eqStrings(got, []string{"1"}) {
		t.Fatalf("first rollback = %v", got)
	}
	mustExec(t, sess, "INSERT INTO t VALUES (4, 4)")
	mustExec(t, sess, "ROLLBACK TO SAVEPOINT a")
	if got := idList(t, sess); !eqStrings(got, []string{"1"}) {
		t.Fatalf("second rollback = %v", got)
	}
	mustExec(t, sess, "COMMIT")
	if got := idList(t, sess); !eqStrings(got, []string{"1"}) {
		t.Fatalf("after commit = %v", got)
	}
}

func TestSavepointNesting(t *testing.T) {
	sess := newSavepointSession(t)
	mustExec(t, sess, "BEGIN")
	mustExec(t, sess, "INSERT INTO t VALUES (1, 1)")
	mustExec(t, sess, "SAVEPOINT a")
	mustExec(t, sess, "INSERT INTO t VALUES (2, 2)")
	mustExec(t, sess, "SAVEPOINT b")
	mustExec(t, sess, "INSERT INTO t VALUES (3, 3)")
	mustExec(t, sess, "ROLLBACK TO SAVEPOINT b")
	if got := idList(t, sess); !eqStrings(got, []string{"1", "2"}) {
		t.Fatalf("rollback to inner = %v", got)
	}
	// Rolling back to the outer savepoint destroys the inner one.
	mustExec(t, sess, "ROLLBACK TO SAVEPOINT a")
	if got := idList(t, sess); !eqStrings(got, []string{"1"}) {
		t.Fatalf("rollback to outer = %v", got)
	}
	if _, err := sess.Exec("ROLLBACK TO SAVEPOINT b"); err == nil {
		t.Fatal("a savepoint established after the rollback target survived")
	}
	mustExec(t, sess, "COMMIT")
}

func TestSavepointRelease(t *testing.T) {
	sess := newSavepointSession(t)
	mustExec(t, sess, "BEGIN")
	mustExec(t, sess, "SAVEPOINT a")
	mustExec(t, sess, "INSERT INTO t VALUES (1, 1)")
	mustExec(t, sess, "RELEASE SAVEPOINT a")
	// RELEASE keeps the work and drops the mark.
	if got := idList(t, sess); !eqStrings(got, []string{"1"}) {
		t.Fatalf("after RELEASE = %v", got)
	}
	if _, err := sess.Exec("ROLLBACK TO SAVEPOINT a"); err == nil {
		t.Fatal("rolled back to a released savepoint")
	}
	mustExec(t, sess, "COMMIT")
	if got := idList(t, sess); !eqStrings(got, []string{"1"}) {
		t.Fatalf("after COMMIT = %v", got)
	}
}

// Re-using a live name replaces the savepoint, as in the standard.
func TestSavepointNameReuse(t *testing.T) {
	sess := newSavepointSession(t)
	mustExec(t, sess, "BEGIN")
	mustExec(t, sess, "INSERT INTO t VALUES (1, 1)")
	mustExec(t, sess, "SAVEPOINT a")
	mustExec(t, sess, "INSERT INTO t VALUES (2, 2)")
	mustExec(t, sess, "SAVEPOINT a")
	mustExec(t, sess, "INSERT INTO t VALUES (3, 3)")
	mustExec(t, sess, "ROLLBACK TO SAVEPOINT a")
	if got := idList(t, sess); !eqStrings(got, []string{"1", "2"}) {
		t.Fatalf("rollback to the replaced savepoint = %v, want [1 2]", got)
	}
	mustExec(t, sess, "COMMIT")
}

// A whole-transaction ROLLBACK after a partial one still reverts everything.
func TestSavepointThenFullRollback(t *testing.T) {
	sess := newSavepointSession(t)
	mustExec(t, sess, "INSERT INTO t VALUES (1, 1)")
	mustExec(t, sess, "BEGIN")
	mustExec(t, sess, "INSERT INTO t VALUES (2, 2)")
	mustExec(t, sess, "SAVEPOINT a")
	mustExec(t, sess, "INSERT INTO t VALUES (3, 3)")
	mustExec(t, sess, "UPDATE t SET n = 77 WHERE id = 1")
	mustExec(t, sess, "ROLLBACK TO SAVEPOINT a")
	mustExec(t, sess, "ROLLBACK")
	got := rowsOf(t, sess, "SELECT id, n FROM t ORDER BY id")
	if len(got) != 1 || got[0][0] != "1" || got[0][1] != "1" {
		t.Fatalf("after full rollback = %v, want only the pre-transaction row", got)
	}
}

func TestSavepointRefusals(t *testing.T) {
	sess := newSavepointSession(t)
	// Outside a transaction there is nothing to mark.
	if _, err := sess.Exec("SAVEPOINT a"); err == nil {
		t.Fatal("SAVEPOINT outside a transaction was accepted")
	}
	if _, err := sess.Exec("ROLLBACK TO SAVEPOINT a"); err == nil {
		t.Fatal("ROLLBACK TO outside a transaction was accepted")
	}
	mustExec(t, sess, "BEGIN")
	if _, err := sess.Exec("ROLLBACK TO SAVEPOINT nope"); err == nil {
		t.Fatal("rolled back to a savepoint that was never set")
	}
	if _, err := sess.Exec("RELEASE SAVEPOINT nope"); err == nil {
		t.Fatal("released a savepoint that was never set")
	}
	mustExec(t, sess, "ROLLBACK")
}

// A schema change is not part of the row undo chain a savepoint reverses, so
// crossing one is refused rather than half-applied.
func TestSavepointRefusesCrossingDDL(t *testing.T) {
	sess := newSavepointSession(t)
	mustExec(t, sess, "BEGIN")
	mustExec(t, sess, "SAVEPOINT a")
	mustExec(t, sess, "CREATE TABLE later (id INT64 PRIMARY KEY)")
	_, err := sess.Exec("ROLLBACK TO SAVEPOINT a")
	if err == nil {
		t.Fatal("rolled back across a schema change")
	}
	if !strings.Contains(err.Error(), "schema change") {
		t.Fatalf("error does not explain the refusal: %v", err)
	}
	mustExec(t, sess, "ROLLBACK")
}

func TestSavepointStackIsBounded(t *testing.T) {
	sess := newSavepointSession(t)
	mustExec(t, sess, "BEGIN")
	for i := 0; i < MaxSavepoints; i++ {
		if _, err := sess.Exec("SAVEPOINT s" + string(rune('a'+i%26)) + string(rune('a'+i/26))); err != nil {
			t.Fatalf("savepoint %d: %v", i, err)
		}
	}
	if _, err := sess.Exec("SAVEPOINT overflow"); err == nil {
		t.Fatal("the savepoint stack is unbounded")
	}
	mustExec(t, sess, "ROLLBACK")
}

// Savepoints belong to the transaction, so the stack cannot outlive it.
func TestSavepointClearedAtTransactionEnd(t *testing.T) {
	sess := newSavepointSession(t)
	mustExec(t, sess, "BEGIN")
	mustExec(t, sess, "SAVEPOINT a")
	mustExec(t, sess, "COMMIT")
	mustExec(t, sess, "BEGIN")
	if _, err := sess.Exec("ROLLBACK TO SAVEPOINT a"); err == nil {
		t.Fatal("a savepoint survived its transaction")
	}
	mustExec(t, sess, "ROLLBACK")
}

// Secondary indexes are separate trees with their own undo records, so a
// partial rollback has to reverse them too — otherwise a unique index would
// keep a key for a row that no longer exists, and an index scan would return
// a row a heap scan does not.
func TestSavepointReversesSecondaryIndexes(t *testing.T) {
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE t (id INT64 PRIMARY KEY, email STRING, n INT64)")
	mustExec(t, sess, "CREATE UNIQUE INDEX t_email ON t (email)")
	mustExec(t, sess, "CREATE INDEX t_n ON t (n)")
	mustExec(t, sess, "INSERT INTO t VALUES (1, 'a@x', 1)")

	mustExec(t, sess, "BEGIN")
	mustExec(t, sess, "SAVEPOINT a")
	mustExec(t, sess, "INSERT INTO t VALUES (2, 'b@x', 2)")
	mustExec(t, sess, "ROLLBACK TO SAVEPOINT a")
	// The unique key must be free again.
	mustExec(t, sess, "INSERT INTO t VALUES (3, 'b@x', 3)")
	mustExec(t, sess, "COMMIT")

	byIndex := idsOf(t, sess, "SELECT id FROM t WHERE email = 'b@x'")
	if !eqStrings(byIndex, []string{"3"}) {
		t.Fatalf("unique index lookup = %v, want [3]", byIndex)
	}
	all := idsOf(t, sess, "SELECT id FROM t ORDER BY id")
	if !eqStrings(all, []string{"1", "3"}) {
		t.Fatalf("heap scan = %v, want [1 3]", all)
	}
	ranged := idsOf(t, sess, "SELECT id FROM t WHERE n >= 1 ORDER BY id")
	if !eqStrings(ranged, []string{"1", "3"}) {
		t.Fatalf("index range scan = %v, want [1 3]", ranged)
	}
}
