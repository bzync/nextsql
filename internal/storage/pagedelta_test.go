package storage_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/storage/btree"
	"github.com/bzync/nextsql/internal/storage/checksum"
	"github.com/bzync/nextsql/internal/txn"
	"github.com/bzync/nextsql/internal/wal"
)

func deltaKeys(t *testing.T) *crypto.MemoryKeyProvider {
	t.Helper()
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	k, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func commitKeys(t *testing.T, tr *btree.Tree, from, to int) {
	t.Helper()
	for i := from; i < to; i++ {
		tx, err := tr.BeginTxn(txn.SnapshotIsolation)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Insert([]byte(fmt.Sprintf("key-%06d", i)), []byte(fmt.Sprintf("value-%d", i))); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}

func countRecords(t *testing.T, lg *wal.Log) (images, deltas int, imageBytes, deltaBytes int) {
	t.Helper()
	recs, _, err := lg.ScanFrom(1)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		switch r.Type {
		case wal.RecPageImage:
			images++
			imageBytes += len(r.Body)
		case wal.RecPageDelta:
			deltas++
			deltaBytes += len(r.Body)
		}
	}
	return
}

// A new database logs single-row commits as page deltas, and recovery rebuilds
// every committed key from them after power loss.
func TestPageDeltasAreWrittenAndRecovered(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := deltaKeys(t)
	e, err := storage.Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	if !e.WAL.PageDeltas() {
		t.Fatal("a newly created database does not have page deltas enabled")
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	commitKeys(t, tr, 0, 2000)
	images, deltas, imageBytes, deltaBytes := countRecords(t, e.WAL)
	t.Logf("%d full images (%d bytes), %d deltas (%d bytes, %.0f bytes each)", images, imageBytes, deltas, deltaBytes, float64(deltaBytes)/float64(max(1, deltas)))
	if deltas < 1500 {
		t.Fatalf("only %d page deltas for 2000 single-key commits (%d full images)", deltas, images)
	}
	if avg := deltaBytes / deltas; avg > 2048 {
		t.Fatalf("average delta is %d bytes", avg)
	}
	e.Kill()

	e2, err := storage.Open(path, keys, 64)
	if err != nil {
		t.Fatalf("recover from page deltas: %v", err)
	}
	defer e2.Close()
	tr2, err := btree.Open(e2)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2000; i++ {
		if _, err := tr2.Lookup([]byte(fmt.Sprintf("key-%06d", i))); err != nil {
			t.Fatalf("committed key %d missing after recovery: %v", i, err)
		}
	}
}

// Deltas survive checkpoints interleaved with the commits (each checkpoint
// raises the base floor), repeated crashes, and more commits after recovery.
func TestPageDeltasAcrossCheckpointsAndRepeatedCrashes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := deltaKeys(t)
	e, err := storage.Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	next := 0
	for round := 0; round < 4; round++ {
		for batch := 0; batch < 3; batch++ {
			commitKeys(t, tr, next, next+300)
			next += 300
			if err := e.Checkpoint(); err != nil {
				t.Fatal(err)
			}
		}
		commitKeys(t, tr, next, next+150) // after the last checkpoint
		next += 150
		e.Kill()
		e, err = storage.Open(path, keys, 32)
		if err != nil {
			t.Fatalf("round %d reopen: %v", round, err)
		}
		tr, err = btree.Open(e)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < next; i++ {
			if _, err := tr.Lookup([]byte(fmt.Sprintf("key-%06d", i))); err != nil {
				t.Fatalf("round %d: committed key %d missing: %v", round, i, err)
			}
		}
	}
	_ = e.Close()
}

// An existing database whose log predates page deltas keeps writing full
// images under the default mode, so the release that created it can still
// open it; PageDeltasOn upgrades it.
func TestPageDeltaModesOnAnExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := deltaKeys(t)
	e, err := storage.CreateWith(path, keys, 64, storage.OpenOptions{PageDeltas: storage.PageDeltasOff})
	if err != nil {
		t.Fatal(err)
	}
	if e.WAL.PageDeltas() {
		t.Fatal("PageDeltasOff created a delta-capable log")
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	commitKeys(t, tr, 0, 100)
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	e, err = storage.Open(path, keys, 64) // default: auto
	if err != nil {
		t.Fatal(err)
	}
	if e.WAL.PageDeltas() {
		t.Fatal("opening with the default mode upgraded an existing log")
	}
	tr, err = btree.Open(e)
	if err != nil {
		t.Fatal(err)
	}
	commitKeys(t, tr, 100, 200)
	if _, deltas, _, _ := countRecords(t, e.WAL); deltas != 0 {
		t.Fatalf("an existing full-image log received %d page deltas", deltas)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	e, err = storage.OpenWith(path, keys, 64, storage.OpenOptions{PageDeltas: storage.PageDeltasOn})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if !e.WAL.PageDeltas() {
		t.Fatal("PageDeltasOn did not upgrade the log")
	}
	tr, err = btree.Open(e)
	if err != nil {
		t.Fatal(err)
	}
	commitKeys(t, tr, 200, 400)
	if _, deltas, _, _ := countRecords(t, e.WAL); deltas == 0 {
		t.Fatal("an upgraded log wrote no page deltas")
	}
}

// A log holding page deltas behind a version-1 control file -- what a
// point-in-time restore produces from a base backup taken before the source
// moved to version 2 -- is moved to version 2 by recovery before it replays
// anything, so the gate a pre-delta release checks is true again.
func TestRecoveryRaisesControlVersionForDeltasItFinds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := deltaKeys(t)
	e, err := storage.Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	commitKeys(t, tr, 0, 200)
	if _, deltas, _, _ := countRecords(t, e.WAL); deltas == 0 {
		t.Fatal("no page deltas were written")
	}
	e.Kill()

	ctrlPath := filepath.Join(wal.DirFor(path), "control")
	raw, err := os.ReadFile(ctrlPath)
	if err != nil {
		t.Fatal(err)
	}
	encoding.PutU16(raw, 4, 1)
	checksum.Write(raw[:104], 100)
	if err := os.WriteFile(ctrlPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	e2, err := storage.Open(path, keys, 64)
	if err != nil {
		t.Fatalf("recover a version-1 log holding deltas: %v", err)
	}
	defer e2.Close()
	if !e2.WAL.PageDeltas() {
		t.Fatal("recovery replayed page deltas and left the control file at version 1")
	}
	raw, err = os.ReadFile(ctrlPath)
	if err != nil {
		t.Fatal(err)
	}
	if v := encoding.U16(raw, 4); v != 2 {
		t.Fatalf("control version on disk after recovery = %d, want 2", v)
	}
	tr2, err := btree.Open(e2)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if _, err := tr2.Lookup([]byte(fmt.Sprintf("key-%06d", i))); err != nil {
			t.Fatalf("committed key %d missing after recovery: %v", i, err)
		}
	}
}
