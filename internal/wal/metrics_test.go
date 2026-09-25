package wal

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/metrics"
	"github.com/bzync/nextsql/internal/storage/format"
)

// wal_bytes_written is the "ever appended" WAL counter NextSQL Admin's
// Diagnostics view reports. Registry.AddWAL existed and was documented, but
// nothing called it, so the metric read 0 on every deployment no matter how
// much WAL had been written. This pins it to the segment write.
func TestWALBytesWrittenCountsSegmentWrites(t *testing.T) {
	keys, id := testIdent(t)
	dir := filepath.Join(t.TempDir(), "wal")
	lg, err := Create(dir, keys, id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	// The registry is process-global, so only this log's own delta is
	// meaningful — assert on the growth, never on an absolute value.
	before := metrics.Default().Snapshot().WALBytes

	var last format.LSN
	for i := 0; i < 32; i++ {
		lsn, aerr := lg.Append(Record{
			Type:  RecCommit,
			TxnID: format.TxnID(i + 1),
			Body:  []byte("commit-record-payload-that-occupies-some-bytes"),
		})
		if aerr != nil {
			t.Fatal(aerr)
		}
		last = lsn
	}
	if err := lg.Flush(last); err != nil {
		t.Fatal(err)
	}

	after := metrics.Default().Snapshot().WALBytes
	if after <= before {
		t.Fatalf("wal_bytes_written did not rise across a flush: %d then %d", before, after)
	}

	// It is cumulative: a second batch adds to the first rather than
	// replacing it, which is what distinguishes it from the on-disk
	// footprint gauge beside it.
	for i := 0; i < 32; i++ {
		lsn, aerr := lg.Append(Record{
			Type:  RecCommit,
			TxnID: format.TxnID(i + 100),
			Body:  []byte("second-batch-payload"),
		})
		if aerr != nil {
			t.Fatal(aerr)
		}
		last = lsn
	}
	if err := lg.Flush(last); err != nil {
		t.Fatal(err)
	}
	final := metrics.Default().Snapshot().WALBytes
	if final <= after {
		t.Fatalf("wal_bytes_written is not cumulative: %d then %d", after, final)
	}
}
