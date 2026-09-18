package wal

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/storage/format"
)

// fillSegments appends committed transactions until the log has rotated at
// least want segments, and returns the LSN of the last commit.
func fillSegments(t *testing.T, lg *Log, want int) format.LSN {
	t.Helper()
	body := make([]byte, 4<<10)
	var last format.LSN
	for i := 0; i < 20000; i++ {
		txn := lg.AllocTxn()
		if _, err := lg.Append(BeginRec(txn)); err != nil {
			t.Fatal(err)
		}
		if _, err := lg.Append(Record{Type: RecPageImage, TxnID: txn, PageID: format.PageID(i), Body: body}); err != nil {
			t.Fatal(err)
		}
		lsn, err := lg.Append(CommitRec(txn, 1))
		if err != nil {
			t.Fatal(err)
		}
		last = lsn
		if err := lg.Flush(lsn); err != nil {
			t.Fatal(err)
		}
		ids, err := listSegments(lg.dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) >= want {
			return last
		}
	}
	t.Fatalf("log did not rotate to %d segments", want)
	return last
}

func segmentCount(t *testing.T, lg *Log) int {
	t.Helper()
	ids, err := listSegments(lg.dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(ids)
}

func newFilledLog(t *testing.T, segments int) *Log {
	t.Helper()
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	lg, err := Create(dir, keys, id, Options{SegmentSize: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lg.Close() })
	fillSegments(t, lg, segments)
	return lg
}

func TestTrimToCapReclaimsCheckpointedSegments(t *testing.T) {
	lg := newFilledLog(t, 8)
	before := segmentCount(t, lg)
	// Everything written so far is replayed, so only the active segment is
	// still required.
	if err := lg.InstallCheckpoint(lg.NextLSN(), lg.NextLSN()); err != nil {
		t.Fatal(err)
	}
	total, segs, err := lg.OnDiskFootprint()
	if err != nil {
		t.Fatal(err)
	}
	if segs != before || total <= 0 {
		t.Fatalf("footprint = %d bytes over %d segments, want %d segments", total, segs, before)
	}

	// A cap of roughly two segments must bring the directory down to it.
	capBytes := total / int64(before) * 2
	removed, retained, err := lg.TrimToCap(capBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if removed == 0 {
		t.Fatal("cap reclaimed nothing")
	}
	if retained > capBytes {
		t.Fatalf("retained %d bytes above the %d cap", retained, capBytes)
	}
	after := segmentCount(t, lg)
	if after != before-removed {
		t.Fatalf("segment count %d, expected %d - %d", after, before, removed)
	}
	gotBytes, gotSegs, err := lg.OnDiskFootprint()
	if err != nil {
		t.Fatal(err)
	}
	if gotSegs != after || gotBytes != retained {
		t.Fatalf("footprint disagrees with the trim: %d bytes/%d segments vs %d/%d", gotBytes, gotSegs, retained, after)
	}

	// A second pass has nothing left to do.
	removed2, _, err := lg.TrimToCap(capBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if removed2 != 0 {
		t.Fatalf("second trim removed %d more segments", removed2)
	}
}

// The cap must never win against recoverability: with the redo boundary still
// pointing at the first segment, nothing is removable however small the cap.
func TestTrimToCapNeverRemovesSegmentsRecoveryNeeds(t *testing.T) {
	lg := newFilledLog(t, 6)
	before := segmentCount(t, lg)
	if err := lg.InstallCheckpoint(lg.NextLSN(), 1); err != nil {
		t.Fatal(err)
	}
	removed, retained, err := lg.TrimToCap(64<<10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("removed %d segments still required for recovery", removed)
	}
	if segmentCount(t, lg) != before {
		t.Fatal("segment count changed")
	}
	// Exceeding the cap is the correct outcome, and the reported figure says so.
	if retained <= 64<<10 {
		t.Fatalf("retained %d bytes; the test intends the cap to be exceeded", retained)
	}
}

// A CDC subscriber's retention pin holds segments the redo boundary alone
// would release.
func TestTrimToCapRespectsCDCRetentionPins(t *testing.T) {
	lg := newFilledLog(t, 6)
	before := segmentCount(t, lg)
	release, err := lg.PinRetention(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := lg.InstallCheckpoint(lg.NextLSN(), lg.NextLSN()); err != nil {
		t.Fatal(err)
	}
	removed, _, err := lg.TrimToCap(64<<10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("removed %d segments pinned by a subscriber", removed)
	}
	if segmentCount(t, lg) != before {
		t.Fatal("segment count changed while pinned")
	}

	// Releasing the pin makes the same call reclaim.
	release()
	removed, _, err = lg.TrimToCap(64<<10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if removed == 0 {
		t.Fatal("releasing the pin did not make any segment reclaimable")
	}
}

// Where an archiver is configured the local segments may be the only copy
// until it has taken them, so a size cap must refuse rather than guess.
func TestTrimToCapRefusesWhenArchiverIsConfigured(t *testing.T) {
	lg := newFilledLog(t, 4)
	if err := lg.InstallCheckpoint(lg.NextLSN(), lg.NextLSN()); err != nil {
		t.Fatal(err)
	}
	before := segmentCount(t, lg)
	lg.SetArchiver(archiverFunc(func(path string, first, last format.LSN) error { return nil }))
	if _, _, err := lg.TrimToCap(1<<10, nil); err == nil {
		t.Fatal("a size cap must be refused alongside an archiver")
	}
	if segmentCount(t, lg) != before {
		t.Fatal("refused trim removed segments")
	}
}

func TestTrimToCapIsInertWithoutCheckpointOrCap(t *testing.T) {
	lg := newFilledLog(t, 4)
	before := segmentCount(t, lg)
	// No checkpoint installed yet: nothing is known to be replayed.
	removed, _, err := lg.TrimToCap(1<<10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 || segmentCount(t, lg) != before {
		t.Fatalf("trimmed %d segments before any checkpoint", removed)
	}

	if err := lg.InstallCheckpoint(lg.NextLSN(), lg.NextLSN()); err != nil {
		t.Fatal(err)
	}
	// A zero or negative cap is "retain everything", the default.
	for _, capBytes := range []int64{0, -1} {
		removed, _, err := lg.TrimToCap(capBytes, nil)
		if err != nil {
			t.Fatal(err)
		}
		if removed != 0 || segmentCount(t, lg) != before {
			t.Fatalf("cap %d trimmed %d segments", capBytes, removed)
		}
	}
}

// After a trim the log must still open and replay from the retained segments.
func TestTrimToCapLeavesTheLogRecoverable(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	lg, err := Create(dir, keys, id, Options{SegmentSize: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	fillSegments(t, lg, 6)
	if err := lg.InstallCheckpoint(lg.NextLSN(), lg.NextLSN()); err != nil {
		t.Fatal(err)
	}
	redo := lg.RedoLSN()

	txn := lg.AllocTxn()
	if _, err := lg.Append(BeginRec(txn)); err != nil {
		t.Fatal(err)
	}
	marker, err := lg.Append(CommitRec(txn, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := lg.Flush(marker); err != nil {
		t.Fatal(err)
	}
	removed, _, err := lg.TrimToCap(96<<10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if removed == 0 {
		t.Fatal("nothing was trimmed; the test needs a trim to have happened")
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir, keys, id, Options{SegmentSize: 64 << 10})
	if err != nil {
		t.Fatalf("log does not open after a trim: %v", err)
	}
	defer reopened.Close()
	if reopened.RedoLSN() != redo {
		t.Fatalf("redo boundary moved across the trim: %d -> %d", redo, reopened.RedoLSN())
	}
	if reopened.NextLSN() <= marker {
		t.Fatalf("next LSN %d did not resume past the last commit %d", reopened.NextLSN(), marker)
	}
	recs, _, err := reopened.ScanFrom(redo)
	if err != nil {
		t.Fatalf("scan from the redo boundary failed after a trim: %v", err)
	}
	seen := false
	for _, rec := range recs {
		if rec.Type == RecCommit && rec.TxnID == txn {
			seen = true
		}
	}
	if !seen {
		t.Fatal("the commit written after the checkpoint was not replayable")
	}
}

type archiverFunc func(path string, first, last format.LSN) error

func (f archiverFunc) Archive(path string, first, last format.LSN) error { return f(path, first, last) }
