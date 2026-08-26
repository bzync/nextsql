package recovery

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/file"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/page"
	"github.com/bzync/nextsql/internal/wal"
)

func TestRepairPageAppliesCommittedImage(t *testing.T) {
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
	fm, err := file.Create(filepath.Join(dir, "nextsql.db"), id, keys)
	if err != nil {
		t.Fatal(err)
	}
	defer fm.Close()
	lg, err := wal.Create(filepath.Join(dir, "wal"), keys, id, wal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	pid := format.FirstAllocPageID
	pg := page.New(pid, format.PageTypeSlotted)
	if _, err := pg.Insert([]byte("from-wal")); err != nil {
		t.Fatal(err)
	}
	pg.Finalize()
	txn := lg.AllocTxn()
	begin, err := lg.Append(wal.BeginRec(txn))
	if err != nil {
		t.Fatal(err)
	}
	lsn, _, err := lg.AppendPageImage(txn, begin, pid, pg.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	commit, err := lg.Append(wal.CommitRec(txn, lsn))
	if err != nil {
		t.Fatal(err)
	}
	if err := lg.Flush(commit); err != nil {
		t.Fatal(err)
	}

	// No durable page on disk: repair must install the WAL image.
	got, err := RepairPage(fm, lg, pid)
	if err != nil {
		t.Fatal(err)
	}
	p, err := page.ParseID(got, pid)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := p.Get(0)
	if err != nil {
		t.Fatal(err)
	}
	if string(rec) != "from-wal" {
		t.Fatalf("got %q", rec)
	}
	disk, err := fm.ReadLogical(pid)
	if err != nil {
		t.Fatal(err)
	}
	if page.LSNOf(disk) == 0 {
		t.Fatal("repaired page missing LSN")
	}
}

func TestRepairPageIgnoresUncommitted(t *testing.T) {
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
	fm, err := file.Create(filepath.Join(dir, "nextsql.db"), id, keys)
	if err != nil {
		t.Fatal(err)
	}
	defer fm.Close()
	lg, err := wal.Create(filepath.Join(dir, "wal"), keys, id, wal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	pid := format.FirstAllocPageID
	pg := page.New(pid, format.PageTypeSlotted)
	pg.Finalize()
	txn := lg.AllocTxn()
	begin, err := lg.Append(wal.BeginRec(txn))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := lg.AppendPageImage(txn, begin, pid, pg.Bytes()); err != nil {
		t.Fatal(err)
	}
	if _, err := RepairPage(fm, lg, pid); !nerr.HasCode(err, nerr.Corruption) {
		t.Fatalf("uncommitted image must not repair: %v", err)
	}
}
