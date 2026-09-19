package wal

import (
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	diskio "github.com/bzync/nextsql/internal/storage/io"
)

// The before-write hook runs ahead of every segment write, and a failing hook
// leaves nothing written and the flush retryable. The engine installs the undo
// log's Sync here: a committed page image found on disk is replayed whether or
// not its fsync returned, so the undo record reversing any uncommitted version
// it carries must be durable before its bytes are written at all.
func TestBeforeWriteRunsAheadOfTheSegmentWrite(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	lg, err := Create(dir, keys, id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	var ran, writesBeforeHook atomic.Int64
	var fail atomic.Pointer[error]
	restore := diskio.SetFaultForTest(func(f diskio.Fault) error {
		if f.Op == "write" && strings.HasPrefix(f.Path, dir) && ran.Load() == 0 {
			writesBeforeHook.Add(1)
		}
		return nil
	})
	defer restore()
	lg.SetBeforeWrite(func() error {
		if e := fail.Load(); e != nil {
			return *e
		}
		ran.Add(1)
		return nil
	})

	injected := errors.New("undo sync failed")
	fail.Store(&injected)
	txn := lg.AllocTxn()
	if _, err := lg.Append(BeginRec(txn)); err != nil {
		t.Fatal(err)
	}
	lsn, err := lg.Append(CommitRec(txn, 1))
	if err != nil {
		t.Fatal(err)
	}
	written := lg.BytesWritten()
	if err := lg.Flush(lsn); !errors.Is(err, injected) {
		t.Fatalf("Flush with a failing before-write hook = %v, want %v", err, injected)
	}
	if lg.DurableLSN() >= lsn || lg.BytesWritten() != written {
		t.Fatalf("a failed hook still wrote: durable %d (lsn %d), bytes %d -> %d", lg.DurableLSN(), lsn, written, lg.BytesWritten())
	}
	if n := writesBeforeHook.Load(); n != 0 {
		t.Fatalf("%d segment writes happened with the hook failing", n)
	}

	fail.Store(nil)
	if err := lg.Flush(lsn); err != nil {
		t.Fatalf("Flush after the hook recovered: %v", err)
	}
	if ran.Load() == 0 || lg.DurableLSN() < lsn {
		t.Fatalf("hook ran %d times, durable %d, want >= %d", ran.Load(), lg.DurableLSN(), lsn)
	}
	if n := writesBeforeHook.Load(); n != 0 {
		t.Fatalf("%d segment writes preceded the hook", n)
	}
}
