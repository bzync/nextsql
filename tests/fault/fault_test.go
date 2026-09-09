// Package fault drives NextSQL's durable I/O boundary into failure —
// disk-full, short writes, failed fsync and EIO — and asserts the engine
// fails closed rather than acknowledging work it did not persist.
//
// The seam (internal/storage/io) is process-global, so no test here may run
// in parallel with another. Every test installs its fault, targets it at one
// specific file, and restores it before returning.
package fault

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bzync/nextsql/internal/backup"
	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/storage/btree"
	"github.com/bzync/nextsql/internal/storage/format"
	diskio "github.com/bzync/nextsql/internal/storage/io"
)

var errDevice = errors.New("injected device failure")

func testKeys(t *testing.T) *crypto.MemoryKeyProvider {
	t.Helper()
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// newDB creates an engine with a primary tree and the given committed rows.
func newDB(t *testing.T, pages int, rows ...string) (path string, keys crypto.KeyProvider, e *storage.Engine, tr *btree.Tree) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "nextsql.db")
	keys = testKeys(t)
	e, err := storage.Create(path, keys, pages)
	if err != nil {
		t.Fatal(err)
	}
	tr, err = btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range rows {
		if err := tr.Insert([]byte(k), []byte("v-"+k)); err != nil {
			t.Fatalf("insert %q: %v", k, err)
		}
	}
	return path, keys, e, tr
}

func reopen(t *testing.T, path string, keys crypto.KeyProvider, pages int) (*storage.Engine, *btree.Tree) {
	t.Helper()
	e, err := storage.Open(path, keys, pages)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	tr, err := btree.Open(e)
	if err != nil {
		_ = e.Close()
		t.Fatalf("reopen tree: %v", err)
	}
	return e, tr
}

func mustHave(t *testing.T, tr *btree.Tree, key string) {
	t.Helper()
	got, err := tr.Lookup([]byte(key))
	if err != nil {
		t.Fatalf("lookup %q: %v", key, err)
	}
	if string(got) != "v-"+key {
		t.Fatalf("lookup %q = %q, want %q", key, got, "v-"+key)
	}
}

// installFault installs fn at the durable I/O seam for the duration of the
// test and asserts, on cleanup, that it actually fired. Without that check a
// path moved off the seam by a later refactor would leave every test here
// passing while proving nothing.
func installFault(t *testing.T, fn func(diskio.Fault) error) {
	t.Helper()
	var fired atomic.Int64
	restore := diskio.SetFaultForTest(func(f diskio.Fault) error {
		err := fn(f)
		if err != nil {
			fired.Add(1)
		}
		return err
	})
	t.Cleanup(func() {
		restore()
		if fired.Load() == 0 {
			t.Error("no fault was ever injected: this test no longer reaches the durable I/O seam")
		}
	})
}

// A page write that fails must not let a checkpoint declare the redo boundary
// advanced: the committed rows only exist in the WAL until their pages land.
func TestCheckpointFailsWhenPageWriteFails(t *testing.T) {
	path, keys, e, tr := newDB(t, 8, "a", "b", "c")

	before := e.File.Superblock()

	failing := true
	installFault(t, func(f diskio.Fault) error {
		if failing && f.Op == "write" && f.Path == path {
			return errDevice
		}
		return nil
	})

	if err := e.Checkpoint(); !nerr.HasCode(err, nerr.IO) {
		t.Fatalf("Checkpoint under a failing data device = %v, want an IO failure", err)
	}
	if after := e.File.Superblock(); after.RedoLSN != before.RedoLSN || after.CheckpointLSN != before.CheckpointLSN {
		t.Fatalf("checkpoint boundary advanced across a failed page write: %+v -> %+v", before, after)
	}

	failing = false
	e.Kill()

	e2, tr2 := reopen(t, path, keys, 8)
	defer e2.Close()
	for _, k := range []string{"a", "b", "c"} {
		mustHave(t, tr2, k)
	}
	_ = tr
}

// An unreadable page must surface as an I/O failure. Reporting it as "no such
// row" would turn a device fault into a silent wrong answer. The tree is built
// large enough, and the pool small enough, that the lookup cannot be served
// from cache — the test asserts a read was actually attempted.
func TestPageReadFaultIsNotReportedAsAMissingRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db")
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 4)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	const rows = 2000
	for i := 0; i < rows; i++ {
		k := fmt.Sprintf("k%06d", i)
		if err := tr.Insert([]byte(k), []byte("v-"+k)); err != nil {
			t.Fatalf("insert %q: %v", k, err)
		}
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	e2, tr2 := reopen(t, path, keys, 4)
	defer e2.Kill()

	installFault(t, func(f diskio.Fault) error {
		if f.Op == "read" && f.Path == path {
			return errDevice
		}
		return nil
	})

	got, lookupErr := tr2.Lookup([]byte(fmt.Sprintf("k%06d", rows-1)))
	if lookupErr == nil {
		t.Fatalf("Lookup under an unreadable data device returned %q with no error", got)
	}
	if !nerr.HasCode(lookupErr, nerr.IO) {
		t.Fatalf("Lookup error = %v, want an IO failure", lookupErr)
	}
}

// A write that consumed part of its buffer leaves a torn record no later
// write can repair in place: the log must latch and the engine must refuse to
// acknowledge anything after it, including work that would otherwise succeed.
func TestShortWALWriteLatchesAndPreFaultRowsSurvive(t *testing.T) {
	path, keys, e, tr := newDB(t, 8, "a", "b")
	if err := e.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	torn := true
	installFault(t, func(f diskio.Fault) error {
		if torn && f.Op == "write" && strings.Contains(f.Path, ".wal") {
			return &diskio.ShortWrite{Consumed: 64, Err: errDevice}
		}
		return nil
	})

	if err := tr.Insert([]byte("torn"), []byte("v-torn")); err == nil {
		t.Fatal("insert acknowledged despite a partial WAL write")
	}

	// The device stops reporting the error, the way a Linux writeback error is
	// cleared once delivered. The latched log must still refuse.
	torn = false
	if err := tr.Insert([]byte("after"), []byte("v-after")); err == nil {
		t.Fatal("insert acknowledged after a latched WAL durability failure")
	}

	e.Kill()

	e2, tr2 := reopen(t, path, keys, 8)
	defer e2.Close()
	mustHave(t, tr2, "a")
	mustHave(t, tr2, "b")
	for _, k := range []string{"torn", "after"} {
		if got, err := tr2.Lookup([]byte(k)); err == nil && got != nil {
			t.Fatalf("row %q survived a WAL write that was never durable: %q", k, got)
		}
	}
}

// ENOSPC that consumed nothing is retryable: the buffer is intact at an
// unchanged offset, so an operator who frees space must get a working engine.
func TestDiskFullIsRejectedThenRecovers(t *testing.T) {
	path, keys, e, tr := newDB(t, 8, "a")

	full := true
	installFault(t, func(f diskio.Fault) error {
		if full && f.Op == "write" && strings.Contains(f.Path, ".wal") {
			return errDevice
		}
		return nil
	})

	if err := tr.Insert([]byte("full"), []byte("v-full")); err == nil {
		t.Fatal("insert acknowledged on a full device")
	}

	full = false
	if err := tr.Insert([]byte("after"), []byte("v-after")); err != nil {
		t.Fatalf("insert after the device recovered = %v, want progress", err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("close after a cleared fault: %v", err)
	}

	e2, tr2 := reopen(t, path, keys, 8)
	defer e2.Close()
	mustHave(t, tr2, "a")
	mustHave(t, tr2, "after")
	if got, err := tr2.Lookup([]byte("full")); err == nil && got != nil {
		t.Fatalf("row rejected on a full device came back: %q", got)
	}
}

// Creating a database whose directory entry could not be made durable must
// not leave a file behind that blocks every later attempt.
func TestCreateUnderDirectorySyncFaultLeavesNoBlockingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)

	failing := true
	installFault(t, func(f diskio.Fault) error {
		if failing && f.Op == "syncdir" && f.Path == dir {
			return errDevice
		}
		return nil
	})

	if e, err := storage.Create(path, keys, 8); err == nil {
		e.Kill()
		t.Fatal("Create reported success although the directory entry was never durable")
	}

	failing = false
	e, err := storage.Create(path, keys, 8)
	if err != nil {
		t.Fatalf("Create after the fault cleared = %v, want a usable database", err)
	}
	defer e.Close()
	if _, err := btree.Create(e); err != nil {
		t.Fatal(err)
	}
}

func TestMain(m *testing.M) { os.Exit(m.Run()) }

// generationsInFile returns the AES-GCM nonce generation of every sealed page
// in the data file. A generation that repeats under one key is a nonce reuse,
// which breaks the confidentiality and integrity of both pages sealed with it.
func generationsInFile(t *testing.T, path string) map[uint64]int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gens := map[uint64]int{}
	// Page 0 is the superblock, which is not sealed.
	for off := int64(format.PhysicalPageSize); off+format.PhysicalPageSize <= int64(len(raw)); off += format.PhysicalPageSize {
		page := raw[off : off+format.PhysicalPageSize]
		if allZero(page[:32]) {
			continue
		}
		// The envelope header holds the nonce at [16:28]: generation first.
		gens[binary.LittleEndian.Uint64(page[16:24])]++
	}
	return gens
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

func maxKey(m map[uint64]int) uint64 {
	var max uint64
	for k := range m {
		if k > max {
			max = k
		}
	}
	return max
}

// Opening a database reserves a fresh nonce batch by advancing the durable
// high-water in the superblock first. If that write fails, the open must fail:
// handing out generations the file does not record as consumed would let a
// later open issue them a second time, reusing an AES-GCM nonce.
func TestFailedNonceReservationFailsOpenAndNeverReusesAGeneration(t *testing.T) {
	path, keys, e, _ := newDB(t, 8, "a", "b", "c")
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	before := generationsInFile(t, path)
	if len(before) == 0 {
		t.Fatal("no sealed pages were written")
	}

	failing := true
	installFault(t, func(f diskio.Fault) error {
		// Offset 0 is the superblock: the durable nonce high-water.
		if failing && f.Op == "write" && f.Path == path && f.Off == 0 {
			return errDevice
		}
		return nil
	})

	if opened, err := storage.Open(path, keys, 8); err == nil {
		opened.Kill()
		t.Fatal("Open succeeded although the nonce high-water could not be made durable")
	}

	failing = false
	e2, tr2 := reopen(t, path, keys, 8)
	for _, k := range []string{"d", "e", "f"} {
		if err := tr2.Insert([]byte(k), []byte("v-"+k)); err != nil {
			t.Fatal(err)
		}
	}
	if err := e2.Close(); err != nil {
		t.Fatal(err)
	}

	after := generationsInFile(t, path)
	for gen, n := range after {
		if n > 1 {
			t.Fatalf("generation %d seals %d live pages: an AES-GCM nonce was reused", gen, n)
		}
	}
	if maxKey(after) <= maxKey(before) {
		t.Fatalf("generations after the failed reservation (max %d) did not advance past the pre-fault high-water (max %d)",
			maxKey(after), maxKey(before))
	}
}

// A backup destination that fills up must leave nothing a restore could pick
// up, and must not block the retry that follows once space is available.
func TestBackupOnAFullDestinationLeavesNothingRestorable(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, config.DataFileName)
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 8)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"a", "b", "c"} {
		if err := tr.Insert([]byte(k), []byte("v-"+k)); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	backupRoot := t.TempDir()
	dest := filepath.Join(backupRoot, "backup-1")

	full := true
	installFault(t, func(f diskio.Fault) error {
		if full && f.Op == "write" && strings.HasPrefix(f.Path, backupRoot) {
			return errDevice
		}
		return nil
	})

	if _, err := backup.Create(dataDir, dest, keys, backup.Options{}); err == nil {
		t.Fatal("backup reported success on a full destination")
	}
	infos, err := backup.ListBackups(backupRoot)
	if err != nil {
		t.Fatalf("ListBackups after a failed backup: %v", err)
	}
	if len(infos) != 0 {
		t.Fatalf("a failed backup is offered for restore: %+v", infos)
	}

	full = false
	if _, err := backup.Create(dataDir, dest, keys, backup.Options{}); err != nil {
		t.Fatalf("backup retry after space was freed = %v, want success", err)
	}
	infos, err = backup.ListBackups(backupRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("ListBackups = %d backups, want the one that succeeded", len(infos))
	}
	if err := backup.Verify(dest, keys, true); err != nil {
		t.Fatalf("the published backup does not verify: %v", err)
	}
}

// A restore interrupted by a failing destination device must leave no data
// directory behind: a half-written one that a server could be pointed at is
// worse than no restore at all.
func TestRestoreOnAFailingDeviceLeavesNoDataDirectory(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, config.DataFileName)
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 8)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"a", "b", "c"} {
		if err := tr.Insert([]byte(k), []byte("v-"+k)); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(t.TempDir(), "backup-1")
	if _, err := backup.Create(dataDir, src, keys, backup.Options{}); err != nil {
		t.Fatal(err)
	}

	restoreRoot := t.TempDir()
	destDir := filepath.Join(restoreRoot, "restored")

	failing := true
	installFault(t, func(f diskio.Fault) error {
		if failing && f.Op == "write" && strings.HasPrefix(f.Path, restoreRoot) {
			return errDevice
		}
		return nil
	})

	if _, err := backup.Restore(src, destDir, keys, backup.RestoreOptions{}); err == nil {
		t.Fatal("restore reported success on a failing device")
	}
	if _, err := os.Stat(destDir); !os.IsNotExist(err) {
		t.Fatalf("a failed restore left %s behind (stat err = %v)", destDir, err)
	}

	failing = false
	res, err := backup.Restore(src, destDir, keys, backup.RestoreOptions{})
	if err != nil {
		t.Fatalf("restore retry after the device recovered = %v, want success", err)
	}
	e2, tr2 := reopen(t, filepath.Join(res.DataDir, config.DataFileName), keys, 8)
	defer e2.Close()
	for _, k := range []string{"a", "b", "c"} {
		mustHave(t, tr2, k)
	}
}

// Redo cannot be skipped because the log could not be read. An open that
// swallowed the read error would come up missing every committed row the
// unreadable segment carried, and the next checkpoint would make that
// permanent.
func TestUnreadableWALFailsRecoveryInsteadOfDroppingRedo(t *testing.T) {
	path, keys, e, tr := newDB(t, 8, "a", "b")
	if err := e.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	// Committed after the checkpoint, so only redo can restore them.
	for _, k := range []string{"c", "d"} {
		if err := tr.Insert([]byte(k), []byte("v-"+k)); err != nil {
			t.Fatal(err)
		}
	}
	e.Kill()

	wdir := path + ".wal"
	failing := true
	installFault(t, func(f diskio.Fault) error {
		if failing && f.Op == "read" && strings.HasPrefix(f.Path, wdir) {
			return errDevice
		}
		return nil
	})

	if opened, err := storage.Open(path, keys, 8); err == nil {
		opened.Kill()
		t.Fatal("Open succeeded although the log needed for redo was unreadable")
	}

	failing = false
	e2, tr2 := reopen(t, path, keys, 8)
	defer e2.Close()
	for _, k := range []string{"a", "b", "c", "d"} {
		mustHave(t, tr2, k)
	}
}

// The undo log's control file is written before a database is usable. If its
// durability barrier fails, the create must fail — and must not leave the data
// file behind, or every later create at this path would report AlreadyExists.
func TestUndoBarrierFaultFailsCreateAndUnwindsIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)

	failing := true
	installFault(t, func(f diskio.Fault) error {
		if failing && f.Op == "sync" && strings.HasPrefix(f.Path, path+".undo") {
			return errDevice
		}
		return nil
	})

	if e, err := storage.Create(path, keys, 8); err == nil {
		e.Kill()
		t.Fatal("Create succeeded although the undo log's barrier failed")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the failed create left %s behind (stat err = %v)", path, err)
	}

	failing = false
	e, err := storage.Create(path, keys, 8)
	if err != nil {
		t.Fatalf("Create after the fault cleared = %v, want a usable database", err)
	}
	defer e.Close()
	if _, err := btree.Create(e); err != nil {
		t.Fatal(err)
	}
}

// The data file's own fsync is the barrier that makes flushed pages durable.
// A checkpoint whose fsync failed must not record a new redo boundary: the WAL
// is still the only durable copy of those pages.
func TestCheckpointFailsWhenDataSyncFails(t *testing.T) {
	path, keys, e, _ := newDB(t, 8, "a", "b", "c")
	before := e.File.Superblock()

	failing := true
	installFault(t, func(f diskio.Fault) error {
		if failing && f.Op == "sync" && f.Path == path {
			return errDevice
		}
		return nil
	})

	if err := e.Checkpoint(); !nerr.HasCode(err, nerr.IO) {
		t.Fatalf("Checkpoint with a failing data fsync = %v, want an IO failure", err)
	}
	if after := e.File.Superblock(); after.RedoLSN != before.RedoLSN || after.CheckpointLSN != before.CheckpointLSN {
		t.Fatalf("checkpoint boundary advanced across a failed fsync: %+v -> %+v", before, after)
	}

	failing = false
	e.Kill()
	e2, tr2 := reopen(t, path, keys, 8)
	defer e2.Close()
	for _, k := range []string{"a", "b", "c"} {
		mustHave(t, tr2, k)
	}
}
