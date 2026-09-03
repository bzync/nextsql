package recovery

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/storage/file"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/wal"
)

func TestRedoEmptyWAL(t *testing.T) {
	dir := t.TempDir()
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	id, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "nextsql.db")
	fm, err := file.Create(db, id, keys)
	if err != nil {
		t.Fatal(err)
	}
	defer fm.Close()
	lg, err := wal.Create(filepath.Join(dir, "wal"), keys, id, wal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	if err := Redo(fm, lg); err != nil {
		t.Fatal(err)
	}
}

func TestRedoUntilClipsLaterRecords(t *testing.T) {
	dir := t.TempDir()
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	id, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "nextsql.db")
	fm, err := file.Create(db, id, keys)
	if err != nil {
		t.Fatal(err)
	}
	defer fm.Close()
	lg, err := wal.Create(filepath.Join(dir, "wal"), keys, id, wal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	txn := lg.AllocTxn()
	if _, err := lg.Append(wal.BeginRec(txn)); err != nil {
		t.Fatal(err)
	}
	first, err := lg.Append(wal.CommitRec(txn, 0))
	if err != nil {
		t.Fatal(err)
	}
	txn2 := lg.AllocTxn()
	if _, err := lg.Append(wal.BeginRec(txn2)); err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(wal.CommitRec(txn2, 0)); err != nil {
		t.Fatal(err)
	}
	if err := RedoUntil(fm, lg, first); err != nil {
		t.Fatal(err)
	}
	open, err := UncommittedUntil(lg, first)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range open {
		if id == txn {
			t.Fatal("committed-before-until txn must not be uncommitted")
		}
	}
}

// TestRedoTreatsHeldUnflushedCommitAsUncommitted proves the structural fix
// for TODO.md's Phase 27 exit gate ("Local commit precedes replication
// acknowledgment") needs no change to this package: a transaction whose
// CommitRec was appended via wal.Log.AppendHeld but never flushed (the
// state Engine.commitAndReplicate leaves a transaction in while its
// replication outcome is still pending) is never durable on disk by
// construction — a crash at that point, simulated here with CrashClose
// plus a fresh Open exactly like a real process restart, must recover the
// transaction as an ordinary open/never-committed one, indistinguishable
// from any other in-flight write that hadn't reached COMMIT yet.
func TestRedoTreatsHeldUnflushedCommitAsUncommitted(t *testing.T) {
	dir := t.TempDir()
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	id, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "nextsql.db")
	fm, err := file.Create(db, id, keys)
	if err != nil {
		t.Fatal(err)
	}
	defer fm.Close()
	walDir := filepath.Join(dir, "wal")
	lg, err := wal.Create(walDir, keys, id, wal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	txn := lg.AllocTxn()
	beginLSN, err := lg.Append(wal.BeginRec(txn))
	if err != nil {
		t.Fatal(err)
	}
	// Durably flush the Begin record — this is the part of a commit that
	// is already, unremarkably, safe to be durable without a following
	// commit (see the structural-fix design doc: only the CommitRec's own
	// durability needs gating on replication).
	if err := lg.Flush(beginLSN); err != nil {
		t.Fatal(err)
	}
	// Now append the CommitRec via AppendHeld and simulate a crash right
	// there — exactly the window between Engine.prepareCommitLocked
	// appending it and Replicate resolving. It was never flushed, so
	// CrashClose (truncate to the last synced offset, close) needs no
	// wal.Injector crash point at this package's level to prove it never
	// reached disk.
	if _, err := lg.AppendHeld(wal.CommitRec(txn, beginLSN)); err != nil {
		t.Fatal(err)
	}
	lg.CrashClose()

	lg2, err := wal.Open(walDir, keys, id, wal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer lg2.Close()
	if err := Redo(fm, lg2); err != nil {
		t.Fatal(err)
	}
	open, err := Uncommitted(lg2)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range open {
		if id == txn {
			found = true
		}
	}
	if !found {
		t.Fatalf("txn %d with a held-but-never-flushed CommitRec must recover as open/uncommitted, got open=%v", txn, open)
	}
}
