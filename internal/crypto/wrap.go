package crypto

import (
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

const (
	// DomainKEK is the key-encryption key, wrapped by the external root.
	DomainKEK byte = 'K'
	// DomainMaster is the database master, wrapped by the KEK.
	DomainMaster byte = 'M'
	// DomainPage encrypts user pages.
	DomainPage byte = 'P'
	// DomainWAL binds a wrapped DEK to the WAL domain so it cannot be
	// confused with a page or backup key.
	DomainWAL byte = 'W'
	// DomainUNDO binds a wrapped DEK to the UNDO log domain.
	DomainUNDO byte = 'U'
	// DomainBackup binds a wrapped DEK to backup chunks and WAL archives.
	DomainBackup byte = 'B'
	// DomainVector encrypts detached vector blocks and HNSW graphs.
	DomainVector byte = 'V'
	// DomainFullText encrypts inverted-index structures.
	DomainFullText byte = 'F'
	// DomainTemp encrypts durable temporary files.
	DomainTemp byte = 'T'
	// DomainRepl encrypts Raft command payloads (WAL-record batches).
	DomainRepl byte = 'R'

	wrapVersion    = 1
	wrapHeaderSize = 2 + 2 + 4 + 1 + 12 // ver, suite, keyver, domain, nonce
)

// AllDomains is the production set of data-encryption domains.
var AllDomains = []byte{
	DomainPage, DomainWAL, DomainUNDO, DomainBackup,
	DomainVector, DomainFullText, DomainTemp, DomainRepl,
}

// DomainName returns a stable label. It never includes key material.
func DomainName(d byte) string {
	switch d {
	case DomainKEK:
		return "kek"
	case DomainMaster:
		return "master"
	case DomainPage:
		return "page"
	case DomainWAL:
		return "wal"
	case DomainUNDO:
		return "undo"
	case DomainBackup:
		return "backup"
	case DomainVector:
		return "vector"
	case DomainFullText:
		return "fulltext"
	case DomainTemp:
		return "temp"
	case DomainRepl:
		return "replication"
	default:
		return "unknown"
	}
}

// WrapParent is the key used to wrap WAL/UNDO DEKs. An Envelope uses the
// database master so a page-DEK rotation does not brick the log. A flat
// KeyProvider wraps under Current().
func WrapParent(keys KeyProvider) (*DEK, error) {
	if keys == nil {
		return nil, nerr.New(nerr.InvalidArgument, "crypto.WrapParent", "nil key provider")
	}
	type masterer interface {
		Master() (*DEK, error)
	}
	if m, ok := keys.(masterer); ok {
		return m.Master()
	}
	return keys.Current()
}

// WrapDEK encrypts dek under kek. The blob is not a connection secret and
// must not be logged.
func WrapDEK(kek, dek *DEK, domain byte) ([]byte, error) {
	if kek == nil || dek == nil {
		return nil, nerr.New(nerr.InvalidArgument, "crypto.WrapDEK", "nil DEK")
	}
	if domain == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "crypto.WrapDEK", "domain is required")
	}
	aad := wrapAAD(dek.Suite, dek.Version, domain)
	nonce, ct, err := SealBytesRandom(kek, aad, dek.keyBytes())
	if err != nil {
		return nil, err
	}
	out := make([]byte, wrapHeaderSize+len(ct))
	encoding.PutU16(out, 0, wrapVersion)
	encoding.PutU16(out, 2, uint16(dek.Suite))
	encoding.PutU32(out, 4, uint32(dek.Version))
	out[8] = domain
	copy(out[9:21], nonce)
	copy(out[21:], ct)
	return out, nil
}

// UnwrapDEK decrypts a blob produced by WrapDEK.
func UnwrapDEK(kek *DEK, blob []byte, domain byte) (*DEK, error) {
	if kek == nil {
		return nil, nerr.New(nerr.InvalidArgument, "crypto.UnwrapDEK", "nil KEK")
	}
	if len(blob) < wrapHeaderSize+AES256KeySize+format.AuthTagSize {
		return nil, nerr.New(nerr.InvalidFormat, "crypto.UnwrapDEK", "truncated wrapped DEK")
	}
	if encoding.U16(blob, 0) != wrapVersion {
		return nil, nerr.New(nerr.InvalidFormat, "crypto.UnwrapDEK", "unsupported wrap version")
	}
	suite := format.CipherSuite(encoding.U16(blob, 2))
	ver := format.KeyVersion(encoding.U32(blob, 4))
	gotDomain := blob[8]
	if gotDomain != domain {
		return nil, nerr.New(nerr.Crypto, "crypto.UnwrapDEK", "wrapped key domain mismatch")
	}
	nonce := blob[9:21]
	ct := blob[21:]
	aad := wrapAAD(suite, ver, domain)
	key, err := OpenBytes(kek, nonce, aad, ct)
	if err != nil {
		return nil, err
	}
	return DEKFromBytes(ver, key)
}

func wrapAAD(suite format.CipherSuite, ver format.KeyVersion, domain byte) []byte {
	aad := make([]byte, 9)
	encoding.PutU16(aad, 0, wrapVersion)
	encoding.PutU16(aad, 2, uint16(suite))
	encoding.PutU32(aad, 4, uint32(ver))
	aad[8] = domain
	return aad
}
