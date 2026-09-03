package file

import (
	"time"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/checksum"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/upgrade/compat"
)

const (
	sbOffMagic         = 0
	sbOffVersion       = 4
	sbOffFlags         = 6
	sbOffDBUUID        = 8
	sbOffFileUUID      = 24
	sbOffLogical       = 40
	sbOffPhysical      = 44
	sbOffCipher        = 48
	sbOffEnvelope      = 50
	sbOffKeyVersion    = 52
	sbOffNextPage      = 56
	sbOffFreeHead      = 64
	sbOffFreeCount     = 72
	sbOffNextNonce     = 80
	sbOffCreated       = 88
	sbOffWrapLen       = 96
	sbOffAuth          = 100
	sbOffCheckpointLSN = 116
	sbOffRedoLSN       = 124
	sbOffPrimaryRoot   = 236
	sbOffPrimaryHeight = 244
	sbOffPrimaryFlags  = 246
	sbOffChecksum      = 252
)

const nonceBatch = 65536

type Superblock struct {
	FormatVersion       uint16
	Flags               uint16
	Identity            format.Identity
	LogicalPageSize     uint32
	PhysicalPageSize    uint32
	CipherSuite         format.CipherSuite
	EnvelopeVersion     uint16
	KeyVersion          format.KeyVersion
	NextPageID          format.PageID
	FreeListHead        format.PageID
	FreeCount           uint64
	NextNonceGeneration uint64
	CreatedUnixNano     int64
	CheckpointLSN       format.LSN
	RedoLSN             format.LSN
	PrimaryRoot         format.PageID
	PrimaryHeight       uint16
	PrimaryFlags        uint16
}

func newSuperblock(id format.Identity, keyVer format.KeyVersion) Superblock {
	return Superblock{
		FormatVersion:       format.CurrentFormatVersion,
		Identity:            id,
		LogicalPageSize:     format.LogicalPageSize,
		PhysicalPageSize:    format.PhysicalPageSize,
		CipherSuite:         format.CipherAES256GCM,
		EnvelopeVersion:     format.CurrentEnvelopeVersion,
		KeyVersion:          keyVer,
		NextPageID:          format.FirstAllocPageID,
		NextNonceGeneration: nonceBatch,
		CreatedUnixNano:     time.Now().UnixNano(),
	}
}

func encodeSuperblock(sb Superblock, auth []byte) []byte {
	buf := make([]byte, format.PhysicalPageSize)
	format.PutMagic(buf, sbOffMagic)
	encoding.PutU16(buf, sbOffVersion, sb.FormatVersion)
	encoding.PutU16(buf, sbOffFlags, sb.Flags)
	copy(buf[sbOffDBUUID:sbOffDBUUID+16], sb.Identity.Database[:])
	copy(buf[sbOffFileUUID:sbOffFileUUID+16], sb.Identity.File[:])
	encoding.PutU32(buf, sbOffLogical, sb.LogicalPageSize)
	encoding.PutU32(buf, sbOffPhysical, sb.PhysicalPageSize)
	encoding.PutU16(buf, sbOffCipher, uint16(sb.CipherSuite))
	encoding.PutU16(buf, sbOffEnvelope, sb.EnvelopeVersion)
	encoding.PutU32(buf, sbOffKeyVersion, uint32(sb.KeyVersion))
	encoding.PutU64(buf, sbOffNextPage, uint64(sb.NextPageID))
	encoding.PutU64(buf, sbOffFreeHead, uint64(sb.FreeListHead))
	encoding.PutU64(buf, sbOffFreeCount, sb.FreeCount)
	encoding.PutU64(buf, sbOffNextNonce, sb.NextNonceGeneration)
	encoding.PutU64(buf, sbOffCreated, uint64(sb.CreatedUnixNano))
	encoding.PutU64(buf, sbOffCheckpointLSN, uint64(sb.CheckpointLSN))
	encoding.PutU64(buf, sbOffRedoLSN, uint64(sb.RedoLSN))
	encoding.PutU64(buf, sbOffPrimaryRoot, uint64(sb.PrimaryRoot))
	encoding.PutU16(buf, sbOffPrimaryHeight, sb.PrimaryHeight)
	encoding.PutU16(buf, sbOffPrimaryFlags, sb.PrimaryFlags)
	copy(buf[sbOffAuth:sbOffAuth+16], auth)
	checksum.Write(buf[:format.SuperblockSize], sbOffChecksum)
	return buf
}

func decodeSuperblock(buf []byte) (Superblock, []byte, error) {
	if len(buf) < format.SuperblockSize {
		return Superblock{}, nil, nerr.New(nerr.InvalidFormat, "file.decodeSuperblock", "truncated superblock")
	}
	if !format.HasMagic(buf, sbOffMagic) {
		return Superblock{}, nil, nerr.New(nerr.InvalidFormat, "file.decodeSuperblock", "bad file magic")
	}
	if err := checksum.Verify(buf[:format.SuperblockSize], sbOffChecksum); err != nil {
		return Superblock{}, nil, nerr.Wrap(nerr.Corruption, "file.decodeSuperblock", "checksum", err)
	}
	ver := encoding.U16(buf, sbOffVersion)
	if err := compat.Check(compat.FamilyPage, ver); err != nil {
		return Superblock{}, nil, nerr.Wrap(nerr.InvalidFormat, "file.decodeSuperblock", "unsupported database format version", err)
	}
	sb := Superblock{
		FormatVersion:       ver,
		Flags:               encoding.U16(buf, sbOffFlags),
		LogicalPageSize:     encoding.U32(buf, sbOffLogical),
		PhysicalPageSize:    encoding.U32(buf, sbOffPhysical),
		CipherSuite:         format.CipherSuite(encoding.U16(buf, sbOffCipher)),
		EnvelopeVersion:     encoding.U16(buf, sbOffEnvelope),
		KeyVersion:          format.KeyVersion(encoding.U32(buf, sbOffKeyVersion)),
		NextPageID:          format.PageID(encoding.U64(buf, sbOffNextPage)),
		FreeListHead:        format.PageID(encoding.U64(buf, sbOffFreeHead)),
		FreeCount:           encoding.U64(buf, sbOffFreeCount),
		NextNonceGeneration: encoding.U64(buf, sbOffNextNonce),
		CreatedUnixNano:     int64(encoding.U64(buf, sbOffCreated)),
		CheckpointLSN:       format.LSN(encoding.U64(buf, sbOffCheckpointLSN)),
		RedoLSN:             format.LSN(encoding.U64(buf, sbOffRedoLSN)),
		PrimaryRoot:         format.PageID(encoding.U64(buf, sbOffPrimaryRoot)),
		PrimaryHeight:       encoding.U16(buf, sbOffPrimaryHeight),
		PrimaryFlags:        encoding.U16(buf, sbOffPrimaryFlags),
	}
	copy(sb.Identity.Database[:], buf[sbOffDBUUID:sbOffDBUUID+16])
	copy(sb.Identity.File[:], buf[sbOffFileUUID:sbOffFileUUID+16])
	if sb.LogicalPageSize != format.LogicalPageSize || sb.PhysicalPageSize != format.PhysicalPageSize {
		return Superblock{}, nil, nerr.New(nerr.InvalidFormat, "file.decodeSuperblock", "unexpected page size")
	}
	if sb.CipherSuite != format.CipherAES256GCM {
		return Superblock{}, nil, nerr.New(nerr.Crypto, "file.decodeSuperblock", "unsupported cipher suite")
	}
	if sb.EnvelopeVersion != format.CurrentEnvelopeVersion {
		return Superblock{}, nil, nerr.New(nerr.InvalidFormat, "file.decodeSuperblock", "unsupported envelope version")
	}
	if sb.NextPageID < format.FirstAllocPageID {
		return Superblock{}, nil, nerr.New(nerr.Corruption, "file.decodeSuperblock", "invalid next page id")
	}
	if sb.NextNonceGeneration == 0 {
		return Superblock{}, nil, nerr.New(nerr.Corruption, "file.decodeSuperblock", "invalid nonce generation")
	}
	if err := validatePrimaryTree(sb.PrimaryRoot, sb.PrimaryHeight, sb.NextPageID); err != nil {
		return Superblock{}, nil, err
	}
	auth := make([]byte, 16)
	copy(auth, buf[sbOffAuth:sbOffAuth+16])
	return sb, auth, nil
}

func validatePrimaryTree(root format.PageID, height uint16, next format.PageID) error {
	if root == 0 {
		if height != 0 {
			return nerr.New(nerr.Corruption, "file.decodeSuperblock", "primary tree height without root")
		}
		return nil
	}
	if err := root.UserData(); err != nil {
		return nerr.Wrap(nerr.Corruption, "file.decodeSuperblock", "primary tree root", err)
	}
	if height == 0 {
		return nerr.New(nerr.Corruption, "file.decodeSuperblock", "primary tree root without height")
	}
	if root >= next {
		return nerr.New(nerr.Corruption, "file.decodeSuperblock", "primary tree root is not allocated")
	}
	return nil
}
