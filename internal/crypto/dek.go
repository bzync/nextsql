package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"sync"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

const AES256KeySize = 32

// DEK is an AES-256 data-encryption key. The raw bytes are never printed.
type DEK struct {
	Version format.KeyVersion
	Suite   format.CipherSuite
	key     [AES256KeySize]byte

	gcmMu sync.Mutex
	gcm   cipher.AEAD
}

func GenerateDEK(version format.KeyVersion) (*DEK, error) {
	d := &DEK{Version: version, Suite: format.CipherAES256GCM}
	if _, err := rand.Read(d.key[:]); err != nil {
		return nil, nerr.Wrap(nerr.Crypto, "crypto.GenerateDEK", "rand", err)
	}
	return d, nil
}

func DEKFromBytes(version format.KeyVersion, key []byte) (*DEK, error) {
	if len(key) != AES256KeySize {
		return nil, nerr.New(nerr.InvalidArgument, "crypto.DEKFromBytes", "AES-256 key must be 32 bytes")
	}
	d := &DEK{Version: version, Suite: format.CipherAES256GCM}
	copy(d.key[:], key)
	return d, nil
}

func (d *DEK) Equal(other *DEK) bool {
	if d == nil || other == nil {
		return d == other
	}
	if d.Version != other.Version || d.Suite != other.Suite {
		return false
	}
	return subtle.ConstantTimeCompare(d.key[:], other.key[:]) == 1
}

// Zero overwrites key material. The DEK is unusable afterwards.
func (d *DEK) Zero() {
	if d == nil {
		return
	}
	for i := range d.key {
		d.key[i] = 0
	}
	d.gcmMu.Lock()
	d.gcm = nil
	d.gcmMu.Unlock()
}

func (d *DEK) clone() *DEK {
	if d == nil {
		return nil
	}
	return &DEK{Version: d.Version, Suite: d.Suite, key: d.key}
}

// AEAD returns a cached AES-256-GCM primitive for this DEK.
func (d *DEK) AEAD() (cipher.AEAD, error) {
	if d == nil {
		return nil, nerr.New(nerr.InvalidArgument, "crypto.AEAD", "nil DEK")
	}
	if d.Suite != format.CipherAES256GCM {
		return nil, nerr.New(nerr.Crypto, "crypto.AEAD", "unsupported cipher suite")
	}
	d.gcmMu.Lock()
	g := d.gcm
	d.gcmMu.Unlock()
	if g != nil {
		return g, nil
	}
	d.gcmMu.Lock()
	defer d.gcmMu.Unlock()
	if d.gcm != nil {
		return d.gcm, nil
	}
	block, err := aes.NewCipher(d.key[:])
	if err != nil {
		return nil, nerr.Wrap(nerr.Crypto, "crypto.AEAD", "aes", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nerr.Wrap(nerr.Crypto, "crypto.AEAD", "gcm", err)
	}
	d.gcm = aead
	return aead, nil
}

// keyBytes returns the raw key. Callers must not log or persist the result in plaintext user files.
func (d *DEK) keyBytes() []byte {
	return d.key[:]
}
