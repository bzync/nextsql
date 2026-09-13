package executor

import (
	"strings"
	"testing"
)

// Two transactions could both write the same row, both commit, and one write
// would vanish with nothing reported to either side — a lost update at the
// default isolation level.
//
// The cause was not the conflict check but the lock that was supposed to make
// it unnecessary: btree.Txn.lockWrite skips locking when only one transaction
// is active at that moment, so the *first* writer of a row often left no lock
// behind at all. A second transaction starting afterwards then found nothing
// to block on, and the snapshot conflict check only fires when the other
// writer has already committed — an in-progress writer was invisible to it.
//
// Overwriting a row an unfinished transaction has written is a dirty write,
// which SQL forbids at every isolation level, so the check is not conditional
// on one.
func twoWriters(t *testing.T, begin string) (secondErr error, final string) {
	t.Helper()
	db := newCancelDB(t)
	setup := db.Session()
	mustExec(t, setup, "CREATE TABLE t (id INT64 PRIMARY KEY, n INT64)")
	mustExec(t, setup, "INSERT INTO t VALUES (1, 1)")

	a := db.Session()
	b := db.Session()
	mustExec(t, a, begin)
	mustExec(t, a, "UPDATE t SET n = 2 WHERE id = 1")
	mustExec(t, b, begin)
	_, secondErr = b.Exec("UPDATE t SET n = 3 WHERE id = 1")
	_, _ = a.Exec("COMMIT")
	_, _ = b.Exec("COMMIT")
	rows := rowsOf(t, setup, "SELECT n FROM t WHERE id = 1")
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %v", rows)
	}
	return secondErr, rows[0][0]
}

func TestConcurrentUpdateDoesNotLoseAWrite(t *testing.T) {
	// SERIALIZABLE is excluded here: it blocks on the key lock instead of
	// failing fast, which a single-goroutine test cannot express.
	for _, begin := range []string{"BEGIN", "BEGIN SNAPSHOT", "BEGIN READ COMMITTED"} {
		t.Run(begin, func(t *testing.T) {
			err, final := twoWriters(t, begin)
			if err == nil {
				t.Fatalf("%s: the second writer overwrote an uncommitted row", begin)
			}
			if !strings.Contains(err.Error(), "write-write conflict") {
				t.Fatalf("%s: want a write-write conflict, got %v", begin, err)
			}
			// The first writer's value is the one that survives.
			if final != "2" {
				t.Fatalf("%s: final value %q, want the first writer's 2", begin, final)
			}
		})
	}
}

// A DELETE and an INSERT over the same uncommitted row are dirty writes too.
func TestConcurrentDeleteAndInsertConflict(t *testing.T) {
	db := newCancelDB(t)
	setup := db.Session()
	mustExec(t, setup, "CREATE TABLE t (id INT64 PRIMARY KEY, n INT64)")
	mustExec(t, setup, "INSERT INTO t VALUES (1, 1)")

	a := db.Session()
	b := db.Session()
	mustExec(t, a, "BEGIN")
	mustExec(t, a, "UPDATE t SET n = 2 WHERE id = 1")
	mustExec(t, b, "BEGIN")
	if _, err := b.Exec("DELETE FROM t WHERE id = 1"); err == nil {
		t.Fatal("a DELETE removed a row an unfinished transaction had written")
	}
	_, _ = b.Exec("ROLLBACK")
	_, _ = a.Exec("COMMIT")

	// Now the insert side: a's delete leaves a tombstone b must not write over.
	c := db.Session()
	d := db.Session()
	mustExec(t, c, "BEGIN")
	mustExec(t, c, "DELETE FROM t WHERE id = 1")
	mustExec(t, d, "BEGIN")
	if _, err := d.Exec("INSERT INTO t VALUES (1, 9)"); err == nil {
		t.Fatal("an INSERT wrote over a row an unfinished transaction had deleted")
	}
	_, _ = d.Exec("ROLLBACK")
	_, _ = c.Exec("COMMIT")
}

// A transaction writing the same row twice is not conflicting with itself.
func TestOwnWriteIsNotAConflict(t *testing.T) {
	db := newCancelDB(t)
	sess := db.Session()
	mustExec(t, sess, "CREATE TABLE t (id INT64 PRIMARY KEY, n INT64)")
	mustExec(t, sess, "INSERT INTO t VALUES (1, 1)")
	mustExec(t, sess, "BEGIN")
	mustExec(t, sess, "UPDATE t SET n = 2 WHERE id = 1")
	mustExec(t, sess, "UPDATE t SET n = 3 WHERE id = 1")
	mustExec(t, sess, "DELETE FROM t WHERE id = 1")
	mustExec(t, sess, "INSERT INTO t VALUES (1, 4)")
	mustExec(t, sess, "COMMIT")
	got := rowsOf(t, sess, "SELECT n FROM t WHERE id = 1")
	if len(got) != 1 || got[0][0] != "4" {
		t.Fatalf("own repeated writes = %v, want 4", got)
	}
}

// Once the first writer finishes, the row is writable again: an aborted
// writer leaves nothing behind, and a committed one is overwritten normally
// under READ COMMITTED.
func TestRowWritableAfterFirstWriterFinishes(t *testing.T) {
	db := newCancelDB(t)
	setup := db.Session()
	mustExec(t, setup, "CREATE TABLE t (id INT64 PRIMARY KEY, n INT64)")
	mustExec(t, setup, "INSERT INTO t VALUES (1, 1)")

	a := db.Session()
	mustExec(t, a, "BEGIN")
	mustExec(t, a, "UPDATE t SET n = 2 WHERE id = 1")
	mustExec(t, a, "ROLLBACK")
	b := db.Session()
	mustExec(t, b, "UPDATE t SET n = 5 WHERE id = 1")
	if got := rowsOf(t, setup, "SELECT n FROM t WHERE id = 1"); got[0][0] != "5" {
		t.Fatalf("after the first writer aborted = %v, want 5", got)
	}

	c := db.Session()
	mustExec(t, c, "BEGIN READ COMMITTED")
	mustExec(t, c, "UPDATE t SET n = 6 WHERE id = 1")
	mustExec(t, c, "COMMIT")
	d := db.Session()
	mustExec(t, d, "BEGIN READ COMMITTED")
	mustExec(t, d, "UPDATE t SET n = 7 WHERE id = 1")
	mustExec(t, d, "COMMIT")
	if got := rowsOf(t, setup, "SELECT n FROM t WHERE id = 1"); got[0][0] != "7" {
		t.Fatalf("sequential committed writers = %v, want 7", got)
	}
}
