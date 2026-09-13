package recovery

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/encoding"
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

// repairFixture is a data file with no durable page and a WAL that permits
// page deltas.
func repairFixture(t *testing.T) (*file.Manager, *wal.Log) {
	t.Helper()
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
	t.Cleanup(func() { _ = fm.Close() })
	lg, err := wal.Create(filepath.Join(dir, "wal"), keys, id, wal.Options{PageDeltas: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lg.Close() })
	return fm, lg
}

func slottedPage(t *testing.T, pid format.PageID, recs ...string) []byte {
	t.Helper()
	pg := page.New(pid, format.PageTypeSlotted)
	for _, r := range recs {
		if _, err := pg.Insert([]byte(r)); err != nil {
			t.Fatal(err)
		}
	}
	pg.Finalize()
	return append([]byte(nil), pg.Bytes()...)
}

// commitImageThenDelta logs a committed full image of first, then a committed
// delta that turns base into second. base is normally the logged first.
func commitImageThenDelta(t *testing.T, lg *wal.Log, pid format.PageID, first, base, second []byte) {
	t.Helper()
	txn := lg.AllocTxn()
	begin, err := lg.Append(wal.BeginRec(txn))
	if err != nil {
		t.Fatal(err)
	}
	imgLSN, _, err := lg.AppendPageImage(txn, begin, pid, first)
	if err != nil {
		t.Fatal(err)
	}
	if base == nil {
		base = append([]byte(nil), first...)
	}
	encoding.PutU64(base, wal.PageLSNOffset, uint64(imgLSN))
	body, ok := wal.EncodePageDelta(imgLSN, base, second, format.LogicalPageSize/2)
	if !ok {
		t.Fatal("delta did not encode")
	}
	dLSN, err := lg.Append(wal.PageDeltaRec(txn, imgLSN, pid, body))
	if err != nil {
		t.Fatal(err)
	}
	commit, err := lg.Append(wal.CommitRec(txn, dLSN))
	if err != nil {
		t.Fatal(err)
	}
	if err := lg.Flush(commit); err != nil {
		t.Fatal(err)
	}
}

func TestRepairPageChainsDeltasOntoAnImage(t *testing.T) {
	fm, lg := repairFixture(t)
	pid := format.FirstAllocPageID
	commitImageThenDelta(t, lg, pid, slottedPage(t, pid, "first"), nil, slottedPage(t, pid, "first", "second"))

	got, err := RepairPage(fm, lg, pid)
	if err != nil {
		t.Fatal(err)
	}
	p, err := page.ParseID(got, pid)
	if err != nil {
		t.Fatal(err)
	}
	if rec, err := p.Get(1); err != nil || string(rec) != "second" {
		t.Fatalf("repaired page is missing the change the delta logged: %q %v", rec, err)
	}
}

// A delta that cannot be applied means the log holds a committed change the
// rebuilt page lacks. Repair must refuse rather than install the older state.
func TestRepairPageRefusesABrokenDeltaChain(t *testing.T) {
	fm, lg := repairFixture(t)
	pid := format.FirstAllocPageID
	other := slottedPage(t, pid, "not-what-was-logged")
	commitImageThenDelta(t, lg, pid, slottedPage(t, pid, "first"), other, slottedPage(t, pid, "first", "second"))

	if got, err := RepairPage(fm, lg, pid); !nerr.HasCode(err, nerr.Corruption) {
		t.Fatalf("repair installed a page older than a committed change (page LSN %d): %v", page.LSNOf(got), err)
	}
}
