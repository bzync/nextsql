package backup

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
	f.Add([]byte("NSBK"))
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

func FuzzDecodeManifest(f *testing.F) {
	mf := Manifest{Version: CurrentVersion, Members: []Member{{
		Kind: KindData, Name: "data",
	}}}
	if raw, err := encodeManifest(mf); err == nil {
		f.Add(raw)
	}
	f.Add([]byte("NSMF"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, err := decodeManifest(raw)
		if err == nil {
			return
		}
		if !nerr.HasCode(err, nerr.InvalidFormat) && !nerr.HasCode(err, nerr.Corruption) {
			t.Fatalf("uncontrolled error: %v", err)
		}
	})
}
