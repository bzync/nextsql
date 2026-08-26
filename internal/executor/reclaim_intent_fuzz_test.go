package executor

import (
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

func FuzzDecodeReclaimIntent(f *testing.F) {
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		f.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		f.Fatal(err)
	}
	id, err := format.NewIdentity()
	if err != nil {
		f.Fatal(err)
	}
	hdr := make([]byte, reclaimHeader)
	encoding.PutU32(hdr, 0, reclaimMagic)
	encoding.PutU16(hdr, 4, reclaimVersion)
	encoding.PutU32(hdr, 8, uint32(dek.Version))
	copy(hdr[12:28], id.Database[:])
	copy(hdr[28:44], id.File[:])
	encoding.PutU32(hdr, 44, 1)
	plain := make([]byte, 8)
	encoding.PutU64(plain, 0, uint64(format.FirstAllocPageID))
	nonce, sealed, err := crypto.SealBytesRandom(dek, hdr[:48], plain)
	if err != nil {
		f.Fatal(err)
	}
	copy(hdr[48:60], nonce)
	f.Add(append(hdr, sealed...))
	f.Add([]byte("NSRI"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, raw []byte) {
		_, err := decodeReclaimIntent(raw, id, keys)
		if err == nil {
			return
		}
		if !nerr.HasCode(err, nerr.InvalidFormat) &&
			!nerr.HasCode(err, nerr.Corruption) &&
			!nerr.HasCode(err, nerr.Crypto) &&
			!nerr.HasCode(err, nerr.InvalidArgument) {
			t.Fatalf("uncontrolled error: %v", err)
		}
	})
}
