// Package row encodes clustered-row MVCC headers stored in B+Tree leaf values.
package row

import (
	"bytes"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

const (
	// Magic marks an MVCC-wrapped leaf value. Unprefixed values are
	// pre-Phase-4 rows and are treated as always-visible committed data.
	Magic      = "NSRV"
	headerSize = 4 + 1 + 1 + 8 + 8 + 8
	curVersion = 1
)

// Version is one row version: current → undo → previous.
type Version struct {
	Xmin    format.TxnID
	Xmax    format.TxnID
	Undo    format.UndoID
	Payload []byte
}

func Encode(v Version) []byte {
	return EncodeInto(nil, v)
}

// EncodeInto writes v into buf, growing it if needed. The returned slice
// aliases buf and is only valid until the next EncodeInto on that buffer.
func EncodeInto(buf []byte, v Version) []byte {
	n := headerSize + len(v.Payload)
	if cap(buf) < n {
		buf = make([]byte, n)
	} else {
		buf = buf[:n]
	}
	copy(buf[0:4], Magic)
	buf[4] = curVersion
	buf[5] = 0
	encoding.PutU64(buf, 6, uint64(v.Xmin))
	encoding.PutU64(buf, 14, uint64(v.Xmax))
	encoding.PutU64(buf, 22, uint64(v.Undo))
	copy(buf[headerSize:], v.Payload)
	return buf
}

// Inspect reports whether b is an MVCC-wrapped value. Payload is a view
// into b and must not be retained after b is reused.
func Inspect(b []byte) (Version, bool, error) {
	if len(b) < headerSize || !bytes.Equal(b[:4], []byte(Magic)) {
		return Version{}, false, nil
	}
	if b[4] != curVersion {
		return Version{}, false, nerr.New(nerr.InvalidFormat, "row.Decode", "unsupported row version")
	}
	return Version{
		Xmin:    format.TxnID(encoding.U64(b, 6)),
		Xmax:    format.TxnID(encoding.U64(b, 14)),
		Undo:    format.UndoID(encoding.U64(b, 22)),
		Payload: b[headerSize:],
	}, true, nil
}

// Decode reports whether b is an MVCC-wrapped value. Payload is copied.
func Decode(b []byte) (Version, bool, error) {
	v, ok, err := Inspect(b)
	if err != nil || !ok {
		return v, ok, err
	}
	v.Payload = append([]byte(nil), v.Payload...)
	return v, true, nil
}

func PayloadOf(b []byte) ([]byte, error) {
	v, ok, err := Decode(b)
	if err != nil {
		return nil, err
	}
	if !ok {
		return append([]byte(nil), b...), nil
	}
	return v.Payload, nil
}
