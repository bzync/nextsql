package xport

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/checksum"
	"github.com/bzync/nextsql/internal/storage/format"
)

const (
	// HeaderMagic is ASCII 'N','S','X','P'.
	HeaderMagic uint32 = 0x5058534E
	// PayloadMagic is ASCII 'N','S','X','L'.
	PayloadMagic uint32 = 0x4C58534E

	CurrentVersion uint16 = 1

	headerFixedSize = 160
	maxWrapBlob     = 1 << 16
	maxNameLen      = 255
	maxChunkSize    = 1 << 20
	maxTables       = 1 << 16
	maxRowBytes     = 16 << 20
	defaultChunk    = 1 << 20

	headerName    = "header"
	keystoreName  = "keystore"
	payloadName   = "payload"
	verifiedName  = "verified"
	partialSuffix = ".partial"
)

const (
	recTable byte = 1
	recRow   byte = 2
)

// Header is the plaintext export prologue. It holds identity and the
// wrapped export DEK. It never contains a raw root or DEK.
type Header struct {
	Version     uint16
	Flags       uint16
	Suite       format.CipherSuite
	KeyVersion  format.KeyVersion
	Identity    format.Identity
	CreatedNano int64
	ExportID    [16]byte
	NonceHigh   uint64
	TableCount  uint32
	RowCount    uint64
	PlainSize   uint64
	SealedSize  uint64
	PayloadSHA  [32]byte
	WrappedDEK  []byte
}

func encodeHeader(h Header) ([]byte, error) {
	if len(h.WrappedDEK) == 0 || len(h.WrappedDEK) > maxWrapBlob {
		return nil, nerr.New(nerr.InvalidFormat, "xport.encodeHeader", "invalid wrapped export DEK length")
	}
	buf := make([]byte, headerFixedSize+len(h.WrappedDEK)+4)
	encoding.PutU32(buf, 0, HeaderMagic)
	encoding.PutU16(buf, 4, CurrentVersion)
	encoding.PutU16(buf, 6, h.Flags)
	encoding.PutU16(buf, 8, uint16(h.Suite))
	encoding.PutU32(buf, 12, uint32(h.KeyVersion))
	copy(buf[16:32], h.Identity.Database[:])
	copy(buf[32:48], h.Identity.File[:])
	encoding.PutU64(buf, 48, uint64(h.CreatedNano))
	copy(buf[56:72], h.ExportID[:])
	encoding.PutU64(buf, 72, h.NonceHigh)
	encoding.PutU32(buf, 80, h.TableCount)
	encoding.PutU64(buf, 84, h.RowCount)
	encoding.PutU16(buf, 92, uint16(len(h.WrappedDEK)))
	encoding.PutU64(buf, 96, h.PlainSize)
	encoding.PutU64(buf, 104, h.SealedSize)
	copy(buf[112:144], h.PayloadSHA[:])
	copy(buf[headerFixedSize:], h.WrappedDEK)
	checksum.Write(buf, len(buf)-4)
	return buf, nil
}

func decodeHeader(raw []byte) (Header, error) {
	if len(raw) < headerFixedSize+4 {
		return Header{}, nerr.New(nerr.InvalidFormat, "xport.decodeHeader", "truncated header")
	}
	if encoding.U32(raw, 0) != HeaderMagic {
		return Header{}, nerr.New(nerr.InvalidFormat, "xport.decodeHeader", "bad export magic")
	}
	if encoding.U16(raw, 4) != CurrentVersion {
		return Header{}, nerr.New(nerr.InvalidFormat, "xport.decodeHeader", "unsupported export version")
	}
	if err := checksum.Verify(raw, len(raw)-4); err != nil {
		return Header{}, nerr.Wrap(nerr.Corruption, "xport.decodeHeader", "checksum", err)
	}
	wrapLen := int(encoding.U16(raw, 92))
	if wrapLen <= 0 || wrapLen > maxWrapBlob {
		return Header{}, nerr.New(nerr.InvalidFormat, "xport.decodeHeader", "invalid wrapped DEK length")
	}
	if headerFixedSize+wrapLen+4 != len(raw) {
		return Header{}, nerr.New(nerr.InvalidFormat, "xport.decodeHeader", "header length mismatch")
	}
	suite := format.CipherSuite(encoding.U16(raw, 8))
	if suite != format.CipherAES256GCM {
		return Header{}, nerr.New(nerr.Crypto, "xport.decodeHeader", "unsupported cipher suite")
	}
	h := Header{
		Version:     CurrentVersion,
		Flags:       encoding.U16(raw, 6),
		Suite:       suite,
		KeyVersion:  format.KeyVersion(encoding.U32(raw, 12)),
		CreatedNano: int64(encoding.U64(raw, 48)),
		NonceHigh:   encoding.U64(raw, 72),
		TableCount:  encoding.U32(raw, 80),
		RowCount:    encoding.U64(raw, 84),
		PlainSize:   encoding.U64(raw, 96),
		SealedSize:  encoding.U64(raw, 104),
		WrappedDEK:  append([]byte(nil), raw[headerFixedSize:headerFixedSize+wrapLen]...),
	}
	copy(h.Identity.Database[:], raw[16:32])
	copy(h.Identity.File[:], raw[32:48])
	copy(h.ExportID[:], raw[56:72])
	copy(h.PayloadSHA[:], raw[112:144])
	if h.KeyVersion == 0 {
		return Header{}, nerr.New(nerr.InvalidFormat, "xport.decodeHeader", "invalid key version")
	}
	return h, nil
}

func schemaForExport(t *catalog.Table) *catalog.Table {
	c := t.Clone()
	c.HeapMeta = 0
	c.VecMeta = 0
	for i := range c.Indexes {
		c.Indexes[i].Meta = 0
	}
	return c
}

func encodeTableRec(t *catalog.Table) ([]byte, error) {
	body, err := catalog.EncodeTable(schemaForExport(t))
	if err != nil {
		return nil, err
	}
	if len(t.Name) == 0 || len(t.Name) > maxNameLen {
		return nil, nerr.New(nerr.InvalidFormat, "xport.encodeTableRec", "invalid table name")
	}
	buf := make([]byte, 1+4+len(body))
	buf[0] = recTable
	encoding.PutU32(buf, 1, uint32(len(body)))
	copy(buf[5:], body)
	return buf, nil
}

func encodeRowRec(table string, row []types.Value) ([]byte, error) {
	if len(table) == 0 || len(table) > maxNameLen {
		return nil, nerr.New(nerr.InvalidFormat, "xport.encodeRowRec", "invalid table name")
	}
	inlined := inlineVectors(row)
	body, err := types.EncodeRow(inlined)
	if err != nil {
		return nil, err
	}
	if len(body) > maxRowBytes {
		return nil, nerr.New(nerr.InvalidArgument, "xport.encodeRowRec", "row exceeds export size limit")
	}
	buf := make([]byte, 1+2+len(table)+4+len(body))
	buf[0] = recRow
	encoding.PutU16(buf, 1, uint16(len(table)))
	copy(buf[3:], table)
	encoding.PutU32(buf, 3+len(table), uint32(len(body)))
	copy(buf[7+len(table):], body)
	return buf, nil
}

func inlineVectors(row []types.Value) []types.Value {
	out := make([]types.Value, len(row))
	for i, v := range row {
		v = v.Clone()
		if v.Typ.Kind == types.KindVector {
			v.VecRef = false
		}
		out[i] = v
	}
	return out
}

type tableDump struct {
	Table *catalog.Table
	Rows  [][]types.Value
}

func decodePayload(raw []byte) ([]tableDump, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var dumps []tableDump
	byName := make(map[string]int)
	off := 0
	for off < len(raw) {
		kind := raw[off]
		off++
		switch kind {
		case recTable:
			n, err := encoding.ReadU32(raw, off)
			if err != nil {
				return nil, nerr.New(nerr.InvalidFormat, "xport.decodePayload", "truncated table length")
			}
			off += 4
			if n == 0 || int(n) > maxRowBytes {
				return nil, nerr.New(nerr.InvalidFormat, "xport.decodePayload", "invalid table descriptor length")
			}
			body, err := encoding.ReadBytes(raw, off, int(n))
			if err != nil {
				return nil, nerr.New(nerr.InvalidFormat, "xport.decodePayload", "truncated table descriptor")
			}
			off += int(n)
			t, err := catalog.DecodeTable(body)
			if err != nil {
				return nil, err
			}
			t.HeapMeta = 0
			t.VecMeta = 0
			for i := range t.Indexes {
				t.Indexes[i].Meta = 0
			}
			if _, dup := byName[t.Name]; dup {
				return nil, nerr.New(nerr.InvalidFormat, "xport.decodePayload", "duplicate table")
			}
			if len(dumps) >= maxTables {
				return nil, nerr.New(nerr.InvalidFormat, "xport.decodePayload", "too many tables")
			}
			byName[t.Name] = len(dumps)
			dumps = append(dumps, tableDump{Table: t})
		case recRow:
			nl, err := encoding.ReadU16(raw, off)
			if err != nil {
				return nil, nerr.New(nerr.InvalidFormat, "xport.decodePayload", "truncated row table name")
			}
			off += 2
			if nl == 0 || int(nl) > maxNameLen {
				return nil, nerr.New(nerr.InvalidFormat, "xport.decodePayload", "invalid row table name")
			}
			nameb, err := encoding.ReadBytes(raw, off, int(nl))
			if err != nil {
				return nil, nerr.New(nerr.InvalidFormat, "xport.decodePayload", "truncated row table name")
			}
			off += int(nl)
			n, err := encoding.ReadU32(raw, off)
			if err != nil {
				return nil, nerr.New(nerr.InvalidFormat, "xport.decodePayload", "truncated row length")
			}
			off += 4
			if n == 0 || int(n) > maxRowBytes {
				return nil, nerr.New(nerr.InvalidFormat, "xport.decodePayload", "invalid row length")
			}
			body, err := encoding.ReadBytes(raw, off, int(n))
			if err != nil {
				return nil, nerr.New(nerr.InvalidFormat, "xport.decodePayload", "truncated row")
			}
			off += int(n)
			idx, ok := byName[string(nameb)]
			if !ok {
				return nil, nerr.New(nerr.InvalidFormat, "xport.decodePayload", "row for unknown table")
			}
			row, err := types.DecodeRow(body, dumps[idx].Table.Types())
			if err != nil {
				return nil, err
			}
			dumps[idx].Rows = append(dumps[idx].Rows, row)
		default:
			return nil, nerr.New(nerr.InvalidFormat, "xport.decodePayload", "unknown record type")
		}
	}
	return dumps, nil
}
