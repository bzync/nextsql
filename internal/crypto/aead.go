package crypto

import (
	"crypto/rand"
	"time"

	"github.com/bzync/nextsql/internal/metrics"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

// SealBytesRandom encrypts plaintext with a random 96-bit nonce.
// Use this for one-off envelopes such as wrapped DEKs.
func SealBytesRandom(dek *DEK, aad, plaintext []byte) (nonce, ciphertext []byte, err error) {
	if dek == nil {
		return nil, nil, nerr.New(nerr.InvalidArgument, "crypto.SealBytesRandom", "nil DEK")
	}
	if dek.Suite != format.CipherAES256GCM {
		return nil, nil, nerr.New(nerr.Crypto, "crypto.SealBytesRandom", "unsupported cipher suite")
	}
	var n [nonceSize]byte
	if _, err := rand.Read(n[:]); err != nil {
		return nil, nil, nerr.Wrap(nerr.Crypto, "crypto.SealBytesRandom", "rand", err)
	}
	aead, err := newGCM(dek)
	if err != nil {
		return nil, nil, err
	}
	return n[:], aead.Seal(nil, n[:], plaintext, aad), nil
}

// SealBytes encrypts plaintext with AES-256-GCM.
// generation must be unique for this DEK; 0 is reserved.
func SealBytes(dek *DEK, generation uint64, aad, plaintext []byte) (nonce, ciphertext []byte, err error) {
	if dek == nil {
		return nil, nil, nerr.New(nerr.InvalidArgument, "crypto.SealBytes", "nil DEK")
	}
	if dek.Suite != format.CipherAES256GCM {
		return nil, nil, nerr.New(nerr.Crypto, "crypto.SealBytes", "unsupported cipher suite")
	}
	if generation == 0 {
		return nil, nil, nerr.New(nerr.InvalidArgument, "crypto.SealBytes", "generation 0 is reserved")
	}
	start := time.Now()
	aead, err := newGCM(dek)
	if err != nil {
		return nil, nil, err
	}
	n := makeNonce(dek.Version, generation)
	out := aead.Seal(nil, n[:], plaintext, aad)
	metrics.Default().ObserveSeal(int64(len(plaintext)), time.Since(start))
	return n[:], out, nil
}

// SealBytesInto is SealBytes writing ciphertext into dst when cap is enough.
func SealBytesInto(dek *DEK, generation uint64, aad, plaintext, dst []byte) (nonce, ciphertext []byte, err error) {
	if dek == nil {
		return nil, nil, nerr.New(nerr.InvalidArgument, "crypto.SealBytes", "nil DEK")
	}
	if dek.Suite != format.CipherAES256GCM {
		return nil, nil, nerr.New(nerr.Crypto, "crypto.SealBytes", "unsupported cipher suite")
	}
	if generation == 0 {
		return nil, nil, nerr.New(nerr.InvalidArgument, "crypto.SealBytes", "generation 0 is reserved")
	}
	start := time.Now()
	aead, err := newGCM(dek)
	if err != nil {
		return nil, nil, err
	}
	n := makeNonce(dek.Version, generation)
	need := len(plaintext) + aead.Overhead()
	if cap(dst) < need {
		dst = make([]byte, 0, need)
	} else {
		dst = dst[:0]
	}
	out := aead.Seal(dst, n[:], plaintext, aad)
	metrics.Default().ObserveSeal(int64(len(plaintext)), time.Since(start))
	return n[:], out, nil
}

// OpenBytes decrypts a ciphertext produced by SealBytes.
func OpenBytes(dek *DEK, nonce, aad, ciphertext []byte) ([]byte, error) {
	if dek == nil {
		return nil, nerr.New(nerr.InvalidArgument, "crypto.OpenBytes", "nil DEK")
	}
	if dek.Suite != format.CipherAES256GCM {
		return nil, nerr.New(nerr.Crypto, "crypto.OpenBytes", "unsupported cipher suite")
	}
	if len(nonce) != nonceSize {
		return nil, nerr.New(nerr.InvalidFormat, "crypto.OpenBytes", "invalid nonce size")
	}
	start := time.Now()
	aead, err := newGCM(dek)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, nerr.New(nerr.Crypto, "crypto.OpenBytes", "authentication failed")
	}
	metrics.Default().ObserveOpen(int64(len(plain)), time.Since(start))
	return plain, nil
}
