package wal

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
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
