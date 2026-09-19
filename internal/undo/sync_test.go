package undo

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	diskio "github.com/bzync/nextsql/internal/storage/io"
	"github.com/bzync/nextsql/internal/storage/row"
)

func newSyncTestLog(t *testing.T) (*Log, string, format.Identity, *crypto.MemoryKeyProvider) {
	t.Helper()
	id, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "db.undo")
	keys := testKeys(t)
	lg, err := Create(dir, keys, id)
	if err != nil {
		t.Fatal(err)
	}
	return lg, dir, id, keys
}

func appendRec(t *testing.T, lg *Log, txn format.TxnID, key string) format.UndoID {
	t.Helper()
	uid, err := lg.Append(Record{Txn: txn, Kind: KindUpdate, Key: []byte(key), Old: row.Version{Xmin: 1, Payload: []byte("old-" + key)}})
	if err != nil {
		t.Fatal(err)
	}
	return uid
}

// countSyncs counts fsyncs of the undo log under dir, failing them with fail
// while it is set.
func countSyncs(t *testing.T, dir string, fail *atomic.Pointer[error]) *atomic.Int64 {
	t.Helper()
	var n atomic.Int64
	logPath := filepath.Join(dir, logName)
	restore := diskio.SetFaultForTest(func(f diskio.Fault) error {
		if (f.Op == "datasync" || f.Op == "sync") && f.Path == logPath {
			n.Add(1)
			if e := fail.Load(); e != nil {
				return *e
			}
		}
		return nil
	})
	t.Cleanup(restore)
	return &n
}

// Sync makes appended records durable, and costs nothing when there is
// nothing new: the WAL runs it before every segment write.
func TestSyncFsyncsOnlyWhenRecordsAreUnsynced(t *testing.T) {
	lg, dir, _, _ := newSyncTestLog(t)
	defer lg.Close()
	var fail atomic.Pointer[error]
	syncs := countSyncs(t, dir, &fail)

	if err := lg.Sync(); err != nil {
		t.Fatal(err)
	}
	if got := syncs.Load(); got != 0 {
		t.Fatalf("Sync of an empty log fsynced %d times", got)
	}
	appendRec(t, lg, 7, "a")
	if err := lg.Sync(); err != nil {
		t.Fatal(err)
	}
	if got := syncs.Load(); got != 1 {
		t.Fatalf("Sync after an append fsynced %d times, want 1", got)
	}
	if err := lg.Sync(); err != nil {
		t.Fatal(err)
	}
	if got := syncs.Load(); got != 1 {
		t.Fatalf("a second Sync with nothing new fsynced again (%d)", got)
	}
	// Records written by Flush but not yet synced still need the fsync.
	appendRec(t, lg, 7, "b")
	if err := lg.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := lg.Sync(); err != nil {
		t.Fatal(err)
	}
	if got := syncs.Load(); got != 2 {
		t.Fatalf("Sync after a Flush fsynced %d times in total, want 2", got)
	}
}

// A failed fsync latches: the next fsync would not cover what the failed one
// lost, so no WAL image that depends on those records may be written.
func TestSyncLatchesAFailedFsync(t *testing.T) {
	lg, dir, _, _ := newSyncTestLog(t)
	defer lg.Close()
	var fail atomic.Pointer[error]
	injected := errors.New("injected EIO")
	fail.Store(&injected)
	countSyncs(t, dir, &fail)

	appendRec(t, lg, 9, "a")
	first := lg.Sync()
	if first == nil {
		t.Fatal("Sync succeeded through a failed fsync")
	}
	fail.Store(nil)
	if err := lg.Sync(); err == nil || err.Error() != first.Error() {
		t.Fatalf("Sync after a failed fsync = %v, want the latched %v", err, first)
	}
	if err := lg.Flush(); err == nil {
		t.Fatal("Flush after a failed fsync succeeded")
	}
}

// Power loss can leave a partial record at the end of undo.log. Replay stops
// there; Open must also cut it, or every record appended afterwards lands past
// the point the next replay stops and is lost.
func TestOpenCutsATornTailSoLaterRecordsSurvive(t *testing.T) {
	lg, dir, id, keys := newSyncTestLog(t)
	first := appendRec(t, lg, 3, "before")
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, logName)
	st, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	whole := st.Size()
	// A torn record: a valid-looking start, cut short.
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(raw[:len(raw)/2]); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	lg, err = Open(dir, keys, id)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(logPath); err != nil || st.Size() != whole {
		t.Fatalf("after Open the log is %v bytes (err %v), want the %d-byte valid prefix", st.Size(), err, whole)
	}
	after := appendRec(t, lg, 4, "after")
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}

	lg, err = Open(dir, keys, id)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	if _, err := lg.Get(first); err != nil {
		t.Fatalf("record before the torn tail: %v", err)
	}
	if got, err := lg.Get(after); err != nil || string(got.Key) != "after" {
		t.Fatalf("record appended after reopening over a torn tail = %+v, %v: lost", got, err)
	}
}

// FuzzOpenTornTail appends arbitrary bytes after valid records and holds what
// Open's cut must guarantee: it either refuses with a controlled error or
// keeps every record before the tail, never grows the file, and positions
// appends so a record written afterwards survives the next open.
func FuzzOpenTornTail(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("torn"))
	f.Add(make([]byte, HeaderSize+3))
	f.Fuzz(func(t *testing.T, tail []byte) {
		lg, dir, id, keys := newSyncTestLog(t)
		first := appendRec(t, lg, 3, "before")
		if err := lg.Close(); err != nil {
			t.Fatal(err)
		}
		logPath := filepath.Join(dir, logName)
		fh, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fh.Write(tail); err != nil {
			t.Fatal(err)
		}
		_ = fh.Close()
		st, err := os.Stat(logPath)
		if err != nil {
			t.Fatal(err)
		}
		grown := st.Size()

		lg, err = Open(dir, keys, id)
		if err != nil {
			if !nerr.HasCode(err, nerr.InvalidFormat) {
				t.Fatalf("uncontrolled open error: %v", err)
			}
			return
		}
		if st, err := os.Stat(logPath); err != nil || st.Size() > grown {
			t.Fatalf("Open grew the log: %v -> %v (%v)", grown, st.Size(), err)
		}
		if _, err := lg.Get(first); err != nil {
			t.Fatalf("record before the tail lost: %v", err)
		}
		after := appendRec(t, lg, 4, "after")
		if err := lg.Close(); err != nil {
			t.Fatal(err)
		}
		lg, err = Open(dir, keys, id)
		if err != nil {
			t.Fatalf("reopen after appending past the tail: %v", err)
		}
		defer lg.Close()
		if got, err := lg.Get(after); err != nil || string(got.Key) != "after" {
			t.Fatalf("record appended after the cut = %+v, %v", got, err)
		}
	})
}
