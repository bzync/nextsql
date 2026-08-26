// Package replication is the Phase 15 HA layer: hashicorp/raft plus
// encrypted WAL-record replication. Consensus is not invented here.
package replication

import (
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/wal"
)

const (
	// Magic is ASCII 'N','S','R','L' — replication log command.
	Magic uint32 = 0x4C52534E

	CurrentVersion uint16 = 1

	cmdHeaderSize = 4 + 2 + 2 + 4 // magic, version, kind, count

	KindWALBatch uint16 = 1
)

// EncodeCommand seals a WAL-record batch under the replication DEK.
func EncodeCommand(dek *crypto.DEK, recs []wal.Record) ([]byte, error) {
	if dek == nil {
		return nil, nerr.New(nerr.InvalidArgument, "replication.EncodeCommand", "nil replication DEK")
	}
	if len(recs) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "replication.EncodeCommand", "empty record batch")
	}
	if len(recs) > 1<<20 {
		return nil, nerr.New(nerr.InvalidArgument, "replication.EncodeCommand", "record batch too large")
	}
	plain := marshalBatch(recs)
	aad := make([]byte, 6)
	encoding.PutU32(aad, 0, Magic)
	encoding.PutU16(aad, 4, CurrentVersion)
	nonce, ct, err := crypto.SealBytesRandom(dek, aad, plain)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 4+2+2+4+12+4+len(ct))
	encoding.PutU32(out, 0, Magic)
	encoding.PutU16(out, 4, CurrentVersion)
	encoding.PutU16(out, 6, uint16(dek.Suite))
	encoding.PutU32(out, 8, uint32(dek.Version))
	copy(out[12:24], nonce)
	encoding.PutU32(out, 24, uint32(len(ct)))
	copy(out[28:], ct)
	return out, nil
}

// DecodeCommand opens a sealed command. Malformed or tampered input fails closed.
func DecodeCommand(keys crypto.KeyProvider, data []byte) ([]wal.Record, error) {
	if keys == nil {
		return nil, nerr.New(nerr.InvalidArgument, "replication.DecodeCommand", "nil key provider")
	}
	if len(data) < 28 {
		return nil, nerr.New(nerr.InvalidFormat, "replication.DecodeCommand", "truncated command")
	}
	if encoding.U32(data, 0) != Magic {
		return nil, nerr.New(nerr.InvalidFormat, "replication.DecodeCommand", "bad magic")
	}
	ver := encoding.U16(data, 4)
	if ver != CurrentVersion {
		return nil, nerr.New(nerr.InvalidFormat, "replication.DecodeCommand", "unsupported command version")
	}
	suite := format.CipherSuite(encoding.U16(data, 6))
	if suite != format.CipherAES256GCM {
		return nil, nerr.New(nerr.InvalidFormat, "replication.DecodeCommand", "unsupported cipher suite")
	}
	keyVer := format.KeyVersion(encoding.U32(data, 8))
	dek, err := keys.Key(keyVer)
	if err != nil {
		return nil, err
	}
	nonce := data[12:24]
	ctLen := encoding.U32(data, 24)
	if int(ctLen) != len(data)-28 || ctLen > 64<<20 {
		return nil, nerr.New(nerr.InvalidFormat, "replication.DecodeCommand", "ciphertext length mismatch")
	}
	aad := append([]byte(nil), data[:6]...)
	plain, err := crypto.OpenBytes(dek, nonce, aad, data[28:])
	if err != nil {
		return nil, err
	}
	return unmarshalBatch(plain)
}

func marshalBatch(recs []wal.Record) []byte {
	n := cmdHeaderSize
	for _, r := range recs {
		n += 2 + 2 + 8 + 8 + 8 + 8 + 4 + len(r.Body)
	}
	buf := make([]byte, n)
	encoding.PutU32(buf, 0, Magic)
	encoding.PutU16(buf, 4, CurrentVersion)
	encoding.PutU16(buf, 6, KindWALBatch)
	encoding.PutU32(buf, 8, uint32(len(recs)))
	off := cmdHeaderSize
	for _, r := range recs {
		encoding.PutU16(buf, off, uint16(r.Type))
		encoding.PutU16(buf, off+2, r.Flags)
		encoding.PutU64(buf, off+4, uint64(r.LSN))
		encoding.PutU64(buf, off+12, uint64(r.TxnID))
		encoding.PutU64(buf, off+20, uint64(r.PrevLSN))
		encoding.PutU64(buf, off+28, uint64(r.PageID))
		encoding.PutU32(buf, off+36, uint32(len(r.Body)))
		copy(buf[off+40:], r.Body)
		off += 40 + len(r.Body)
	}
	return buf
}

func unmarshalBatch(plain []byte) ([]wal.Record, error) {
	if len(plain) < cmdHeaderSize {
		return nil, nerr.New(nerr.InvalidFormat, "replication.DecodeCommand", "truncated batch")
	}
	if encoding.U32(plain, 0) != Magic {
		return nil, nerr.New(nerr.InvalidFormat, "replication.DecodeCommand", "bad batch magic")
	}
	if encoding.U16(plain, 4) != CurrentVersion {
		return nil, nerr.New(nerr.InvalidFormat, "replication.DecodeCommand", "unsupported batch version")
	}
	if encoding.U16(plain, 6) != KindWALBatch {
		return nil, nerr.New(nerr.InvalidFormat, "replication.DecodeCommand", "unknown command kind")
	}
	n := encoding.U32(plain, 8)
	if n == 0 || n > 1<<20 {
		return nil, nerr.New(nerr.InvalidFormat, "replication.DecodeCommand", "invalid record count")
	}
	out := make([]wal.Record, 0, n)
	off := cmdHeaderSize
	for i := uint32(0); i < n; i++ {
		if off+40 > len(plain) {
			return nil, nerr.New(nerr.InvalidFormat, "replication.DecodeCommand", "truncated record")
		}
		bodyLen := encoding.U32(plain, off+36)
		if bodyLen > 16<<20 || off+40+int(bodyLen) > len(plain) {
			return nil, nerr.New(nerr.InvalidFormat, "replication.DecodeCommand", "record body length")
		}
		r := wal.Record{
			Type:    wal.RecType(encoding.U16(plain, off)),
			Flags:   encoding.U16(plain, off+2),
			LSN:     format.LSN(encoding.U64(plain, off+4)),
			TxnID:   format.TxnID(encoding.U64(plain, off+12)),
			PrevLSN: format.LSN(encoding.U64(plain, off+20)),
			PageID:  format.PageID(encoding.U64(plain, off+28)),
		}
		if bodyLen > 0 {
			r.Body = append([]byte(nil), plain[off+40:off+40+int(bodyLen)]...)
		}
		out = append(out, r)
		off += 40 + int(bodyLen)
	}
	if off != len(plain) {
		return nil, nerr.New(nerr.InvalidFormat, "replication.DecodeCommand", "trailing batch bytes")
	}
	return out, nil
}
