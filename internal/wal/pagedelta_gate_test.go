package wal

import (
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

// A page delta may only reach a log whose control file says it can hold one,
// so a release that predates deltas refuses the whole log rather than opening
// it and failing on a record it does not know.
func TestPageDeltaNeedsControlVersion2(t *testing.T) {
	keys, id := testIdent(t)
	rng := rand.New(rand.NewSource(1))
	base := testPage(rng, 1)
	cur := append([]byte(nil), base...)
	cur[100] ^= 0xff
	body, ok := EncodePageDelta(1, base, cur, len(cur)/2)
	if !ok {
		t.Fatal("delta did not encode")
	}

	// A local append on a v1 log fails closed.
	dir := filepath.Join(t.TempDir(), "wal")
	lg, err := Create(dir, keys, id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if lg.PageDeltas() {
		t.Fatal("a log created without PageDeltas is at version 2")
	}
	txn := lg.AllocTxn()
	if _, err := lg.Append(PageDeltaRec(txn, 0, 7, body)); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("delta appended to a v1 log: %v", err)
	}

	// A replica installing a leader's delta moves its own log to version 2
	// first, durably.
	next := lg.NextLSN()
	recs := []Record{BeginRec(txn), PageDeltaRec(txn, 0, 7, body), CommitRec(txn, 0)}
	for i := range recs {
		recs[i].LSN = next + format.LSN(i)
	}
	if err := lg.InstallRecords(recs); err != nil {
		t.Fatal(err)
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
	ctrl, err := readControl(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ctrl.Version != controlVersionPageDeltas {
		t.Fatalf("control version after installing a delta = %d, want %d", ctrl.Version, controlVersionPageDeltas)
	}

	// Records without a delta leave a v1 log alone.
	dir2 := filepath.Join(t.TempDir(), "wal")
	lg2, err := Create(dir2, keys, id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	txn2 := lg2.AllocTxn()
	next = lg2.NextLSN()
	plain := []Record{BeginRec(txn2), CommitRec(txn2, 0)}
	for i := range plain {
		plain[i].LSN = next + format.LSN(i)
	}
	if err := lg2.InstallRecords(plain); err != nil {
		t.Fatal(err)
	}
	if lg2.PageDeltas() {
		t.Fatal("installing records without a delta moved the log to version 2")
	}
	_ = lg2.Close()
}
