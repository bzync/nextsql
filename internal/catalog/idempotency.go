package catalog

import (
	"bytes"
	"encoding/binary"

	"github.com/bzync/nextsql/internal/nerr"
)

const (
	KeyIdempotency byte = 'I'

	idempotencyMagic   = "NSID"
	idempotencyVersion = 1

	MaxIdempotencyRecords  = 1024
	MaxIdempotencyResponse = 256 << 10
)

// IdempotencyRecord is the durable retry fence for one scoped, hashed key.
// Raw user keys are never persisted. Response is an executor-owned, versioned
// result encoding so the catalog package stays independent of execution.
type IdempotencyRecord struct {
	RequestHash [32]byte
	CreatedNS   int64
	ExpiresNS   int64
	Response    []byte
}

func IdempotencyKey(scopeHash [32]byte) []byte {
	key := make([]byte, 1+len(scopeHash))
	key[0] = KeyIdempotency
	copy(key[1:], scopeHash[:])
	return key
}

func IdempotencyBounds() (start, end []byte) {
	return []byte{KeyIdempotency}, []byte{KeyIdempotency + 1}
}

func EncodeIdempotency(record IdempotencyRecord) ([]byte, error) {
	if record.CreatedNS <= 0 || record.ExpiresNS <= record.CreatedNS {
		return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodeIdempotency", "invalid idempotency retention")
	}
	if len(record.Response) == 0 || len(record.Response) > MaxIdempotencyResponse {
		return nil, nerr.New(nerr.Exhausted, "catalog.EncodeIdempotency", "idempotency response exceeds limit")
	}
	out := make([]byte, 4+1+32+8+8+4+len(record.Response))
	copy(out[:4], idempotencyMagic)
	out[4] = idempotencyVersion
	copy(out[5:37], record.RequestHash[:])
	binary.LittleEndian.PutUint64(out[37:45], uint64(record.CreatedNS))
	binary.LittleEndian.PutUint64(out[45:53], uint64(record.ExpiresNS))
	binary.LittleEndian.PutUint32(out[53:57], uint32(len(record.Response)))
	copy(out[57:], record.Response)
	return out, nil
}

func DecodeIdempotency(raw []byte) (IdempotencyRecord, error) {
	if len(raw) < 57 || !bytes.Equal(raw[:4], []byte(idempotencyMagic)) {
		return IdempotencyRecord{}, nerr.New(nerr.InvalidFormat, "catalog.DecodeIdempotency", "bad idempotency record magic")
	}
	if raw[4] != idempotencyVersion {
		return IdempotencyRecord{}, nerr.New(nerr.InvalidFormat, "catalog.DecodeIdempotency", "unsupported idempotency record version")
	}
	var record IdempotencyRecord
	copy(record.RequestHash[:], raw[5:37])
	record.CreatedNS = int64(binary.LittleEndian.Uint64(raw[37:45]))
	record.ExpiresNS = int64(binary.LittleEndian.Uint64(raw[45:53]))
	n := int(binary.LittleEndian.Uint32(raw[53:57]))
	if record.CreatedNS <= 0 || record.ExpiresNS <= record.CreatedNS || n < 1 || n > MaxIdempotencyResponse || len(raw) != 57+n {
		return IdempotencyRecord{}, nerr.New(nerr.InvalidFormat, "catalog.DecodeIdempotency", "invalid idempotency record")
	}
	record.Response = append([]byte(nil), raw[57:]...)
	return record, nil
}
