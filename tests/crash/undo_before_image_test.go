package crash

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/storage/btree"
	diskio "github.com/bzync/nextsql/internal/storage/io"
	"github.com/bzync/nextsql/internal/txn"
	"github.com/bzync/nextsql/internal/undo"
)

// A logged page image must not reach the WAL ahead of the undo records for the
// row versions it carries.
//
// Pages are shared, so a committed image -- a commit's, or the system
// transaction that logs a split -- can carry another transaction's
// uncommitted version. Only that transaction's undo record holds the value it
// replaced. Undo records are buffered in memory, and the image was copied and
// appended without writing that buffer first (a split never wrote it; a
// commit wrote it before copying, leaving a window for another transaction's
// write). After a crash, redo installed the image, the undo record was gone,
// and recovery could not put the old value back: the balance of an account a
// rolled-back transfer had touched came back changed
// (TestRollbacksWithPageDeltasSurvivePowerLoss, about one round in 45).
//
// Here a running transaction updates a key, a second transaction splits the
// same leaf, and the WAL -- but nothing else -- is made durable before a
// process crash.
func TestPageImageDoesNotOutrunTheUndoItCarries(t *testing.T) {
	forEachRedoMode(t, func(t *testing.T, mode storage.PageDeltaMode) {
		pageImageCarriesUncommittedVersion(t, mode, false)
	})
}

// The same, across power loss: undo bytes written but never fsynced are gone.
// Writing the undo record before the image entered the WAL was not enough --
// the WAL fsync made the image durable while the undo write sat in the page
// cache. The WAL now runs the undo log's Sync before writing any record
// (wal.Log.SetBeforeWrite).
func TestPageImageDoesNotOutrunTheUndoItCarriesAcrossPowerLoss(t *testing.T) {
	forEachRedoMode(t, func(t *testing.T, mode storage.PageDeltaMode) {
		pageImageCarriesUncommittedVersion(t, mode, true)
	})
}

func pageImageCarriesUncommittedVersion(t *testing.T, mode storage.PageDeltaMode, powerLoss bool) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	dropUnsyncedUndo := trackUndoSync(t, path)
	e, err := storage.CreateWith(path, keys, 64, storage.OpenOptions{PageDeltas: mode})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	key := func(i int) []byte { return []byte(fmt.Sprintf("k%05d", i)) }
	setup, err := tr.BeginTxn(txn.SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i += 2 {
		if err := setup.Insert(key(i), []byte("committed")); err != nil {
			t.Fatal(err)
		}
	}
	if err := setup.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := e.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	running, err := tr.BeginTxn(txn.SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	if err := running.Update(key(4), []byte("uncommitted")); err != nil {
		t.Fatal(err)
	}
	splitter, err := tr.BeginTxn(txn.SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	pad := []byte(strings.Repeat("x", 400))
	for i := 1; i < 200; i += 2 {
		if err := splitter.Insert(key(i), pad); err != nil {
			t.Fatal(err)
		}
	}
	// The split images are logged as a committed system transaction;
	// make them durable the way any later group commit would.
	if err := e.WAL.Flush(e.WAL.NextLSN() - 1); err != nil {
		t.Fatal(err)
	}
	e.Kill()
	if powerLoss {
		dropUnsyncedUndo()
	}

	e2, tr2 := reopen(t, path, keys, 64)
	defer e2.Close()
	got, err := tr2.Lookup(key(4))
	if err != nil {
		t.Fatalf("committed key after recovery: %v", err)
	}
	if string(got) != "committed" {
		t.Fatalf("committed key after recovery = %q, want %q: an uncommitted update survived because its undo record did not", got, "committed")
	}
}

// trackUndoSync models power loss for the undo log of the database at dbPath:
// it records the log's size at every fsync, and the returned function cuts
// the log back to it after Engine.Kill, discarding writes that never reached
// stable storage -- what wal.Log.CrashClose already does for the WAL.
func trackUndoSync(t *testing.T, dbPath string) (dropUnsynced func()) {
	t.Helper()
	logPath := filepath.Join(undo.DirFor(dbPath), "undo.log")
	var mu sync.Mutex
	var synced int64
	restore := diskio.SetFaultForTest(func(f diskio.Fault) error {
		if (f.Op == "datasync" || f.Op == "sync") && f.Path == logPath {
			if st, err := os.Stat(logPath); err == nil {
				mu.Lock()
				synced = st.Size()
				mu.Unlock()
			}
		}
		return nil
	})
	t.Cleanup(restore)
	return func() {
		mu.Lock()
		defer mu.Unlock()
		if err := os.Truncate(logPath, synced); err != nil {
			t.Fatalf("drop unsynced undo: %v", err)
		}
	}
}
