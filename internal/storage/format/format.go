package format

import (
	"encoding/hex"

	"github.com/bzync/nextsql/internal/nerr"
)

// MagicBytes is the on-disk ASCII sentinel 'N','S','Q','L'.
var MagicBytes = [4]byte{'N', 'S', 'Q', 'L'}

func PutMagic(b []byte, off int) {
	copy(b[off:off+4], MagicBytes[:])
}

func HasMagic(b []byte, off int) bool {
	return off >= 0 && off+4 <= len(b) &&
		b[off] == 'N' && b[off+1] == 'S' && b[off+2] == 'Q' && b[off+3] == 'L'
}

const (
	// Magic is the little-endian uint32 value of MagicBytes.
	Magic uint32 = 0x4C51534E

	CurrentFormatVersion   uint16 = 1
	CurrentEnvelopeVersion uint16 = 1

	LogicalPageSize    = 16384
	PageHeaderSize     = 48
	SlotSize           = 4
	EnvelopeHeaderSize = 32
	AuthTagSize        = 16
	EnvelopePadSize    = 16
	PhysicalPageSize   = LogicalPageSize + EnvelopeHeaderSize + AuthTagSize + EnvelopePadSize // 16448

	SuperblockSize = 256
	ChecksumSize   = 4

	MaxRecordSize = LogicalPageSize - PageHeaderSize - SlotSize
)

// CipherSuite identifies an approved AEAD. Do not add unreviewed primitives.
type CipherSuite uint16

const (
	CipherInvalid   CipherSuite = 0
	CipherAES256GCM CipherSuite = 1
)

func (s CipherSuite) String() string {
	switch s {
	case CipherAES256GCM:
		return "AES-256-GCM"
	default:
		return "invalid"
	}
}

type PageType uint16

const (
	PageTypeInvalid       PageType = 0
	PageTypeSuperblock    PageType = 1
	PageTypeSlotted       PageType = 2
	PageTypeFree          PageType = 3
	PageTypeFreeList      PageType = 4
	PageTypeBTreeLeaf     PageType = 5
	PageTypeBTreeInternal PageType = 6
)

func (t PageType) Known() bool {
	switch t {
	case PageTypeSuperblock, PageTypeSlotted, PageTypeFree, PageTypeFreeList,
		PageTypeBTreeLeaf, PageTypeBTreeInternal:
		return true
	default:
		return false
	}
}

func (t PageType) String() string {
	switch t {
	case PageTypeSuperblock:
		return "superblock"
	case PageTypeSlotted:
		return "slotted"
	case PageTypeFree:
		return "free"
	case PageTypeFreeList:
		return "freelist"
	case PageTypeBTreeLeaf:
		return "btree_leaf"
	case PageTypeBTreeInternal:
		return "btree_internal"
	default:
		return "invalid"
	}
}

type PageID uint64

const (
	PageIDSuperblock PageID = 0
	FirstAllocPageID PageID = 1
)

func (id PageID) IsSuperblock() bool { return id == PageIDSuperblock }

func (id PageID) UserData() error {
	if id.IsSuperblock() {
		return nerr.New(nerr.InvalidArgument, "format.PageID", "page id 0 is reserved for the superblock")
	}
	return nil
}

type LSN uint64
type TxnID uint64
type UndoID uint64
type KeyVersion uint32

// Identity uniquely names a database and a file within it.
type Identity struct {
	Database [16]byte
	File     [16]byte
}

func (id Identity) DatabaseString() string { return hex.EncodeToString(id.Database[:]) }
func (id Identity) FileString() string     { return hex.EncodeToString(id.File[:]) }

func PhysicalOffset(id PageID) int64 {
	return int64(id) * int64(PhysicalPageSize)
}
