package wal

import (
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
)

func FuzzDecodePhysical(f *testing.F) {
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		f.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		f.Fatal(err)
	}
	phys, err := encodePhysical(dek, 1, 1, encodePayload(Record{Type: RecBegin, TxnID: 1}))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(phys)
	f.Add([]byte("NSWL"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, in []byte) {
		if len(in) < HeaderSize {
			_, _ = parseHeader(in)
			return
		}
		h, err := parseHeader(in[:HeaderSize])
		if err != nil {
			return
		}
		rest := in[HeaderSize:]
		if h.CTLen > len(rest) {
			return
		}
		_, _ = decodePhysical(keys, in[:HeaderSize], rest[:h.CTLen])
	})
}
