package replication

import (
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/wal"
)

func FuzzDecodeCommand(f *testing.F) {
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		f.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		f.Fatal(err)
	}
	good, err := EncodeCommand(dek, []wal.Record{{Type: wal.RecBegin, LSN: 1, TxnID: 1}})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good)
	f.Add([]byte{})
	f.Add([]byte("NSRL"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = DecodeCommand(keys, data)
	})
}
