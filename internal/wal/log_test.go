package wal

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	diskio "github.com/bzync/nextsql/internal/storage/io"
)

func testIdent(t *testing.T) (crypto.KeyProvider, format.Identity) {
	t.Helper()
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
	return keys, id
}

func TestLogAppendFlushScan(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	lg, err := Create(dir, keys, id, Options{SegmentSize: 256 << 10})
	if err != nil {
		t.Fatal(err)
	}
	txn := lg.AllocTxn()
	if _, err := lg.Append(BeginRec(txn)); err != nil {
		t.Fatal(err)
	}
	lsn, err := lg.Append(CommitRec(txn, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := lg.Flush(lsn); err != nil {
		t.Fatal(err)
	}
	if lg.DurableLSN() != lsn {
		t.Fatalf("durable %d want %d", lg.DurableLSN(), lsn)
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}

	lg, err = Open(dir, keys, id, Options{SegmentSize: 256 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	recs, last, err := lg.ScanFrom(1)
	if err != nil {
		t.Fatal(err)
	}
	if last < 2 || len(recs) < 2 {
		t.Fatalf("recs=%d last=%d", len(recs), last)
	}
	if recs[0].Type != RecBegin || recs[1].Type != RecCommit {
		t.Fatalf("types %s %s", recs[0].Type, recs[1].Type)
	}
}

func TestLogWrongPageKey(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	lg, err := Create(dir, keys, id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
	other, _ := testIdent(t)
	if _, err := Open(dir, other, id, Options{}); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("wrong key opened WAL: %v", err)
	}
}

func TestGroupCommit(t *testing.T) {
	keys, id := testIdent(t)
	lg, err := Create(filepath.Join(t.TempDir(), "wal"), keys, id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	const n = 8
	lsns := make([]format.LSN, n)
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		txn := lg.AllocTxn()
		lsn, err := lg.Append(BeginRec(txn))
		if err != nil {
			t.Fatal(err)
		}
		lsns[i] = lsn
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(lsn format.LSN) {
			defer wg.Done()
			errCh <- lg.Flush(lsn)
		}(lsns[i])
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if lg.DurableLSN() < lsns[n-1] {
		t.Fatalf("durable %d", lg.DurableLSN())
	}
}

func TestPartialTailTruncated(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	lg, err := Create(dir, keys, id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	txn := lg.AllocTxn()
	lsn, err := lg.Append(BeginRec(txn))
	if err != nil {
		t.Fatal(err)
	}
	if err := lg.Flush(lsn); err != nil {
		t.Fatal(err)
	}
	// Append a second record, write it, then crash before sync.
	if _, err := lg.Append(CommitRec(txn, lsn)); err != nil {
		t.Fatal(err)
	}
	lg.SetCrash(func() *Injector {
		inj := NewInjector()
		inj.Arm(PointAfterWALWriteBeforeSync)
		return inj
	}())
	if err := lg.Flush(lsn + 1); !IsCrash(err) {
		t.Fatalf("expected crash, got %v", err)
	}
	lg.CrashClose()

	lg, err = Open(dir, keys, id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	recs, last, err := lg.ScanFrom(1)
	if err != nil {
		t.Fatal(err)
	}
	if last != lsn || len(recs) != 1 {
		t.Fatalf("tail should drop unsynced commit: recs=%d last=%d", len(recs), last)
	}
}

func TestSegmentRotation(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	lg, err := Create(dir, keys, id, Options{SegmentSize: 8 << 10})
	if err != nil {
		t.Fatal(err)
	}
	var last format.LSN
	for i := 0; i < 40; i++ {
		txn := lg.AllocTxn()
		body := make([]byte, 200)
		rec := Record{Type: RecInsert, TxnID: txn, Body: body}
		lsn, err := lg.Append(BeginRec(txn))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := lg.Append(rec); err != nil {
			t.Fatal(err)
		}
		last, err = lg.Append(CommitRec(txn, lsn))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := lg.Flush(last); err != nil {
		t.Fatal(err)
	}
	ids, err := listSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) < 2 {
		t.Fatalf("expected rotation, segments=%v", ids)
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
	lg, err = Open(dir, keys, id, Options{SegmentSize: 8 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	recs, _, err := lg.ScanFrom(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) < 80 {
		t.Fatalf("scan after rotation recs=%d", len(recs))
	}
}

func TestScanFromSkipsSealedSegmentsBeforeStart(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	lg, err := Create(dir, keys, id, Options{SegmentSize: 8 << 10})
	if err != nil {
		t.Fatal(err)
	}
	var last format.LSN
	for i := 0; i < 80; i++ {
		txn := lg.AllocTxn()
		begin, err := lg.Append(BeginRec(txn))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := lg.Append(Record{Type: RecInsert, TxnID: txn, Body: make([]byte, 200)}); err != nil {
			t.Fatal(err)
		}
		last, err = lg.Append(CommitRec(txn, begin))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := lg.Flush(last); err != nil {
		t.Fatal(err)
	}
	ids, err := listSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) < 2 {
		t.Fatalf("expected rotation, segments=%v", ids)
	}
	lastSegment, header, _, err := openSegment(dir, ids[len(ids)-1], id)
	if err != nil {
		t.Fatal(err)
	}
	if err := lastSegment.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}

	// A prior sealed segment is retained for PITR/page repair, but is outside
	// this scan's redo interval. Corrupting it proves ScanFrom does not spend
	// recovery CPU decrypting records it cannot return; its successor header
	// still provides the authenticated identity/start-LSN boundary.
	oldPath := filepath.Join(dir, segmentName(ids[0]))
	f, err := os.OpenFile(oldPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xff}, SegmentHeaderSize+HeaderSize); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	lg, err = Open(dir, keys, id, Options{SegmentSize: 8 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	recs, _, err := lg.ScanFrom(header.StartLSN)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 || recs[0].LSN < header.StartLSN {
		t.Fatalf("ScanFrom(%d) returned %d records starting at %#v", header.StartLSN, len(recs), recs)
	}
}

type archiveSink struct {
	n           int
	first, last format.LSN
	err         error
}

func (a *archiveSink) Archive(path string, first, last format.LSN) error {
	a.n++
	a.first, a.last = first, last
	return a.err
}

func TestCrashDuringRotation(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	lg, err := Create(dir, keys, id, Options{SegmentSize: 2 << 10})
	if err != nil {
		t.Fatal(err)
	}
	inj := NewInjector()
	inj.Arm(PointBeforeRotation)
	lg.SetCrash(inj)
	var hit error
	for i := 0; i < 40; i++ {
		txn := lg.AllocTxn()
		if _, err := lg.Append(BeginRec(txn)); err != nil {
			hit = err
			break
		}
		if _, err := lg.Append(Record{Type: RecInsert, TxnID: txn, Body: make([]byte, 400)}); err != nil {
			hit = err
			break
		}
	}
	if !IsCrash(hit) {
		t.Fatalf("expected rotation crash, got %v", hit)
	}
	lg.CrashClose()
	lg, err = Open(dir, keys, id, Options{SegmentSize: 2 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	if _, _, err := lg.ScanFrom(1); err != nil {
		t.Fatal(err)
	}
}

func TestAppendIsNotDurableUntilFlush(t *testing.T) {
	keys, id := testIdent(t)
	lg, err := Create(filepath.Join(t.TempDir(), "wal"), keys, id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	txn := lg.AllocTxn()
	lsn, err := lg.Append(BeginRec(txn))
	if err != nil {
		t.Fatal(err)
	}
	if lg.DurableLSN() >= lsn {
		t.Fatal("append must not fsync")
	}
	if err := lg.Flush(lsn); err != nil {
		t.Fatal(err)
	}
	if lg.DurableLSN() < lsn {
		t.Fatal("flush must make LSN durable")
	}
}

func TestArchivalHook(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	sink := &archiveSink{}
	lg, err := Create(dir, keys, id, Options{SegmentSize: 8 << 10, Archiver: sink})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	var last format.LSN
	for i := 0; i < 30; i++ {
		txn := lg.AllocTxn()
		if _, err := lg.Append(BeginRec(txn)); err != nil {
			t.Fatal(err)
		}
		last, err = lg.Append(Record{Type: RecInsert, TxnID: txn, Body: make([]byte, 300)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := lg.Append(CommitRec(txn, last)); err != nil {
			t.Fatal(err)
		}
	}
	if err := lg.Flush(lg.NextLSN() - 1); err != nil {
		t.Fatal(err)
	}
	if err := lg.InstallCheckpoint(last, lg.NextLSN()); err != nil {
		t.Fatal(err)
	}
	if err := lg.Recycle(); err != nil {
		t.Fatal(err)
	}
	if sink.n == 0 {
		t.Fatal("archiver was not invoked")
	}
}

func TestDiscardCheckpointedSegments(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	lg, err := Create(dir, keys, id, Options{SegmentSize: 8 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	var last format.LSN
	for i := 0; i < 80; i++ {
		txn := lg.AllocTxn()
		if _, err := lg.Append(BeginRec(txn)); err != nil {
			t.Fatal(err)
		}
		last, err = lg.Append(Record{Type: RecInsert, TxnID: txn, Body: make([]byte, 400)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := lg.Append(CommitRec(txn, last)); err != nil {
			t.Fatal(err)
		}
	}
	if err := lg.Flush(lg.NextLSN() - 1); err != nil {
		t.Fatal(err)
	}
	before, err := listSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) < 2 {
		t.Fatalf("expected rotated WAL, segments=%v", before)
	}
	redo := lg.NextLSN()
	if err := lg.InstallCheckpoint(last, redo); err != nil {
		t.Fatal(err)
	}
	if err := lg.DiscardCheckpointedSegments(); err != nil {
		t.Fatal(err)
	}
	after, err := listSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 {
		t.Fatalf("segments after discard=%v, want active segment only", after)
	}
	if _, _, err := lg.ScanFrom(redo); err != nil {
		t.Fatalf("scan from checkpoint redo: %v", err)
	}
}

func TestDiscardCheckpointedSegmentsPreservesWALAfterCheckpoint(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	lg, err := Create(dir, keys, id, Options{SegmentSize: 8 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	appendTxn := func() format.LSN {
		t.Helper()
		txn := lg.AllocTxn()
		if _, err := lg.Append(BeginRec(txn)); err != nil {
			t.Fatal(err)
		}
		last, err := lg.Append(Record{Type: RecInsert, TxnID: txn, Body: make([]byte, 400)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := lg.Append(CommitRec(txn, last)); err != nil {
			t.Fatal(err)
		}
		return last
	}
	var checkpointLSN format.LSN
	for i := 0; i < 80; i++ {
		checkpointLSN = appendTxn()
	}
	if err := lg.Flush(lg.NextLSN() - 1); err != nil {
		t.Fatal(err)
	}
	redo := lg.NextLSN()
	if err := lg.InstallCheckpoint(checkpointLSN, redo); err != nil {
		t.Fatal(err)
	}
	// Rotate more than once after the checkpoint. A delayed discard must keep
	// every segment containing records at or after redo.
	for i := 0; i < 80; i++ {
		appendTxn()
	}
	if err := lg.Flush(lg.NextLSN() - 1); err != nil {
		t.Fatal(err)
	}
	before, err := listSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) < 3 {
		t.Fatalf("expected at least three segments, got %v", before)
	}
	if err := lg.DiscardCheckpointedSegments(); err != nil {
		t.Fatal(err)
	}
	recs, _, err := lg.ScanFrom(redo)
	if err != nil {
		t.Fatalf("scan from checkpoint redo: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("post-checkpoint WAL was discarded")
	}
}

func TestPruneArchivedBeforeRequiresAndPreservesHorizons(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	lg, err := Create(dir, keys, id, Options{SegmentSize: 8 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	var last format.LSN
	for i := 0; i < 100; i++ {
		txn := lg.AllocTxn()
		if _, err := lg.Append(BeginRec(txn)); err != nil {
			t.Fatal(err)
		}
		last, err = lg.Append(Record{Type: RecInsert, TxnID: txn, Body: make([]byte, 400)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := lg.Append(CommitRec(txn, last)); err != nil {
			t.Fatal(err)
		}
	}
	if err := lg.Flush(lg.NextLSN() - 1); err != nil {
		t.Fatal(err)
	}
	redo := lg.NextLSN()
	if err := lg.InstallCheckpoint(last, redo); err != nil {
		t.Fatal(err)
	}
	before, err := listSegments(dir)
	if err != nil || len(before) < 3 {
		t.Fatalf("segments before prune=%v err=%v", before, err)
	}
	if _, err := lg.PruneArchivedBefore(redo, nil); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("prune without archiver: %v", err)
	}
	sink := &archiveSink{}
	lg.SetArchiver(sink)
	firstFile, firstHdr, _, err := openSegment(dir, before[0], id)
	if err != nil {
		t.Fatal(err)
	}
	_ = firstFile.Close()
	release, err := lg.PinRetention(firstHdr.StartLSN)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := lg.PruneArchivedBefore(redo, nil); err != nil || n != 0 {
		t.Fatalf("CDC-pinned prune=%d err=%v", n, err)
	}
	if after, _ := listSegments(dir); len(after) != len(before) {
		t.Fatalf("CDC pin removed segments: before=%v after=%v", before, after)
	}
	release()
	release()
	n, err := lg.PruneArchivedBefore(redo, nil)
	if err != nil || n == 0 {
		t.Fatalf("archived prune=%d err=%v", n, err)
	}
	if sink.n != n || sink.first == 0 || sink.last < sink.first {
		t.Fatalf("archive calls=%d range=%d..%d removed=%d", sink.n, sink.first, sink.last, n)
	}
	if _, _, err := lg.ScanFrom(redo); err != nil {
		t.Fatalf("redo scan after prune: %v", err)
	}
}

func TestPruneArchivedBeforeArchiveFailureKeepsSegment(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	sink := &archiveSink{err: nerr.New(nerr.IO, "test", "archive failed")}
	lg, err := Create(dir, keys, id, Options{SegmentSize: 2 << 10, Archiver: sink})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	var last format.LSN
	for i := 0; i < 40; i++ {
		txn := lg.AllocTxn()
		_, _ = lg.Append(BeginRec(txn))
		last, _ = lg.Append(Record{Type: RecInsert, TxnID: txn, Body: make([]byte, 300)})
		_, _ = lg.Append(CommitRec(txn, last))
	}
	if err := lg.Flush(lg.NextLSN() - 1); err != nil {
		t.Fatal(err)
	}
	redo := lg.NextLSN()
	if err := lg.InstallCheckpoint(last, redo); err != nil {
		t.Fatal(err)
	}
	before, _ := listSegments(dir)
	if _, err := lg.PruneArchivedBefore(redo, nil); err == nil {
		t.Fatal("archive failure accepted")
	}
	after, _ := listSegments(dir)
	if len(after) != len(before) {
		t.Fatalf("archive failure removed segment: before=%v after=%v", before, after)
	}
}

func TestInstallRecordsReplica(t *testing.T) {
	keys, id := testIdent(t)
	src, err := Create(filepath.Join(t.TempDir(), "src"), keys, id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	txn := src.AllocTxn()
	if _, err := src.Append(BeginRec(txn)); err != nil {
		t.Fatal(err)
	}
	last, err := src.Append(CommitRec(txn, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Flush(last); err != nil {
		t.Fatal(err)
	}
	recs, _, err := src.ScanFrom(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) < 2 {
		t.Fatalf("src records %d", len(recs))
	}

	dst, err := Create(filepath.Join(t.TempDir(), "dst"), keys, id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.InstallRecords(recs); err != nil {
		t.Fatal(err)
	}
	got, _, err := dst.ScanFrom(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(recs) {
		t.Fatalf("dst %d src %d", len(got), len(recs))
	}
	if err := dst.InstallRecords(recs); err != nil {
		t.Fatal(err)
	}
	id2, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	gap, err := Create(filepath.Join(t.TempDir(), "gap"), keys, id2, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer gap.Close()
	if err := gap.InstallRecords(recs[1:]); err == nil {
		t.Fatal("LSN gap must fail")
	}
}

// A durability barrier that fails leaves what actually reached stable storage
// indeterminate, and the platform will not report the same failure twice: a
// Linux writeback error is delivered to one fsync and then cleared. Before the
// latch, the next flush synced only the *following* bytes, succeeded, and
// advanced durableLSN across the un-synced region — acknowledging a commit
// whose predecessor could be missing from the log entirely.
func TestFailedSyncLatchesAndRefusesLaterCommits(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	lg, err := Create(dir, keys, id, Options{SegmentSize: 256 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	a := lg.AllocTxn()
	if _, err := lg.Append(BeginRec(a)); err != nil {
		t.Fatal(err)
	}
	lsnA, err := lg.Append(CommitRec(a, 1))
	if err != nil {
		t.Fatal(err)
	}

	failSync := true
	restore := diskio.SetFaultForTest(func(f diskio.Fault) error {
		if failSync && (f.Op == "sync" || f.Op == "datasync") {
			return errors.New("EIO")
		}
		return nil
	})
	defer restore()

	if err := lg.Flush(lsnA); !nerr.HasCode(err, nerr.IO) {
		t.Fatalf("Flush(A) = %v, want an IO failure", err)
	}
	if lg.DurableLSN() >= lsnA {
		t.Fatalf("durable %d advanced across a failed fsync at %d", lg.DurableLSN(), lsnA)
	}

	// The kernel has now cleared the error; every subsequent fsync succeeds.
	failSync = false

	if _, err := lg.Append(BeginRec(lg.AllocTxn())); !nerr.HasCode(err, nerr.IO) {
		t.Fatalf("Append after a latched failure = %v, want the latched IO failure", err)
	}
	if err := lg.Flush(lsnA); !nerr.HasCode(err, nerr.IO) {
		t.Fatalf("Flush retry after a latched failure = %v, want the latched IO failure", err)
	}
	if lg.DurableLSN() >= lsnA {
		t.Fatalf("durable %d advanced across the un-synced region after the latch", lg.DurableLSN())
	}
}

// A write that consumed nothing left the buffer intact at an unchanged offset,
// so a transient ENOSPC an operator clears must still be able to make progress.
func TestFailedWriteConsumingNothingStaysRetryable(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	lg, err := Create(dir, keys, id, Options{SegmentSize: 256 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	txn := lg.AllocTxn()
	if _, err := lg.Append(BeginRec(txn)); err != nil {
		t.Fatal(err)
	}
	lsn, err := lg.Append(CommitRec(txn, 1))
	if err != nil {
		t.Fatal(err)
	}

	full := true
	restore := diskio.SetFaultForTest(func(f diskio.Fault) error {
		if full && f.Op == "write" {
			return errors.New("ENOSPC")
		}
		return nil
	})
	defer restore()

	if err := lg.Flush(lsn); !nerr.HasCode(err, nerr.IO) {
		t.Fatalf("Flush under ENOSPC = %v, want an IO failure", err)
	}
	if lg.DurableLSN() >= lsn {
		t.Fatalf("durable %d advanced across a failed write at %d", lg.DurableLSN(), lsn)
	}

	full = false
	if err := lg.Flush(lsn); err != nil {
		t.Fatalf("Flush after the write fault cleared = %v, want progress", err)
	}
	if lg.DurableLSN() != lsn {
		t.Fatalf("durable %d after recovery, want %d", lg.DurableLSN(), lsn)
	}

	recs, _, err := lg.ScanFrom(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("scanned %d records, want 2", len(recs))
	}
}

// TestOpenRefusesMissingSegments pins the durability boundary at open. The
// control file names a redo boundary the pages on disk are behind; if the
// segment that covers it is gone, the log cannot be replayed to the point the
// engine acknowledged. Coming up anyway presents a database missing
// acknowledged commits and — once the next checkpoint installs over the gap —
// makes that loss permanent.
func TestOpenRefusesMissingSegments(t *testing.T) {
	segs := func(t *testing.T, dir string) []string {
		t.Helper()
		names, err := filepath.Glob(filepath.Join(dir, "wal-*.seg"))
		if err != nil {
			t.Fatal(err)
		}
		if len(names) == 0 {
			t.Fatal("no segments were written")
		}
		return names
	}

	t.Run("all segments deleted", func(t *testing.T) {
		keys, id := testIdent(t)
		dir := filepath.Join(t.TempDir(), "wal")
		lg, err := Create(dir, keys, id, Options{SegmentSize: 64 << 10})
		if err != nil {
			t.Fatal(err)
		}
		txn := lg.AllocTxn()
		if _, err := lg.Append(BeginRec(txn)); err != nil {
			t.Fatal(err)
		}
		lsn, err := lg.Append(CommitRec(txn, 1))
		if err != nil {
			t.Fatal(err)
		}
		if err := lg.Flush(lsn); err != nil {
			t.Fatal(err)
		}
		if err := lg.Close(); err != nil {
			t.Fatal(err)
		}
		for _, name := range segs(t, dir) {
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
		}
		lg, err = Open(dir, keys, id, Options{SegmentSize: 64 << 10})
		if err == nil {
			_ = lg.Close()
			t.Fatal("Open accepted a WAL whose control file records acknowledged records but has no segments")
		}
		if !nerr.HasCode(err, nerr.Corruption) {
			t.Fatalf("want a corruption error, got: %v", err)
		}
	})

	t.Run("oldest segment deleted", func(t *testing.T) {
		keys, id := testIdent(t)
		dir := filepath.Join(t.TempDir(), "wal")
		lg, err := Create(dir, keys, id, Options{SegmentSize: 4 << 10})
		if err != nil {
			t.Fatal(err)
		}
		// Enough traffic to roll past the first segment; the redo boundary
		// stays at the start because no checkpoint is installed.
		var lsn format.LSN
		for i := 0; i < 200; i++ {
			txn := lg.AllocTxn()
			if _, err := lg.Append(BeginRec(txn)); err != nil {
				t.Fatal(err)
			}
			if lsn, err = lg.Append(CommitRec(txn, format.LSN(i+1))); err != nil {
				t.Fatal(err)
			}
		}
		if err := lg.Flush(lsn); err != nil {
			t.Fatal(err)
		}
		if err := lg.Close(); err != nil {
			t.Fatal(err)
		}
		names := segs(t, dir)
		if len(names) < 2 {
			t.Fatalf("expected the log to roll over, got %d segment(s)", len(names))
		}
		sort.Strings(names)
		if err := os.Remove(names[0]); err != nil {
			t.Fatal(err)
		}
		lg, err = Open(dir, keys, id, Options{SegmentSize: 4 << 10})
		if err == nil {
			_ = lg.Close()
			t.Fatal("Open accepted a WAL whose oldest segment no longer covers the redo boundary")
		}
		if !nerr.HasCode(err, nerr.Corruption) {
			t.Fatalf("want a corruption error, got: %v", err)
		}
	})

	t.Run("fresh log still opens", func(t *testing.T) {
		keys, id := testIdent(t)
		dir := filepath.Join(t.TempDir(), "wal")
		lg, err := Create(dir, keys, id, Options{SegmentSize: 64 << 10})
		if err != nil {
			t.Fatal(err)
		}
		if err := lg.Close(); err != nil {
			t.Fatal(err)
		}
		// Nothing was ever acknowledged, so a log with no segment on disk is
		// an empty log, not a hole: it must still open.
		for _, name := range segs(t, dir) {
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
		}
		lg, err = Open(dir, keys, id, Options{SegmentSize: 64 << 10})
		if err != nil {
			t.Fatalf("Open rejected an empty log: %v", err)
		}
		if err := lg.Close(); err != nil {
			t.Fatal(err)
		}
	})
}
