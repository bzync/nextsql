package protocol

import (
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

const flagNull = 0x01

func appendU16String(dst []byte, s string, max int) ([]byte, error) {
	if len(s) > max {
		return nil, nerr.New(nerr.Protocol, "protocol", "string exceeds limit")
	}
	if len(s) > 0xFFFF {
		return nil, nerr.New(nerr.Protocol, "protocol", "string exceeds u16 length")
	}
	var n [2]byte
	encoding.PutU16(n[:], 0, uint16(len(s)))
	dst = append(dst, n[:]...)
	return append(dst, s...), nil
}

func readU16String(b []byte, off, max int) (string, int, error) {
	n, err := encoding.ReadU16(b, off)
	if err != nil {
		return "", 0, protoErr("truncated string length")
	}
	if int(n) > max {
		return "", 0, protoErr("string exceeds limit")
	}
	raw, err := encoding.ReadBytes(b, off+2, int(n))
	if err != nil {
		return "", 0, protoErr("truncated string")
	}
	return string(raw), off + 2 + int(n), nil
}

func appendU32Bytes(dst []byte, s []byte, max int) ([]byte, error) {
	if len(s) > max {
		return nil, nerr.New(nerr.Protocol, "protocol", "bytes exceed limit")
	}
	var n [4]byte
	encoding.PutU32(n[:], 0, uint32(len(s)))
	dst = append(dst, n[:]...)
	return append(dst, s...), nil
}

func readU32Bytes(b []byte, off, max int) ([]byte, int, error) {
	n, err := encoding.ReadU32(b, off)
	if err != nil {
		return nil, 0, protoErr("truncated bytes length")
	}
	if int(n) > max {
		return nil, 0, protoErr("bytes exceed limit")
	}
	raw, err := encoding.ReadBytes(b, off+4, int(n))
	if err != nil {
		return nil, 0, protoErr("truncated bytes")
	}
	return raw, off + 4 + int(n), nil
}

func appendValue(dst []byte, v types.Value, max int) ([]byte, error) {
	dst = append(dst, byte(v.Typ.Kind))
	var flags byte
	if v.Null {
		flags |= flagNull
	}
	dst = append(dst, flags)
	var meta [5]byte
	encoding.PutU16(meta[:], 0, v.Typ.Precision)
	encoding.PutU16(meta[:], 2, v.Typ.Scale)
	meta[4] = v.Typ.VecElem
	dst = append(dst, meta[:]...)
	if v.Null {
		return dst, nil
	}
	payload, err := types.EncodeScalar(v)
	if err != nil {
		return nil, err
	}
	if len(payload) > max {
		return nil, nerr.New(nerr.Protocol, "protocol", "value exceeds limit")
	}
	return append(dst, payload...), nil
}

func readValue(b []byte, off, max int) (types.Value, int, error) {
	if off+7 > len(b) {
		return types.Value{}, 0, protoErr("truncated value header")
	}
	kind := types.Kind(b[off])
	flags := b[off+1]
	prec := encoding.U16(b, off+2)
	scale := encoding.U16(b, off+4)
	elem := b[off+6]
	off += 7
	typ := types.Type{Kind: kind, Precision: prec, Scale: scale, VecElem: elem}
	if flags&flagNull != 0 {
		return types.Null(typ), off, nil
	}
	remain := len(b) - off
	if remain > max {
		remain = max
	}
	v, next, err := types.DecodeScalar(b, off, typ)
	if err != nil {
		return types.Value{}, 0, nerr.Wrap(nerr.Protocol, "protocol.readValue", "decode", err)
	}
	if next-off > max {
		return types.Value{}, 0, protoErr("value exceeds limit")
	}
	return v, next, nil
}

func appendType(dst []byte, t types.Type) []byte {
	dst = append(dst, byte(t.Kind))
	var meta [5]byte
	encoding.PutU16(meta[:], 0, t.Precision)
	encoding.PutU16(meta[:], 2, t.Scale)
	meta[4] = t.VecElem
	return append(dst, meta[:]...)
}

func readType(b []byte, off int) (types.Type, int, error) {
	if off+6 > len(b) {
		return types.Type{}, 0, protoErr("truncated type")
	}
	t := types.Type{
		Kind:      types.Kind(b[off]),
		Precision: encoding.U16(b, off+1),
		Scale:     encoding.U16(b, off+3),
		VecElem:   b[off+5],
	}
	return t, off + 6, nil
}
