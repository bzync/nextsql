package crash

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/storage/btree"
	"github.com/bzync/nextsql/internal/txn"
)

// A checkpoint may not move the redo boundary past a committed change that is
// not yet on disk.
//
// Commits log page images; the pages themselves reach the data file later,
// when the buffer pool may flush them. A checkpoint flushes what it can and
// then records a redo boundary. It used to take that boundary as the log's end,
// but a page modified by a still-running transaction cannot be flushed, so a
// committed image of that page could sit below the new boundary with the page
// on disk still older. After a crash, recovery started past the image and the
// acknowledged row was gone. The boundary must be the oldest logged change any
// dirty page has not yet written.
func TestCheckpointDoesNotSkipAnUnflushedCommittedChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := executor.Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	committer := db.Session()
	if _, err := committer.Exec(`CREATE TABLE r (id INT64 PRIMARY KEY, v STRING)`); err != nil {
		t.Fatal(err)
	}
	// Committed and acknowledged: its image is in the WAL, its page only in
	// the buffer pool.
	if _, err := committer.Exec(`INSERT INTO r (id, v) VALUES (1, 'acknowledged')`); err != nil {
		t.Fatal(err)
	}
	// A running transaction dirties the same leaf, so the checkpoint cannot
	// flush it.
	writer := db.Session()
	if _, err := writer.Exec(`BEGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(`INSERT INTO r (id, v) VALUES (2, 'uncommitted')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Eng.Checkpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	db.Eng.Kill() // power loss

	reopened, err := executor.Open(path, keys, 64)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	res, err := reopened.Session().Exec(`SELECT id, v FROM r ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprint(res.Rows)
	if len(res.Rows) != 1 || res.Rows[0][0].Int != 1 {
		t.Fatalf("after checkpoint + crash, rows = %s; want exactly the acknowledged row 1", got)
	}
}

// The same property at the storage layer, where nothing but the engine runs:
// a committed key whose page a running transaction has since dirtied must
// survive a checkpoint followed by power loss.
func TestStorageCheckpointKeepsCommittedKeyOnDirtiedPage(t *testing.T) {
	forEachRedoMode(t, func(t *testing.T, mode storage.PageDeltaMode) {
		dir := t.TempDir()
		path := filepath.Join(dir, "nextsql.db")
		keys := testKeys(t)
		e, err := storage.CreateWith(path, keys, 32, storage.OpenOptions{PageDeltas: mode})
		if err != nil {
			t.Fatal(err)
		}
		tr, err := btree.Create(e)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Checkpoint(); err != nil { // start from a clean, flushed state
			t.Fatal(err)
		}
		committed, err := tr.BeginTxn(txn.SnapshotIsolation)
		if err != nil {
			t.Fatal(err)
		}
		if err := committed.Insert([]byte("a"), []byte("acknowledged")); err != nil {
			t.Fatal(err)
		}
		if err := committed.Commit(); err != nil {
			t.Fatal(err)
		}
		running, err := tr.BeginTxn(txn.SnapshotIsolation)
		if err != nil {
			t.Fatal(err)
		}
		if err := running.Insert([]byte("b"), []byte("uncommitted")); err != nil {
			t.Fatal(err)
		}
		if err := e.Checkpoint(); err != nil {
			t.Fatal(err)
		}
		e.Kill()

		e2, tr2 := reopen(t, path, keys, 32)
		defer e2.Close()
		if _, err := tr2.Lookup([]byte("a")); err != nil {
			t.Fatalf("acknowledged key lost after checkpoint + power loss: %v", err)
		}
		if _, err := tr2.Lookup([]byte("b")); !nerr.HasCode(err, nerr.NotFound) {
			t.Fatalf("uncommitted key visible after recovery: %v", err)
		}
	})
}

// A checkpoint may not move the redo boundary past the Begin record of a
// transaction that is still running.
//
// Recovery finds the transactions to undo by scanning for Begin records from
// the boundary; an id it never sees defaults to committed. A later commit on a
// shared page logs an image carrying the running transaction's row version, so
// a boundary placed at that image -- after the Begin -- brought the
// uncommitted version back after a crash, as committed, with no undo.
func TestStorageCheckpointDoesNotSkipARunningTransactionsBegin(t *testing.T) {
	forEachRedoMode(t, func(t *testing.T, mode storage.PageDeltaMode) {
		dir := t.TempDir()
		path := filepath.Join(dir, "nextsql.db")
		keys := testKeys(t)
		e, err := storage.CreateWith(path, keys, 32, storage.OpenOptions{PageDeltas: mode})
		if err != nil {
			t.Fatal(err)
		}
		tr, err := btree.Create(e)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Checkpoint(); err != nil {
			t.Fatal(err)
		}
		running, err := tr.BeginTxn(txn.SnapshotIsolation)
		if err != nil {
			t.Fatal(err)
		}
		if err := running.Insert([]byte("b"), []byte("uncommitted")); err != nil {
			t.Fatal(err)
		}
		// Committed after the running transaction's first write, on the same leaf.
		committed, err := tr.BeginTxn(txn.SnapshotIsolation)
		if err != nil {
			t.Fatal(err)
		}
		if err := committed.Insert([]byte("c"), []byte("acknowledged")); err != nil {
			t.Fatal(err)
		}
		if err := committed.Commit(); err != nil {
			t.Fatal(err)
		}
		if err := e.Checkpoint(); err != nil {
			t.Fatal(err)
		}
		e.Kill()

		e2, tr2 := reopen(t, path, keys, 32)
		defer e2.Close()
		if _, err := tr2.Lookup([]byte("c")); err != nil {
			t.Fatalf("acknowledged key lost after checkpoint + power loss: %v", err)
		}
		if _, err := tr2.Lookup([]byte("b")); !nerr.HasCode(err, nerr.NotFound) {
			t.Fatalf("a transaction running at the checkpoint came back as committed: %v", err)
		}
	})
}

// forEachRedoMode runs a storage-level crash test once logging full page images
// only and once with page deltas, since a checkpoint's redo boundary must hold
// for both.
func forEachRedoMode(t *testing.T, fn func(*testing.T, storage.PageDeltaMode)) {
	for _, m := range []struct {
		name string
		mode storage.PageDeltaMode
	}{{"full-images", storage.PageDeltasOff}, {"page-deltas", storage.PageDeltasOn}} {
		t.Run(m.name, func(t *testing.T) { fn(t, m.mode) })
	}
}
