package crypto

import (
	"crypto/cipher"
	"time"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/metrics"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

const (
	nonceSize = 12
	aadSize   = 16
)

// SealPage encrypts a logical page into a physical envelope.
// generation must be unique for this DEK (reserved from the file superblock).
func SealPage(dek *DEK, pageID format.PageID, generation uint64, logical []byte) ([]byte, error) {
	return SealPageInto(dek, pageID, generation, logical, nil)
}

// SealPageInto is SealPage writing into dst when cap is enough.
func SealPageInto(dek *DEK, pageID format.PageID, generation uint64, logical, dst []byte) ([]byte, error) {
	if dek == nil {
		return nil, nerr.New(nerr.InvalidArgument, "crypto.SealPage", "nil DEK")
	}
	if dek.Suite != format.CipherAES256GCM {
		return nil, nerr.New(nerr.Crypto, "crypto.SealPage", "unsupported cipher suite")
	}
	if err := pageID.UserData(); err != nil {
		return nil, err
	}
	if len(logical) != format.LogicalPageSize {
		return nil, nerr.New(nerr.InvalidArgument, "crypto.SealPage", "logical page has wrong size")
	}
	if generation == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "crypto.SealPage", "generation 0 is reserved")
	}

	start := time.Now()
	aead, err := newGCM(dek)
	if err != nil {
		return nil, err
	}
	nonce := makeNonce(dek.Version, generation)
	var aadBuf [aadSize]byte
	fillAAD(aadBuf[:], format.CurrentEnvelopeVersion, dek.Suite, dek.Version, pageID)

	if cap(dst) < format.PhysicalPageSize {
		dst = make([]byte, format.PhysicalPageSize)
	} else {
		dst = dst[:format.PhysicalPageSize]
	}
	encoding.PutU16(dst, 0, format.CurrentEnvelopeVersion)
	encoding.PutU16(dst, 2, uint16(dek.Suite))
	encoding.PutU32(dst, 4, uint32(dek.Version))
	encoding.PutU64(dst, 8, uint64(pageID))
	copy(dst[16:28], nonce[:])
	// bytes 28-31 reserved flags
	for i := 28; i < format.EnvelopeHeaderSize && i < len(dst); i++ {
		dst[i] = 0
	}

	sealed := aead.Seal(dst[format.EnvelopeHeaderSize:format.EnvelopeHeaderSize], nonce[:], logical, aadBuf[:])
	if len(sealed) != format.LogicalPageSize+format.AuthTagSize {
		return nil, nerr.New(nerr.Internal, "crypto.SealPage", "unexpected AEAD output length")
	}
	metrics.Default().ObserveSeal(int64(format.LogicalPageSize), time.Since(start))
	return dst, nil
}

// OpenPage decrypts a physical envelope and checks that the page ID matches want.
func OpenPage(keys KeyProvider, want format.PageID, physical []byte) ([]byte, error) {
	dst := make([]byte, format.LogicalPageSize)
	if err := OpenPageInto(keys, want, physical, dst); err != nil {
		return nil, err
	}
	return dst, nil
}

// OpenPageInto decrypts a physical envelope into dst. dst must be exactly
// one logical page. The caller owns dst; this does not allocate a new page
// on the success path.
func OpenPageInto(keys KeyProvider, want format.PageID, physical, dst []byte) error {
	if keys == nil {
		return nerr.New(nerr.InvalidArgument, "crypto.OpenPage", "nil key provider")
	}
	if err := want.UserData(); err != nil {
		return err
	}
	if len(physical) != format.PhysicalPageSize {
		return nerr.New(nerr.InvalidFormat, "crypto.OpenPage", "truncated or oversized physical page")
	}
	if len(dst) != format.LogicalPageSize {
		return nerr.New(nerr.InvalidArgument, "crypto.OpenPage", "logical destination has wrong size")
	}

	envVer, err := encoding.ReadU16(physical, 0)
	if err != nil {
		return err
	}
	if envVer != format.CurrentEnvelopeVersion {
		return nerr.New(nerr.InvalidFormat, "crypto.OpenPage", "unsupported envelope version")
	}
	suite := format.CipherSuite(encoding.U16(physical, 2))
	if suite != format.CipherAES256GCM {
		return nerr.New(nerr.Crypto, "crypto.OpenPage", "unsupported cipher suite")
	}
	keyVer := format.KeyVersion(encoding.U32(physical, 4))
	gotID := format.PageID(encoding.U64(physical, 8))
	if gotID != want {
		return nerr.New(nerr.Corruption, "crypto.OpenPage", "envelope page id mismatch")
	}

	dek, err := keys.Key(keyVer)
	if err != nil {
		return err
	}
	if dek.Suite != suite {
		return nerr.New(nerr.Crypto, "crypto.OpenPage", "key suite does not match envelope")
	}

	start := time.Now()
	aead, err := newGCM(dek)
	if err != nil {
		return err
	}
	nonce := physical[16:28]
	var aadBuf [aadSize]byte
	fillAAD(aadBuf[:], envVer, suite, keyVer, gotID)
	ct := physical[format.EnvelopeHeaderSize : format.EnvelopeHeaderSize+format.LogicalPageSize+format.AuthTagSize]

	plain, err := aead.Open(dst[:0], nonce, ct, aadBuf[:])
	if err != nil {
		return nerr.New(nerr.Crypto, "crypto.OpenPage", "authentication failed")
	}
	if len(plain) != format.LogicalPageSize {
		return nerr.New(nerr.Corruption, "crypto.OpenPage", "decrypted page has wrong size")
	}
	if len(plain) > 0 && len(dst) > 0 && &plain[0] != &dst[0] {
		copy(dst, plain)
	}
	metrics.Default().ObserveOpen(int64(format.LogicalPageSize), time.Since(start))
	return nil
}

func newGCM(dek *DEK) (cipher.AEAD, error) {
	return dek.AEAD()
}

func makeNonce(version format.KeyVersion, generation uint64) [nonceSize]byte {
	var n [nonceSize]byte
	encoding.PutU64(n[:], 0, generation)
	encoding.PutU32(n[:], 8, uint32(version))
	return n
}

func makeAAD(envVer uint16, suite format.CipherSuite, version format.KeyVersion, id format.PageID) []byte {
	aad := make([]byte, aadSize)
	fillAAD(aad, envVer, suite, version, id)
	return aad
}

func fillAAD(aad []byte, envVer uint16, suite format.CipherSuite, version format.KeyVersion, id format.PageID) {
	if len(aad) < aadSize {
		return
	}
	encoding.PutU16(aad, 0, envVer)
	encoding.PutU16(aad, 2, uint16(suite))
	encoding.PutU32(aad, 4, uint32(version))
	encoding.PutU64(aad, 8, uint64(id))
}
