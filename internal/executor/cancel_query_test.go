package executor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/crypto"
)

// NextSQL had no way for an operator to stop someone else's runaway
// statement: cancellation was client-driven only, so a query could be seen in
// system.active_queries and not acted on.
func newCancelDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 64)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// waitForActiveQuery blocks until the given SQL text is visible as a running
// statement, and returns its query id.
func waitForActiveQuery(t *testing.T, observer *Session, sql string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rows := rowsOf(t, observer, "SELECT query_id, sql FROM system.active_queries")
		for _, r := range rows {
			if len(r) >= 2 && r[1] == sql {
				return r[0]
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("query never appeared in system.active_queries: %s", sql)
	return ""
}

func TestCancelQueryStopsAnotherSession(t *testing.T) {
	db := newCancelDB(t)
	setup := db.Session()
	mustExec(t, setup, "CREATE TABLE t (id INT64 PRIMARY KEY, n INT64)")
	mustExec(t, setup, "INSERT INTO t VALUES (1, 1)")

	// Hold a row lock so the victim's UPDATE blocks instead of finishing.
	// SERIALIZABLE is what makes the victim wait on the lock rather than fail
	// fast with a write-write conflict.
	holder := db.Session()
	mustExec(t, holder, "BEGIN SERIALIZABLE")
	mustExec(t, holder, "UPDATE t SET n = 2 WHERE id = 1")

	const waitingSQL = "UPDATE t SET n = 3 WHERE id = 1"
	victim := db.Session()
	mustExec(t, victim, "BEGIN SERIALIZABLE")
	// Only a registered session is visible to cross-session introspection,
	// which is what CANCEL QUERY searches. The protocol server registers each
	// connection; a test has to do it explicitly.
	vid := db.RegisterSession(victim)
	t.Cleanup(func() { db.UnregisterSession(vid) })
	done := make(chan error, 1)
	go func() {
		_, err := victim.QueryContext(context.Background(), waitingSQL, nil)
		done <- err
	}()

	observer := db.Session()
	qid := waitForActiveQuery(t, observer, waitingSQL)

	if _, err := observer.Exec("CANCEL QUERY '" + qid + "'"); err != nil {
		t.Fatalf("CANCEL QUERY: %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the cancelled statement completed successfully")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CANCEL QUERY did not stop the lock-waiting statement")
	}
	mustExec(t, holder, "ROLLBACK")
}

// Cancelling a query id that is not running is not an error: the statement
// asked for it to stop, and it has.
func TestCancelQueryUnknownIDIsNotAnError(t *testing.T) {
	db := newCancelDB(t)
	sess := db.Session()
	if _, err := sess.Exec("CANCEL QUERY '999999'"); err != nil {
		t.Fatalf("cancelling a finished query reported an error: %v", err)
	}
}

func TestCancelQueryRejectsInvalidID(t *testing.T) {
	db := newCancelDB(t)
	sess := db.Session()
	for _, sql := range []string{
		"CANCEL QUERY 'abc'",
		"CANCEL QUERY ''",
		"CANCEL QUERY '-1'",
		"CANCEL QUERY",
	} {
		if _, err := sess.Exec(sql); err == nil {
			t.Fatalf("accepted an invalid statement: %s", sql)
		}
	}
}

// CANCEL TASK must keep working unchanged.
func TestCancelTaskStillParses(t *testing.T) {
	db := newCancelDB(t)
	sess := db.Session()
	_, err := sess.Exec("CANCEL TASK 'nope'")
	if err != nil && strings.Contains(err.Error(), "syntax") {
		t.Fatalf("CANCEL TASK stopped parsing: %v", err)
	}
}
