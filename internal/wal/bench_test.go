package wal

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/storage/format"
)

func benchLog(b *testing.B) *Log {
	b.Helper()
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		b.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		b.Fatal(err)
	}
	id, err := format.NewIdentity()
	if err != nil {
		b.Fatal(err)
	}
	lg, err := Create(filepath.Join(b.TempDir(), "wal"), keys, id, Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = lg.Close() })
	return lg
}

func BenchmarkWALAppend(b *testing.B) {
	lg := benchLog(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		txn := lg.AllocTxn()
		if _, err := lg.Append(BeginRec(txn)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWALGroupCommit(b *testing.B) {
	lg := benchLog(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		txn := lg.AllocTxn()
		lsn, err := lg.Append(BeginRec(txn))
		if err != nil {
			b.Fatal(err)
		}
		lsn, err = lg.Append(CommitRec(txn, lsn))
		if err != nil {
			b.Fatal(err)
		}
		if err := lg.Flush(lsn); err != nil {
			b.Fatal(err)
		}
	}
}
