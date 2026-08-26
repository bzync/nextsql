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
