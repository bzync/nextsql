package wal

import (
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/checksum"
	"github.com/bzync/nextsql/internal/storage/format"
)

const (
	// Magic is ASCII 'N','S','W','L'.
	Magic uint32 = 0x4C57534E

	CurrentVersion uint16 = 1

	// HeaderSize is the physical record header before ciphertext.
	HeaderSize = 40

	payloadHeaderSize = 28
)

// RecType identifies a logical WAL record.
type RecType uint16

const (
	RecInvalid    RecType = 0
	RecBegin      RecType = 1
	RecInsert     RecType = 2
	RecDelete     RecType = 3
	RecUpdate     RecType = 4
	RecPageImage  RecType = 5
	RecCommit     RecType = 6
	RecAbort      RecType = 7
	RecCheckpoint RecType = 8
	RecTreeMeta   RecType = 9
	RecAllocState RecType = 10
	RecUndo       RecType = 11
	// RecChange is a versioned logical SQL-row change staged by the executor
	// and appended contiguously immediately before its transaction COMMIT.
	// Recovery ignores it; CDC consumes it only after the matching COMMIT is
	// durable.
	RecChange RecType = 12
)

func (t RecType) String() string {
	switch t {
	case RecBegin:
		return "begin"
	case RecInsert:
		return "insert"
	case RecDelete:
		return "delete"
	case RecUpdate:
		return "update"
	case RecPageImage:
		return "page_image"
	case RecCommit:
		return "commit"
	case RecAbort:
		return "abort"
	case RecCheckpoint:
		return "checkpoint"
	case RecTreeMeta:
		return "tree_meta"
	case RecAllocState:
		return "alloc_state"
	case RecUndo:
		return "undo"
	case RecChange:
		return "change"
	default:
		return "invalid"
	}
}

func (t RecType) known() bool {
	return t >= RecBegin && t <= RecChange
}

// Record is one WAL entry after decryption.
type Record struct {
	Type    RecType
	Flags   uint16
	LSN     format.LSN
	TxnID   format.TxnID
	PrevLSN format.LSN
	PageID  format.PageID
	Body    []byte
}

func encodePayload(r Record) []byte {
	return encodePayloadInto(nil, r)
}

func encodePayloadInto(buf []byte, r Record) []byte {
	n := payloadHeaderSize + len(r.Body)
	if cap(buf) < n {
		buf = make([]byte, n)
	} else {
		buf = buf[:n]
	}
	encoding.PutU16(buf, 0, uint16(r.Type))
	encoding.PutU16(buf, 2, r.Flags)
	encoding.PutU64(buf, 4, uint64(r.TxnID))
	encoding.PutU64(buf, 12, uint64(r.PrevLSN))
	encoding.PutU64(buf, 20, uint64(r.PageID))
	copy(buf[payloadHeaderSize:], r.Body)
	return buf
}

func decodePayload(lsn format.LSN, payload []byte) (Record, error) {
	if len(payload) < payloadHeaderSize {
		return Record{}, nerr.New(nerr.InvalidFormat, "wal.decodePayload", "truncated payload")
	}
	r := Record{
		Type:    RecType(encoding.U16(payload, 0)),
		Flags:   encoding.U16(payload, 2),
		LSN:     lsn,
		TxnID:   format.TxnID(encoding.U64(payload, 4)),
		PrevLSN: format.LSN(encoding.U64(payload, 12)),
		PageID:  format.PageID(encoding.U64(payload, 20)),
	}
	if !r.Type.known() {
		return Record{}, nerr.New(nerr.InvalidFormat, "wal.decodePayload", "unknown record type")
	}
	if n := len(payload) - payloadHeaderSize; n > 0 {
		r.Body = append([]byte(nil), payload[payloadHeaderSize:]...)
	}
	return r, nil
}

// encodePhysical seals payload under the WAL DEK.
func encodePhysical(dek *crypto.DEK, lsn format.LSN, generation uint64, payload []byte) ([]byte, error) {
	return encodePhysicalInto(dek, lsn, generation, payload, nil)
}

func encodePhysicalInto(dek *crypto.DEK, lsn format.LSN, generation uint64, payload, buf []byte) ([]byte, error) {
	if dek == nil {
		return nil, nerr.New(nerr.InvalidArgument, "wal.encodePhysical", "nil WAL DEK")
	}
	if lsn == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "wal.encodePhysical", "LSN 0 is reserved")
	}
	need := HeaderSize + len(payload) + format.AuthTagSize
	if cap(buf) < need {
		buf = make([]byte, need)
	} else {
		buf = buf[:need]
	}
	hdr := buf[:HeaderSize]
	encoding.PutU32(hdr, 0, Magic)
	encoding.PutU16(hdr, 4, CurrentVersion)
	encoding.PutU16(hdr, 6, uint16(dek.Suite))
	encoding.PutU32(hdr, 8, uint32(dek.Version))
	encoding.PutU64(hdr, 12, uint64(lsn))

	// AAD is magic through LSN (20 bytes). Ciphertext length is CRC'd in the header.
	var aad [20]byte
	copy(aad[:], hdr[:20])
	nonce, ct, err := crypto.SealBytesInto(dek, generation, aad[:], payload, buf[HeaderSize:HeaderSize])
	if err != nil {
		return nil, err
	}
	if HeaderSize+len(ct) > cap(buf) {
		nbuf := make([]byte, HeaderSize+len(ct))
		copy(nbuf, hdr)
		copy(nbuf[HeaderSize:], ct)
		buf = nbuf
		hdr = buf[:HeaderSize]
	} else {
		if len(ct) > 0 && (len(buf) <= HeaderSize || &ct[0] != &buf[HeaderSize]) {
			copy(buf[HeaderSize:], ct)
		}
		buf = buf[:HeaderSize+len(ct)]
		hdr = buf[:HeaderSize]
	}
	encoding.PutU32(hdr, 20, uint32(len(ct)))
	copy(hdr[24:36], nonce)
	checksum.Write(hdr, 36)
	return buf, nil
}

type physicalHeader struct {
	Version    uint16
	Suite      format.CipherSuite
	KeyVersion format.KeyVersion
	LSN        format.LSN
	CTLen      int
}

func parseHeader(hdr []byte) (physicalHeader, error) {
	if len(hdr) < HeaderSize {
		return physicalHeader{}, nerr.New(nerr.InvalidFormat, "wal.parseHeader", "truncated header")
	}
	if encoding.U32(hdr, 0) != Magic {
		return physicalHeader{}, nerr.New(nerr.InvalidFormat, "wal.parseHeader", "bad WAL magic")
	}
	if err := checksum.Verify(hdr[:HeaderSize], 36); err != nil {
		return physicalHeader{}, nerr.Wrap(nerr.Corruption, "wal.parseHeader", "checksum", err)
	}
	ver := encoding.U16(hdr, 4)
	if ver != CurrentVersion {
		return physicalHeader{}, nerr.New(nerr.InvalidFormat, "wal.parseHeader", "unsupported WAL record version")
	}
	h := physicalHeader{
		Version:    ver,
		Suite:      format.CipherSuite(encoding.U16(hdr, 6)),
		KeyVersion: format.KeyVersion(encoding.U32(hdr, 8)),
		LSN:        format.LSN(encoding.U64(hdr, 12)),
		CTLen:      int(encoding.U32(hdr, 20)),
	}
	if h.Suite != format.CipherAES256GCM {
		return physicalHeader{}, nerr.New(nerr.Crypto, "wal.parseHeader", "unsupported cipher suite")
	}
	if h.LSN == 0 {
		return physicalHeader{}, nerr.New(nerr.Corruption, "wal.parseHeader", "LSN 0 is reserved")
	}
	if h.CTLen < format.AuthTagSize || h.CTLen > format.LogicalPageSize+payloadHeaderSize+format.AuthTagSize+256 {
		return physicalHeader{}, nerr.New(nerr.InvalidFormat, "wal.parseHeader", "invalid ciphertext length")
	}
	return h, nil
}

func decodePhysical(keys crypto.KeyProvider, hdr, ct []byte) (Record, error) {
	h, err := parseHeader(hdr)
	if err != nil {
		return Record{}, err
	}
	if len(ct) != h.CTLen {
		return Record{}, nerr.New(nerr.InvalidFormat, "wal.decodePhysical", "ciphertext length mismatch")
	}
	dek, err := keys.Key(h.KeyVersion)
	if err != nil {
		return Record{}, err
	}
	if dek.Suite != h.Suite {
		return Record{}, nerr.New(nerr.Crypto, "wal.decodePhysical", "key suite does not match record")
	}
	aad := append([]byte(nil), hdr[:20]...)
	payload, err := crypto.OpenBytes(dek, hdr[24:36], aad, ct)
	if err != nil {
		return Record{}, err
	}
	return decodePayload(h.LSN, payload)
}

func encodeInsertBody(key, value []byte) []byte {
	buf := make([]byte, 4+len(key)+len(value))
	encoding.PutU16(buf, 0, uint16(len(key)))
	encoding.PutU16(buf, 2, uint16(len(value)))
	copy(buf[4:], key)
	copy(buf[4+len(key):], value)
	return buf
}

func encodeDeleteBody(key []byte) []byte {
	buf := make([]byte, 2+len(key))
	encoding.PutU16(buf, 0, uint16(len(key)))
	copy(buf[2:], key)
	return buf
}

func encodeTreeMeta(root format.PageID, height uint16) []byte {
	buf := make([]byte, 10)
	encoding.PutU64(buf, 0, uint64(root))
	encoding.PutU16(buf, 8, height)
	return buf
}

func decodeTreeMeta(body []byte) (format.PageID, uint16, error) {
	if len(body) < 10 {
		return 0, 0, nerr.New(nerr.InvalidFormat, "wal.decodeTreeMeta", "truncated tree meta")
	}
	return format.PageID(encoding.U64(body, 0)), encoding.U16(body, 8), nil
}

func encodeAllocState(next, head format.PageID, count uint64) []byte {
	buf := make([]byte, 24)
	encoding.PutU64(buf, 0, uint64(next))
	encoding.PutU64(buf, 8, uint64(head))
	encoding.PutU64(buf, 16, count)
	return buf
}

func decodeAllocState(body []byte) (next, head format.PageID, count uint64, err error) {
	if len(body) < 24 {
		return 0, 0, 0, nerr.New(nerr.InvalidFormat, "wal.decodeAllocState", "truncated alloc state")
	}
	return format.PageID(encoding.U64(body, 0)), format.PageID(encoding.U64(body, 8)), encoding.U64(body, 16), nil
}

// CheckpointBody is stored in RecCheckpoint records and the control file.
type CheckpointBody struct {
	RedoLSN    format.LSN
	DurableLSN format.LSN
	NextPageID format.PageID
	FreeHead   format.PageID
	FreeCount  uint64
	Root       format.PageID
	Height     uint16
}

func encodeCheckpoint(c CheckpointBody) []byte {
	buf := make([]byte, 50)
	encoding.PutU64(buf, 0, uint64(c.RedoLSN))
	encoding.PutU64(buf, 8, uint64(c.DurableLSN))
	encoding.PutU64(buf, 16, uint64(c.NextPageID))
	encoding.PutU64(buf, 24, uint64(c.FreeHead))
	encoding.PutU64(buf, 32, c.FreeCount)
	encoding.PutU64(buf, 40, uint64(c.Root))
	encoding.PutU16(buf, 48, c.Height)
	return buf
}

func decodeCheckpoint(body []byte) (CheckpointBody, error) {
	if len(body) < 50 {
		return CheckpointBody{}, nerr.New(nerr.InvalidFormat, "wal.decodeCheckpoint", "truncated checkpoint")
	}
	return CheckpointBody{
		RedoLSN:    format.LSN(encoding.U64(body, 0)),
		DurableLSN: format.LSN(encoding.U64(body, 8)),
		NextPageID: format.PageID(encoding.U64(body, 16)),
		FreeHead:   format.PageID(encoding.U64(body, 24)),
		FreeCount:  encoding.U64(body, 32),
		Root:       format.PageID(encoding.U64(body, 40)),
		Height:     encoding.U16(body, 48),
	}, nil
}
