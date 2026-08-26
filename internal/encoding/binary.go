package encoding

import (
	"encoding/binary"

	"github.com/bzync/nextsql/internal/nerr"
)

// Little-endian helpers for versioned on-disk encodings.
// Do not serialize Go structs with encoding/gob or encoding/binary.Write of structs.

func PutU16(b []byte, off int, v uint16) {
	binary.LittleEndian.PutUint16(b[off:off+2], v)
}

func PutU32(b []byte, off int, v uint32) {
	binary.LittleEndian.PutUint32(b[off:off+4], v)
}

func PutU64(b []byte, off int, v uint64) {
	binary.LittleEndian.PutUint64(b[off:off+8], v)
}

func U16(b []byte, off int) uint16 {
	return binary.LittleEndian.Uint16(b[off : off+2])
}

func U32(b []byte, off int) uint32 {
	return binary.LittleEndian.Uint32(b[off : off+4])
}

func U64(b []byte, off int) uint64 {
	return binary.LittleEndian.Uint64(b[off : off+8])
}

func ReadU16(b []byte, off int) (uint16, error) {
	if err := bounds(b, off, 2); err != nil {
		return 0, err
	}
	return U16(b, off), nil
}

func ReadU32(b []byte, off int) (uint32, error) {
	if err := bounds(b, off, 4); err != nil {
		return 0, err
	}
	return U32(b, off), nil
}

func ReadU64(b []byte, off int) (uint64, error) {
	if err := bounds(b, off, 8); err != nil {
		return 0, err
	}
	return U64(b, off), nil
}

func ReadBytes(b []byte, off, n int) ([]byte, error) {
	if err := bounds(b, off, n); err != nil {
		return nil, err
	}
	out := make([]byte, n)
	copy(out, b[off:off+n])
	return out, nil
}

func bounds(b []byte, off, n int) error {
	if off < 0 || n < 0 || off+n > len(b) {
		return nerr.New(nerr.InvalidFormat, "encoding", "truncated field")
	}
	return nil
}
