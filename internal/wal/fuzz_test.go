package wal

import (
	"bytes"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/encoding"
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

func FuzzDecodeUndoBody(f *testing.F) {
	v2 := EncodeUndoBody(UndoBody{
		ID:         123,
		Prev:       456,
		Kind:       1,
		OldXmin:    789,
		OldXmax:    101112,
		OldUndo:    131415,
		Key:        []byte("user:1001"),
		OldPayload: []byte("payload data here"),
	})
	f.Add(v2)

	v1 := make([]byte, 11+4)
	encoding.PutU64(v1, 0, 999)
	v1[8] = 2
	encoding.PutU16(v1, 9, 4)
	copy(v1[11:], []byte("test"))
	f.Add(v1)

	f.Add([]byte{})
	f.Add([]byte{2})
	f.Add(make([]byte, 48))

	f.Fuzz(func(t *testing.T, in []byte) {
		ub, err := DecodeUndoBody(in)
		if err != nil {
			return
		}
		// If valid V2, re-encoding must match bit-for-bit
		if len(in) >= 48 && in[0] == 2 {
			re := EncodeUndoBody(ub)
			if !bytes.Equal(re, in) {
				t.Fatalf("round-trip mismatch: got %x, want %x", re, in)
			}
		}
	})
}
