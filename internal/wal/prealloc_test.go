package wal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/storage/format"
	diskio "github.com/bzync/nextsql/internal/storage/io"
)

func TestWALPreallocatedSegmentCreation(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	segSize := int64(256 << 10) // 256 KiB

	lg, err := Create(dir, keys, id, Options{SegmentSize: segSize})
	if err != nil {
		t.Fatal(err)
	}

	// Verify segment file exists and matches the preallocated segment size.
	segPath := filepath.Join(dir, segmentName(1))
	st, err := os.Stat(segPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != segSize {
		t.Fatalf("expected preallocated segment size %d, got %d", segSize, st.Size())
	}

	// Verify preallocated bytes after header are zero.
	tail := make([]byte, segSize-SegmentHeaderSize)
	if err := diskio.ReadFullAt(lg.seg, tail, SegmentHeaderSize); err != nil {
		t.Fatal(err)
	}
	if !isAllZero(tail) {
		t.Fatal("preallocated tail contains non-zero bytes")
	}

	// Append a single record.
	rec := Record{
		Type:    RecBegin,
		LSN:     1,
		TxnID:   100,
		PrevLSN: 0,
		PageID:  0,
		Body:    []byte("payload-1"),
	}
	lsn, err := lg.Append(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := lg.Flush(lsn); err != nil {
		t.Fatal(err)
	}
	if lsn != 1 {
		t.Fatalf("expected LSN 1, got %d", lsn)
	}

	// File size must still be the preallocated segment size (not extended or shrunk).
	stAfter, err := os.Stat(segPath)
	if err != nil {
		t.Fatal(err)
	}
	if stAfter.Size() != segSize {
		t.Fatalf("segment size changed after write: got %d, want %d", stAfter.Size(), segSize)
	}

	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWALPreallocatedSegmentReopenAndContiguousAppend(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	segSize := int64(256 << 10)

	lg, err := Create(dir, keys, id, Options{SegmentSize: segSize})
	if err != nil {
		t.Fatal(err)
	}

	rec1 := Record{
		Type:  RecBegin,
		LSN:   1,
		TxnID: 101,
	}
	lsn1, err := lg.Append(rec1)
	if err != nil {
		t.Fatal(err)
	}
	if err := lg.Flush(lsn1); err != nil {
		t.Fatal(err)
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen the WAL.
	lg2, err := Open(dir, keys, id, Options{SegmentSize: segSize})
	if err != nil {
		t.Fatal(err)
	}
	defer lg2.Close()

	// Scan must find rec1 and NOT truncate the preallocated segment file.
	recs, last, err := lg2.ScanFrom(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || last != 1 {
		t.Fatalf("scan returned %d records, last=%d; want 1 record, last=1", len(recs), last)
	}

	segPath := filepath.Join(dir, segmentName(1))
	st, err := os.Stat(segPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != segSize {
		t.Fatalf("ScanFrom truncated preallocated segment: got %d, want %d", st.Size(), segSize)
	}

	// Append rec2.
	rec2 := Record{
		Type:    RecCommit,
		LSN:     2,
		TxnID:   101,
		PrevLSN: 1,
	}
	lsn2, err := lg2.Append(rec2)
	if err != nil {
		t.Fatal(err)
	}
	if err := lg2.Flush(lsn2); err != nil {
		t.Fatal(err)
	}

	// Scan again: verify both records exist contiguously.
	recs2, last2, err := lg2.ScanFrom(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs2) != 2 || last2 != 2 {
		t.Fatalf("expected 2 records, got %d, last=%d", len(recs2), last2)
	}
	if recs2[0].LSN != 1 || recs2[1].LSN != 2 {
		t.Fatalf("records out of order: %+v, %+v", recs2[0], recs2[1])
	}
}

func TestWALPreallocatedRotationPreservesZeroTail(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	segSize := int64(8 << 10) // 8 KiB

	lg, err := Create(dir, keys, id, Options{SegmentSize: segSize})
	if err != nil {
		t.Fatal(err)
	}

	// Write enough records to trigger multiple segment rotations.
	var count uint64 = 60
	for i := uint64(1); i <= count; i++ {
		rec := Record{
			Type:    RecInsert,
			LSN:     format.LSN(i),
			TxnID:   format.TxnID(i),
			PrevLSN: format.LSN(i - 1),
			Body:    make([]byte, 400),
		}
		lsn, err := lg.Append(rec)
		if err != nil {
			t.Fatal(err)
		}
		if err := lg.Flush(lsn); err != nil {
			t.Fatal(err)
		}
	}

	ids, err := listSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) < 3 {
		t.Fatalf("expected at least 3 segments, got %d", len(ids))
	}

	// Check that each segment is preallocated to segSize.
	for _, id := range ids {
		p := filepath.Join(dir, segmentName(id))
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Size() != segSize {
			t.Fatalf("segment %d size %d != %d", id, st.Size(), segSize)
		}
	}

	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen and scan all records.
	lg2, err := Open(dir, keys, id, Options{SegmentSize: segSize})
	if err != nil {
		t.Fatal(err)
	}
	defer lg2.Close()

	recs, last, err := lg2.ScanFrom(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != int(count) || last != format.LSN(count) {
		t.Fatalf("expected %d records, got %d (last %d)", count, len(recs), last)
	}
}

func TestWALPreallocatedTornTailRecovery(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	segSize := int64(64 << 10) // 64 KiB

	lg, err := Create(dir, keys, id, Options{SegmentSize: segSize})
	if err != nil {
		t.Fatal(err)
	}

	rec1 := Record{
		Type:  RecBegin,
		LSN:   1,
		TxnID: 201,
	}
	lsn1, err := lg.Append(rec1)
	if err != nil {
		t.Fatal(err)
	}
	if err := lg.Flush(lsn1); err != nil {
		t.Fatal(err)
	}

	// Record write offset before closing.
	validOff := lg.segOff

	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate torn write by writing 8 bytes of non-zero corrupt data at validOff.
	segPath := filepath.Join(dir, segmentName(1))
	f, err := os.OpenFile(segPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	corruptBytes := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x03, 0x04}
	if _, err := f.WriteAt(corruptBytes, validOff); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen WAL.
	lg2, err := Open(dir, keys, id, Options{SegmentSize: segSize})
	if err != nil {
		t.Fatal(err)
	}
	defer lg2.Close()

	// ScanFrom must detect the torn tail (past durable LSN 1), truncate it, and return rec1.
	recs, last, err := lg2.ScanFrom(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || last != 1 {
		t.Fatalf("expected 1 record (last=1), got %d (last=%d)", len(recs), last)
	}

	// Write rec2. Must succeed and append at validOff.
	rec2 := Record{
		Type:    RecCommit,
		LSN:     2,
		TxnID:   201,
		PrevLSN: 1,
	}
	lsn2, err := lg2.Append(rec2)
	if err != nil {
		t.Fatal(err)
	}
	if err := lg2.Flush(lsn2); err != nil {
		t.Fatal(err)
	}

	// Scan both records.
	recs2, last2, err := lg2.ScanFrom(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs2) != 2 || last2 != 2 {
		t.Fatalf("expected 2 records, got %d, last=%d", len(recs2), last2)
	}
}
