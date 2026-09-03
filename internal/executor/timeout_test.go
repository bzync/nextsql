package executor

import (
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/scheduler"
)

// TestStatementTimeoutAbortsScan is a regression test for a real gap found
// while auditing Phase 27 "Statement timeout": scheduler.Limits.Time was
// already wired into a per-statement scheduler.Budget with a real deadline
// context, but nothing in the base SeqScan/IndexScan row-emission loops
// (internal/executor/access.go) ever called Budget.Check() — only
// specialized paths (ANALYZE, vector/full-text search, index rebuild,
// partition maintenance) did. A plain SELECT could run past its statement
// budget unbounded. access.go's six physical scan callbacks now check the
// budget per row, matching the convention already used elsewhere (e.g.
// populatePartitionIndex).
func TestStatementTimeoutAbortsScan(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('a'), ('b'), ('c')`)

	lim := scheduler.DefaultLimits()
	lim.Time = time.Nanosecond
	s.SetLimits(lim)
	if _, err := s.Exec(`SELECT * FROM t`); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("SELECT with an already-expired statement budget = %v, want Exhausted", err)
	}
}

func TestStatementTimeoutDoesNotAffectNormalQueries(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`)
	lim := scheduler.DefaultLimits()
	lim.Time = 30 * time.Second
	s.SetLimits(lim)
	execOK(t, s, `INSERT INTO t (n) VALUES ('a'), ('b')`)
	res := execOK(t, s, `SELECT * FROM t`)
	if len(res.Rows) != 2 {
		t.Fatalf("rows=%d, want 2", len(res.Rows))
	}
}

func TestTransactionTimeoutAbortsNextStatement(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`)

	s.SetTxnTimeout(20 * time.Millisecond)
	execOK(t, s, `BEGIN`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('a')`)
	time.Sleep(30 * time.Millisecond)

	if _, err := s.Exec(`INSERT INTO t (n) VALUES ('b')`); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("statement after transaction timeout = %v, want Exhausted", err)
	}
	if s.InTxn() {
		t.Fatal("transaction must be force-aborted on timeout, not left open")
	}

	// The forced abort must have rolled back 'a'; a fresh transaction works.
	execOK(t, s, `BEGIN`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('c')`)
	execOK(t, s, `COMMIT`)
	res := execOK(t, s, `SELECT * FROM t`)
	if len(res.Rows) != 1 || res.Rows[0][1].Str != "c" {
		t.Fatalf("rows after recovery = %+v, want only 'c' ('a' should have rolled back)", res.Rows)
	}
}

// TestTransactionTimeoutRejectsCommitToo confirms the abort is unconditional
// once the deadline has passed — the client cannot "sneak in" a clean COMMIT
// after overstaying the budget.
func TestTransactionTimeoutRejectsCommitToo(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`)
	s.SetTxnTimeout(20 * time.Millisecond)
	execOK(t, s, `BEGIN`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('a')`)
	time.Sleep(30 * time.Millisecond)
	if _, err := s.Exec(`COMMIT`); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("COMMIT past the transaction timeout = %v, want Exhausted", err)
	}
	res := execOK(t, s, `SELECT * FROM t`)
	if len(res.Rows) != 0 {
		t.Fatalf("rows=%+v, want none ('a' must not have committed)", res.Rows)
	}
}

func TestTransactionTimeoutZeroIsUnbounded(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`)
	// s.txnTimeout defaults to 0 (disabled); a slow transaction must not abort.
	execOK(t, s, `BEGIN`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('a')`)
	time.Sleep(20 * time.Millisecond)
	execOK(t, s, `INSERT INTO t (n) VALUES ('b')`)
	execOK(t, s, `COMMIT`)
	res := execOK(t, s, `SELECT * FROM t`)
	if len(res.Rows) != 2 {
		t.Fatalf("rows=%d, want 2", len(res.Rows))
	}
}

// TestLockWaitTimeoutEndToEnd exercises DB.SetLockWaitTimeout through a real
// blocking two-session scenario, mirroring the blocking-FK-check pattern
// already used by TestFKSnapshotOverlappingLocks: an outbound FK-checking
// INSERT held open in one session blocks a conflicting DELETE in another.
func TestLockWaitTimeoutEndToEnd(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (id STRING PRIMARY KEY, parent_id STRING NOT NULL REFERENCES parents (id))`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)

	db.SetLockWaitTimeout(30 * time.Millisecond)

	t1 := db.Session()
	t2 := db.Session()
	execOK(t, t1, `BEGIN SNAPSHOT`)
	execOK(t, t1, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1')`)

	start := time.Now()
	_, err := t2.Exec(`DELETE FROM parents WHERE id = 'p1'`)
	elapsed := time.Since(start)
	if !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("DELETE against a held lock = %v, want Exhausted", err)
	}
	if elapsed < 30*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("DELETE returned after %v, want roughly the 30ms lock wait timeout", elapsed)
	}
	_ = t1.abort()

	// The timed-out waiter must not have leaked the lock: a fresh DELETE
	// after the holder releases must succeed normally.
	if _, err := t2.Exec(`DELETE FROM parents WHERE id = 'p1'`); err != nil {
		t.Fatalf("DELETE after the holder released: %v", err)
	}
}

func TestLockWaitTimeoutDefaultBlocksIndefinitely(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (id STRING PRIMARY KEY, parent_id STRING NOT NULL REFERENCES parents (id))`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)

	t1 := db.Session()
	t2 := db.Session()
	execOK(t, t1, `BEGIN SNAPSHOT`)
	execOK(t, t1, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1')`)

	done := make(chan error, 1)
	go func() { _, err := t2.Exec(`DELETE FROM parents WHERE id = 'p1'`); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("DELETE returned early with no lock timeout configured: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	// Roll back rather than commit so the child never persists and the
	// blocked DELETE can succeed once the lock is released.
	if err := t1.abort(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("DELETE after rollback: %v", err)
	}
}
