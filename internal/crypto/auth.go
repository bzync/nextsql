package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

const FileAuthSize = 16

// FileAuthTag binds the file identity to the DEK so Open fails closed on the wrong key.
// This is not a substitute for page AEAD; it only authenticates the key-to-file pairing.
func FileAuthTag(dek *DEK, ident format.Identity) [FileAuthSize]byte {
	mac := hmac.New(sha256.New, dek.keyBytes())
	_, _ = mac.Write(ident.Database[:])
	_, _ = mac.Write(ident.File[:])
	var ver [4]byte
	encoding.PutU32(ver[:], 0, uint32(dek.Version))
	_, _ = mac.Write(ver[:])
	sum := mac.Sum(nil)
	var out [FileAuthSize]byte
	copy(out[:], sum)
	return out
}

func VerifyFileAuthTag(dek *DEK, ident format.Identity, got []byte) error {
	if dek == nil {
		return nerr.New(nerr.InvalidArgument, "crypto.VerifyFileAuthTag", "nil DEK")
	}
	want := FileAuthTag(dek, ident)
	if len(got) != FileAuthSize || subtle.ConstantTimeCompare(want[:], got) != 1 {
		return nerr.New(nerr.Crypto, "crypto.VerifyFileAuthTag", "key does not match file")
	}
	return nil
}
