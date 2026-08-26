package undo

import (
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/row"
)

const (
	// Magic is ASCII 'N','S','U','D'.
	Magic uint32 = 0x4455534E

	CurrentVersion uint16 = 1
	HeaderSize            = 40
)

// Kind classifies an undo record.
type Kind uint8

const (
	KindInvalid Kind = 0
	KindInsert  Kind = 1
	KindUpdate  Kind = 2
	KindDelete  Kind = 3
)

// Record is one undo version: the previous row for a key.
type Record struct {
	ID     format.UndoID
	Txn    format.TxnID
	Prev   format.UndoID
	Kind   Kind
	PageID format.PageID
	Key    []byte
	Old    row.Version
}

func encodeRecord(r Record) []byte {
	buf := make([]byte, 53+len(r.Key)+len(r.Old.Payload))
	encoding.PutU64(buf, 0, uint64(r.Txn))
	encoding.PutU64(buf, 8, uint64(r.Prev))
	buf[16] = byte(r.Kind)
	encoding.PutU64(buf, 17, uint64(r.PageID))
	encoding.PutU16(buf, 25, uint16(len(r.Key)))
	encoding.PutU16(buf, 27, uint16(len(r.Old.Payload)))
	encoding.PutU64(buf, 29, uint64(r.Old.Xmin))
	encoding.PutU64(buf, 37, uint64(r.Old.Xmax))
	encoding.PutU64(buf, 45, uint64(r.Old.Undo))
	copy(buf[53:], r.Key)
	copy(buf[53+len(r.Key):], r.Old.Payload)
	return buf
}

func decodeRecord(id format.UndoID, payload []byte) (Record, error) {
	if len(payload) < 53 {
		return Record{}, nerr.New(nerr.InvalidFormat, "undo.decodeRecord", "truncated undo payload")
	}
	klen := int(encoding.U16(payload, 25))
	vlen := int(encoding.U16(payload, 27))
	if klen < 0 || vlen < 0 || 53+klen+vlen != len(payload) {
		return Record{}, nerr.New(nerr.InvalidFormat, "undo.decodeRecord", "invalid undo payload length")
	}
	kind := Kind(payload[16])
	if kind < KindInsert || kind > KindDelete {
		return Record{}, nerr.New(nerr.InvalidFormat, "undo.decodeRecord", "unknown undo kind")
	}
	return Record{
		ID:     id,
		Txn:    format.TxnID(encoding.U64(payload, 0)),
		Prev:   format.UndoID(encoding.U64(payload, 8)),
		Kind:   kind,
		PageID: format.PageID(encoding.U64(payload, 17)),
		Key:    append([]byte(nil), payload[53:53+klen]...),
		Old: row.Version{
			Xmin:    format.TxnID(encoding.U64(payload, 29)),
			Xmax:    format.TxnID(encoding.U64(payload, 37)),
			Undo:    format.UndoID(encoding.U64(payload, 45)),
			Payload: append([]byte(nil), payload[53+klen:]...),
		},
	}, nil
}
