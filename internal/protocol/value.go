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

// maxEnumLabelBytes bounds one ENUM label on the wire, matching
// types.EnumType's own per-label length validation (docs/design-datatypes.md
// D11). The label count itself is bounded by types.MaxEnumLabels.
const maxEnumLabelBytes = 255

// appendEnumLabels/readEnumLabels carry an ENUM Type's declared label list
// alongside the fixed 5-byte Precision/Scale/VecElem meta (appendType/
// appendValue), since ENUM is the first D-track type whose Type needs
// variable-length wire metadata (docs/design-datatypes.md D11). readType and
// readValue are both untrusted-decoder entry points (a value's Type also
// travels on the bound-parameter path, client -> server), so the label count
// and each label's length are bounded here before allocating, and the
// reconstructed list is re-validated through types.EnumType (dedup, 1..
// MaxEnumLabels, per-label length) rather than trusted as-is.
func appendEnumLabels(dst []byte, labels []string) ([]byte, error) {
	var n [2]byte
	encoding.PutU16(n[:], 0, uint16(len(labels)))
	dst = append(dst, n[:]...)
	var err error
	for _, l := range labels {
		if dst, err = appendU16String(dst, l, maxEnumLabelBytes); err != nil {
			return nil, err
		}
	}
	return dst, nil
}

func readEnumLabels(b []byte, off int) ([]string, int, error) {
	n, err := encoding.ReadU16(b, off)
	if err != nil {
		return nil, 0, protoErr("truncated enum label count")
	}
	if int(n) > types.MaxEnumLabels {
		return nil, 0, protoErr("enum label count exceeds limit")
	}
	off += 2
	labels := make([]string, n)
	for i := range labels {
		labels[i], off, err = readU16String(b, off, maxEnumLabelBytes)
		if err != nil {
			return nil, 0, err
		}
	}
	return labels, off, nil
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
	if v.Typ.Kind == types.KindEnum {
		var err error
		if dst, err = appendEnumLabels(dst, v.Typ.EnumLabels); err != nil {
			return nil, err
		}
	}
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
	if kind == types.KindEnum {
		labels, next, err := readEnumLabels(b, off)
		if err != nil {
			return types.Value{}, 0, err
		}
		if typ, err = types.EnumType(labels); err != nil {
			return types.Value{}, 0, nerr.Wrap(nerr.Protocol, "protocol.readValue", "invalid enum type", err)
		}
		off = next
	}
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

func appendType(dst []byte, t types.Type) ([]byte, error) {
	dst = append(dst, byte(t.Kind))
	var meta [5]byte
	encoding.PutU16(meta[:], 0, t.Precision)
	encoding.PutU16(meta[:], 2, t.Scale)
	meta[4] = t.VecElem
	dst = append(dst, meta[:]...)
	if t.Kind == types.KindEnum {
		var err error
		if dst, err = appendEnumLabels(dst, t.EnumLabels); err != nil {
			return nil, err
		}
	}
	return dst, nil
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
	off += 6
	if t.Kind == types.KindEnum {
		labels, next, err := readEnumLabels(b, off)
		if err != nil {
			return types.Type{}, 0, err
		}
		if t, err = types.EnumType(labels); err != nil {
			return types.Type{}, 0, nerr.Wrap(nerr.Protocol, "protocol.readType", "invalid enum type", err)
		}
		off = next
	}
	return t, off, nil
}
