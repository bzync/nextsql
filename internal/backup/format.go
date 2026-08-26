package backup

import (
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/checksum"
	"github.com/bzync/nextsql/internal/storage/format"
)

const (
	// HeaderMagic is ASCII 'N','S','B','K'.
	HeaderMagic uint32 = 0x4B42534E
	// ManifestMagic is ASCII 'N','S','M','F'.
	ManifestMagic uint32 = 0x464D534E
	// MemberMagic is ASCII 'N','S','B','M'.
	MemberMagic uint32 = 0x4D42534E
	// ArchiveMagic is ASCII 'N','S','W','A'.
	ArchiveMagic uint32 = 0x4157534E

	CurrentVersion uint16 = 1

	headerFixedSize = 120
	maxWrapBlob     = 1 << 16
	maxMembers      = 4096
	maxNameLen      = 255
	maxChunkSize    = 1 << 20
	maxArchiveEnt   = 1 << 20

	headerName    = "header"
	manifestName  = "manifest"
	verifiedName  = "verified"
	keystoreName  = "keystore"
	partialSuffix = ".partial"
	memberDirName = "members"
	archiveIndex  = "index"
	defaultChunk  = 1 << 20
)

// Kind classifies one backup member.
type Kind uint8

const (
	KindInvalid  Kind = 0
	KindData     Kind = 1
	KindKeys     Kind = 2
	KindWALCtrl  Kind = 3
	KindWALSeg   Kind = 4
	KindUNDOCtrl Kind = 5
	KindUNDOLog  Kind = 6
	KindUsers    Kind = 7
	KindACL      Kind = 8
	KindManifest Kind = 9
	KindReclaim  Kind = 10
)

func (k Kind) known() bool {
	return k >= KindData && k <= KindReclaim
}

func (k Kind) String() string {
	switch k {
	case KindData:
		return "data"
	case KindKeys:
		return "keys"
	case KindWALCtrl:
		return "wal_control"
	case KindWALSeg:
		return "wal_segment"
	case KindUNDOCtrl:
		return "undo_control"
	case KindUNDOLog:
		return "undo_log"
	case KindUsers:
		return "users"
	case KindACL:
		return "acl"
	case KindManifest:
		return "manifest"
	case KindReclaim:
		return "reclaim_intent"
	default:
		return "invalid"
	}
}

// Header is the plaintext backup prologue. It holds identity and the wrapped
// backup DEK. It never contains a raw root or DEK.
type Header struct {
	Version     uint16
	Flags       uint16
	Suite       format.CipherSuite
	KeyVersion  format.KeyVersion
	Identity    format.Identity
	Checkpoint  format.LSN
	RedoLSN     format.LSN
	DurableLSN  format.LSN
	CreatedNano int64
	BackupID    [16]byte
	NonceHigh   uint64
	WrappedDEK  []byte
}

// Member is one file listed in the encrypted manifest.
type Member struct {
	Kind       Kind
	Name       string
	PlainSize  uint64
	SealedSize uint64
	SHA256     [32]byte
	FirstLSN   format.LSN
	LastLSN    format.LSN
}

// Manifest is the encrypted inventory of a backup.
type Manifest struct {
	Version uint16
	Members []Member
}

func encodeHeader(h Header) ([]byte, error) {
	if len(h.WrappedDEK) == 0 || len(h.WrappedDEK) > maxWrapBlob {
		return nil, nerr.New(nerr.InvalidFormat, "backup.encodeHeader", "invalid wrapped backup DEK length")
	}
	buf := make([]byte, headerFixedSize+len(h.WrappedDEK)+4)
	encoding.PutU32(buf, 0, HeaderMagic)
	encoding.PutU16(buf, 4, CurrentVersion)
	encoding.PutU16(buf, 6, h.Flags)
	encoding.PutU16(buf, 8, uint16(h.Suite))
	encoding.PutU32(buf, 12, uint32(h.KeyVersion))
	copy(buf[16:32], h.Identity.Database[:])
	copy(buf[32:48], h.Identity.File[:])
	encoding.PutU64(buf, 48, uint64(h.Checkpoint))
	encoding.PutU64(buf, 56, uint64(h.RedoLSN))
	encoding.PutU64(buf, 64, uint64(h.DurableLSN))
	encoding.PutU64(buf, 72, uint64(h.CreatedNano))
	copy(buf[80:96], h.BackupID[:])
	encoding.PutU64(buf, 96, h.NonceHigh)
	encoding.PutU16(buf, 104, uint16(len(h.WrappedDEK)))
	copy(buf[headerFixedSize:], h.WrappedDEK)
	checksum.Write(buf, len(buf)-4)
	return buf, nil
}

func decodeHeader(raw []byte) (Header, error) {
	if len(raw) < headerFixedSize+4 {
		return Header{}, nerr.New(nerr.InvalidFormat, "backup.decodeHeader", "truncated header")
	}
	if encoding.U32(raw, 0) != HeaderMagic {
		return Header{}, nerr.New(nerr.InvalidFormat, "backup.decodeHeader", "bad backup magic")
	}
	if encoding.U16(raw, 4) != CurrentVersion {
		return Header{}, nerr.New(nerr.InvalidFormat, "backup.decodeHeader", "unsupported backup version")
	}
	if err := checksum.Verify(raw, len(raw)-4); err != nil {
		return Header{}, nerr.Wrap(nerr.Corruption, "backup.decodeHeader", "checksum", err)
	}
	wrapLen := int(encoding.U16(raw, 104))
	if wrapLen <= 0 || wrapLen > maxWrapBlob {
		return Header{}, nerr.New(nerr.InvalidFormat, "backup.decodeHeader", "invalid wrapped DEK length")
	}
	if headerFixedSize+wrapLen+4 != len(raw) {
		return Header{}, nerr.New(nerr.InvalidFormat, "backup.decodeHeader", "header length mismatch")
	}
	suite := format.CipherSuite(encoding.U16(raw, 8))
	if suite != format.CipherAES256GCM {
		return Header{}, nerr.New(nerr.Crypto, "backup.decodeHeader", "unsupported cipher suite")
	}
	h := Header{
		Version:     CurrentVersion,
		Flags:       encoding.U16(raw, 6),
		Suite:       suite,
		KeyVersion:  format.KeyVersion(encoding.U32(raw, 12)),
		Checkpoint:  format.LSN(encoding.U64(raw, 48)),
		RedoLSN:     format.LSN(encoding.U64(raw, 56)),
		DurableLSN:  format.LSN(encoding.U64(raw, 64)),
		CreatedNano: int64(encoding.U64(raw, 72)),
		NonceHigh:   encoding.U64(raw, 96),
		WrappedDEK:  append([]byte(nil), raw[headerFixedSize:headerFixedSize+wrapLen]...),
	}
	copy(h.Identity.Database[:], raw[16:32])
	copy(h.Identity.File[:], raw[32:48])
	copy(h.BackupID[:], raw[80:96])
	if h.KeyVersion == 0 {
		return Header{}, nerr.New(nerr.InvalidFormat, "backup.decodeHeader", "invalid key version")
	}
	return h, nil
}

func encodeManifest(m Manifest) ([]byte, error) {
	if len(m.Members) > maxMembers {
		return nil, nerr.New(nerr.InvalidFormat, "backup.encodeManifest", "too many members")
	}
	n := 4 + 2 + 2
	for _, mem := range m.Members {
		if len(mem.Name) == 0 || len(mem.Name) > maxNameLen {
			return nil, nerr.New(nerr.InvalidFormat, "backup.encodeManifest", "invalid member name")
		}
		if !mem.Kind.known() {
			return nil, nerr.New(nerr.InvalidFormat, "backup.encodeManifest", "unknown member kind")
		}
		n += 1 + 2 + len(mem.Name) + 8 + 8 + 32 + 8 + 8
	}
	n += 4
	buf := make([]byte, n)
	encoding.PutU32(buf, 0, ManifestMagic)
	encoding.PutU16(buf, 4, CurrentVersion)
	encoding.PutU16(buf, 6, uint16(len(m.Members)))
	off := 8
	for _, mem := range m.Members {
		buf[off] = byte(mem.Kind)
		off++
		encoding.PutU16(buf, off, uint16(len(mem.Name)))
		off += 2
		copy(buf[off:], mem.Name)
		off += len(mem.Name)
		encoding.PutU64(buf, off, mem.PlainSize)
		off += 8
		encoding.PutU64(buf, off, mem.SealedSize)
		off += 8
		copy(buf[off:off+32], mem.SHA256[:])
		off += 32
		encoding.PutU64(buf, off, uint64(mem.FirstLSN))
		off += 8
		encoding.PutU64(buf, off, uint64(mem.LastLSN))
		off += 8
	}
	if off+4 != len(buf) {
		return nil, nerr.New(nerr.Internal, "backup.encodeManifest", "encoded length mismatch")
	}
	checksum.Write(buf, off)
	return buf, nil
}

func decodeManifest(raw []byte) (Manifest, error) {
	if len(raw) < 12 {
		return Manifest{}, nerr.New(nerr.InvalidFormat, "backup.decodeManifest", "truncated manifest")
	}
	if encoding.U32(raw, 0) != ManifestMagic {
		return Manifest{}, nerr.New(nerr.InvalidFormat, "backup.decodeManifest", "bad manifest magic")
	}
	if encoding.U16(raw, 4) != CurrentVersion {
		return Manifest{}, nerr.New(nerr.InvalidFormat, "backup.decodeManifest", "unsupported manifest version")
	}
	if err := checksum.Verify(raw, len(raw)-4); err != nil {
		return Manifest{}, nerr.Wrap(nerr.Corruption, "backup.decodeManifest", "checksum", err)
	}
	n := int(encoding.U16(raw, 6))
	if n < 0 || n > maxMembers {
		return Manifest{}, nerr.New(nerr.InvalidFormat, "backup.decodeManifest", "member count exceeds limit")
	}
	m := Manifest{Version: CurrentVersion, Members: make([]Member, 0, n)}
	off := 8
	end := len(raw) - 4
	for i := 0; i < n; i++ {
		if off >= end {
			return Manifest{}, nerr.New(nerr.InvalidFormat, "backup.decodeManifest", "truncated member")
		}
		k := Kind(raw[off])
		off++
		if !k.known() {
			return Manifest{}, nerr.New(nerr.InvalidFormat, "backup.decodeManifest", "unknown member kind")
		}
		nl, err := encoding.ReadU16(raw, off)
		if err != nil {
			return Manifest{}, nerr.New(nerr.InvalidFormat, "backup.decodeManifest", "truncated name length")
		}
		off += 2
		if int(nl) == 0 || int(nl) > maxNameLen {
			return Manifest{}, nerr.New(nerr.InvalidFormat, "backup.decodeManifest", "invalid member name")
		}
		name, err := encoding.ReadBytes(raw, off, int(nl))
		if err != nil {
			return Manifest{}, nerr.New(nerr.InvalidFormat, "backup.decodeManifest", "truncated member name")
		}
		off += int(nl)
		plain, err := encoding.ReadU64(raw, off)
		if err != nil {
			return Manifest{}, nerr.New(nerr.InvalidFormat, "backup.decodeManifest", "truncated plain size")
		}
		off += 8
		sealed, err := encoding.ReadU64(raw, off)
		if err != nil {
			return Manifest{}, nerr.New(nerr.InvalidFormat, "backup.decodeManifest", "truncated sealed size")
		}
		off += 8
		sum, err := encoding.ReadBytes(raw, off, 32)
		if err != nil {
			return Manifest{}, nerr.New(nerr.InvalidFormat, "backup.decodeManifest", "truncated checksum")
		}
		off += 32
		first, err := encoding.ReadU64(raw, off)
		if err != nil {
			return Manifest{}, nerr.New(nerr.InvalidFormat, "backup.decodeManifest", "truncated first LSN")
		}
		off += 8
		last, err := encoding.ReadU64(raw, off)
		if err != nil {
			return Manifest{}, nerr.New(nerr.InvalidFormat, "backup.decodeManifest", "truncated last LSN")
		}
		off += 8
		mem := Member{
			Kind:       k,
			Name:       string(name),
			PlainSize:  plain,
			SealedSize: sealed,
			FirstLSN:   format.LSN(first),
			LastLSN:    format.LSN(last),
		}
		copy(mem.SHA256[:], sum)
		m.Members = append(m.Members, mem)
	}
	if off != end {
		return Manifest{}, nerr.New(nerr.InvalidFormat, "backup.decodeManifest", "trailing manifest bytes")
	}
	return m, nil
}
