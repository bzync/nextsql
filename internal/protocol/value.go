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

// maxCollDescDepth bounds recursion when reading an untrusted collection Type
// descriptor off the wire, matching types.MaxNestDepth (+1 slack for the
// outermost constructor call).
const maxCollDescDepth = types.MaxNestDepth + 1

// appendTypeBody writes the fixed 5-byte Precision/Scale/VecElem meta plus any
// variable metadata the Kind needs: an ENUM's label list, or a collection's
// recursive element/key/field descriptor. The leading Kind byte is written by
// the caller (appendType / appendValue).
func appendTypeBody(dst []byte, t types.Type) ([]byte, error) {
	var meta [5]byte
	encoding.PutU16(meta[:], 0, t.Precision)
	encoding.PutU16(meta[:], 2, t.Scale)
	meta[4] = t.VecElem
	dst = append(dst, meta[:]...)
	var err error
	switch t.Kind {
	case types.KindEnum:
		if dst, err = appendEnumLabels(dst, t.EnumLabels); err != nil {
			return nil, err
		}
	case types.KindArray:
		if len(t.Elem) != 1 {
			return nil, protoErr("ARRAY type missing element descriptor")
		}
		if dst, err = appendTypeFull(dst, t.Elem[0]); err != nil {
			return nil, err
		}
	case types.KindMap:
		if len(t.Key) != 1 || len(t.Elem) != 1 {
			return nil, protoErr("MAP type missing key/value descriptor")
		}
		if dst, err = appendTypeFull(dst, t.Key[0]); err != nil {
			return nil, err
		}
		if dst, err = appendTypeFull(dst, t.Elem[0]); err != nil {
			return nil, err
		}
	case types.KindStruct:
		if len(t.Fields) == 0 || len(t.Fields) > types.MaxStructFields {
			return nil, protoErr("STRUCT type field count out of range")
		}
		var n [2]byte
		encoding.PutU16(n[:], 0, uint16(len(t.Fields)))
		dst = append(dst, n[:]...)
		for _, f := range t.Fields {
			if dst, err = appendU16String(dst, f.Name, 255); err != nil {
				return nil, err
			}
			if dst, err = appendTypeFull(dst, f.Type); err != nil {
				return nil, err
			}
		}
	}
	return dst, nil
}

// appendTypeFull writes a complete recursive Type (Kind byte + body).
func appendTypeFull(dst []byte, t types.Type) ([]byte, error) {
	dst = append(dst, byte(t.Kind))
	return appendTypeBody(dst, t)
}

// readTypeBody is the untrusted-decoder inverse of appendTypeBody. kind has
// already been consumed by the caller; off points just past it.
func readTypeBody(b []byte, off int, kind types.Kind, depth int) (types.Type, int, error) {
	if depth > maxCollDescDepth {
		return types.Type{}, 0, protoErr("collection type nesting too deep")
	}
	if off+5 > len(b) {
		return types.Type{}, 0, protoErr("truncated type meta")
	}
	prec := encoding.U16(b, off)
	scale := encoding.U16(b, off+2)
	elem := b[off+4]
	off += 5
	base := types.Type{Kind: kind, Precision: prec, Scale: scale, VecElem: elem}
	switch kind {
	case types.KindEnum:
		labels, next, err := readEnumLabels(b, off)
		if err != nil {
			return types.Type{}, 0, err
		}
		et, err := types.EnumType(labels)
		if err != nil {
			return types.Type{}, 0, nerr.Wrap(nerr.Protocol, "protocol.readType", "invalid enum type", err)
		}
		return et, next, nil
	case types.KindArray:
		e, next, err := readTypeFull(b, off, depth+1)
		if err != nil {
			return types.Type{}, 0, err
		}
		at, err := types.ArrayType(e)
		if err != nil {
			return types.Type{}, 0, nerr.Wrap(nerr.Protocol, "protocol.readType", "invalid array type", err)
		}
		return at, next, nil
	case types.KindMap:
		k, next, err := readTypeFull(b, off, depth+1)
		if err != nil {
			return types.Type{}, 0, err
		}
		val, next2, err := readTypeFull(b, next, depth+1)
		if err != nil {
			return types.Type{}, 0, err
		}
		mt, err := types.MapType(k, val)
		if err != nil {
			return types.Type{}, 0, nerr.Wrap(nerr.Protocol, "protocol.readType", "invalid map type", err)
		}
		return mt, next2, nil
	case types.KindStruct:
		n, err := encoding.ReadU16(b, off)
		if err != nil {
			return types.Type{}, 0, protoErr("truncated struct field count")
		}
		if n == 0 || int(n) > types.MaxStructFields {
			return types.Type{}, 0, protoErr("struct field count out of range")
		}
		off += 2
		fields := make([]types.Field, n)
		for i := range fields {
			fields[i].Name, off, err = readU16String(b, off, 255)
			if err != nil {
				return types.Type{}, 0, err
			}
			fields[i].Type, off, err = readTypeFull(b, off, depth+1)
			if err != nil {
				return types.Type{}, 0, err
			}
		}
		st, err := types.StructType(fields)
		if err != nil {
			return types.Type{}, 0, nerr.Wrap(nerr.Protocol, "protocol.readType", "invalid struct type", err)
		}
		return st, off, nil
	default:
		return base, off, nil
	}
}

func readTypeFull(b []byte, off, depth int) (types.Type, int, error) {
	if off >= len(b) {
		return types.Type{}, 0, protoErr("truncated type")
	}
	kind := types.Kind(b[off])
	return readTypeBody(b, off+1, kind, depth)
}

func appendValue(dst []byte, v types.Value, max int) ([]byte, error) {
	dst = append(dst, byte(v.Typ.Kind))
	var flags byte
	if v.Null {
		flags |= flagNull
	}
	dst = append(dst, flags)
	var err error
	if dst, err = appendTypeBody(dst, v.Typ); err != nil {
		return nil, err
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
	if off+2 > len(b) {
		return types.Value{}, 0, protoErr("truncated value header")
	}
	kind := types.Kind(b[off])
	flags := b[off+1]
	off += 2
	typ, off, err := readTypeBody(b, off, kind, 0)
	if err != nil {
		return types.Value{}, 0, err
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
	return appendTypeFull(dst, t)
}

func readType(b []byte, off int) (types.Type, int, error) {
	return readTypeFull(b, off, 0)
}
