package xport

import (
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

func FuzzDecodeHeader(f *testing.F) {
	h := Header{
		Version:    CurrentVersion,
		Suite:      1,
		KeyVersion: 1,
		NonceHigh:  1,
		WrappedDEK: make([]byte, 64),
	}
	if raw, err := encodeHeader(h); err == nil {
		f.Add(raw)
	}
	f.Add([]byte("NSXP"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, err := decodeHeader(raw)
		if err == nil {
			return
		}
		if !nerr.HasCode(err, nerr.InvalidFormat) && !nerr.HasCode(err, nerr.Corruption) && !nerr.HasCode(err, nerr.Crypto) {
			t.Fatalf("uncontrolled error: %v", err)
		}
	})
}

func FuzzDecodePayload(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{recTable, 0, 0, 0, 0})
	f.Add([]byte{recRow, 1, 0, 't', 0, 0, 0, 0})
	f.Add([]byte{99})
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, err := decodePayload(raw)
		if err == nil {
			return
		}
		if !nerr.HasCode(err, nerr.InvalidFormat) && !nerr.HasCode(err, nerr.Corruption) && !nerr.HasCode(err, nerr.InvalidArgument) {
			t.Fatalf("uncontrolled error: %v", err)
		}
	})
}
