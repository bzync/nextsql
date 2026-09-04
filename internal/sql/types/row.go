package types

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/big"

	"github.com/bzync/nextsql/internal/encoding"
	nsjson "github.com/bzync/nextsql/internal/json"
	"github.com/bzync/nextsql/internal/nerr"
)

const (
	rowMagic    = "NSRW"
	rowVersion  = 1
	rowMagicU32 = 0x5752534e // little-endian "NSRW"
)

// EncodeRow writes values in catalog column order.
func EncodeRow(vals []Value) ([]byte, error) {
	n := len(vals)
	if n > 4096 {
		return nil, nerr.New(nerr.InvalidArgument, "types.EncodeRow", "too many columns")
	}
	nulls := make([]byte, (n+7)/8)
	var payload []byte
	for i, v := range vals {
		if v.Null {
			nulls[i/8] |= 1 << (i % 8)
			continue
		}
		enc, err := encodeScalar(v)
		if err != nil {
			return nil, err
		}
		payload = append(payload, enc...)
	}
	buf := make([]byte, 8+len(nulls)+len(payload))
	copy(buf[0:4], rowMagic)
	buf[4] = rowVersion
	buf[5] = 0
	encoding.PutU16(buf, 6, uint16(n))
	copy(buf[8:], nulls)
	copy(buf[8+len(nulls):], payload)
	return buf, nil
}

// DecodeRow reads n catalog-typed values. dest types come from cols.
func DecodeRow(raw []byte, cols []Type) ([]Value, error) {
	if len(raw) < 8 || !bytes.Equal(raw[0:4], []byte(rowMagic)) {
		return nil, nerr.New(nerr.InvalidFormat, "types.DecodeRow", "bad row magic")
	}
	if raw[4] != rowVersion {
		return nil, nerr.New(nerr.InvalidFormat, "types.DecodeRow", "unsupported row version")
	}
	n := int(encoding.U16(raw, 6))
	if n != len(cols) {
		return nil, nerr.New(nerr.InvalidFormat, "types.DecodeRow", "column count mismatch")
	}
	nb := (n + 7) / 8
	if len(raw) < 8+nb {
		return nil, nerr.New(nerr.InvalidFormat, "types.DecodeRow", "truncated null map")
	}
	nulls := raw[8 : 8+nb]
	off := 8 + nb
	out := make([]Value, n)
	for i := 0; i < n; i++ {
		if nulls[i/8]&(1<<(i%8)) != 0 {
			out[i] = Null(cols[i])
			continue
		}
		v, next, err := decodeScalar(raw, off, cols[i])
		if err != nil {
			return nil, err
		}
		off = next
		out[i] = v
	}
	return out, nil
}

// DecodeRowColumn reads only column i. Other columns are skipped, not materialized.
func DecodeRowColumn(raw []byte, cols []Type, i int) (Value, error) {
	if len(raw) < 8 || !bytes.Equal(raw[0:4], []byte(rowMagic)) {
		return Value{}, nerr.New(nerr.InvalidFormat, "types.DecodeRowColumn", "bad row magic")
	}
	if raw[4] != rowVersion {
		return Value{}, nerr.New(nerr.InvalidFormat, "types.DecodeRowColumn", "unsupported row version")
	}
	n := int(encoding.U16(raw, 6))
	if n != len(cols) {
		return Value{}, nerr.New(nerr.InvalidFormat, "types.DecodeRowColumn", "column count mismatch")
	}
	if i < 0 || i >= n {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.DecodeRowColumn", "column out of range")
	}
	nb := (n + 7) / 8
	if len(raw) < 8+nb {
		return Value{}, nerr.New(nerr.InvalidFormat, "types.DecodeRowColumn", "truncated null map")
	}
	nulls := raw[8 : 8+nb]
	off := 8 + nb
	for j := 0; j < i; j++ {
		if nulls[j/8]&(1<<(j%8)) != 0 {
			continue
		}
		next, err := skipScalar(raw, off, cols[j])
		if err != nil {
			return Value{}, err
		}
		off = next
	}
	if nulls[i/8]&(1<<(i%8)) != 0 {
		return Null(cols[i]), nil
	}
	v, _, err := decodeScalar(raw, off, cols[i])
	return v, err
}

// PeekRowColumn returns a view of column i without allocating a Value.
// For STRING/TEXT the slice aliases raw. Caller must not retain it after raw
// is reused. ok is false when the column is not a string or is NULL.
func PeekRowColumn(raw []byte, cols []Type, i int) (s []byte, null bool, err error) {
	if len(raw) < 8 || encoding.U32(raw, 0) != rowMagicU32 {
		return nil, false, nerr.New(nerr.InvalidFormat, "types.PeekRowColumn", "bad row magic")
	}
	if raw[4] != rowVersion {
		return nil, false, nerr.New(nerr.InvalidFormat, "types.PeekRowColumn", "unsupported row version")
	}
	n := int(encoding.U16(raw, 6))
	if n != len(cols) {
		return nil, false, nerr.New(nerr.InvalidFormat, "types.PeekRowColumn", "column count mismatch")
	}
	if i < 0 || i >= n {
		return nil, false, nerr.New(nerr.InvalidArgument, "types.PeekRowColumn", "column out of range")
	}
	nb := (n + 7) / 8
	if len(raw) < 8+nb {
		return nil, false, nerr.New(nerr.InvalidFormat, "types.PeekRowColumn", "truncated null map")
	}
	nulls := raw[8 : 8+nb]
	off := 8 + nb
	for j := 0; j < i; j++ {
		if nulls[j/8]&(1<<(j%8)) != 0 {
			continue
		}
		next, err := skipScalar(raw, off, cols[j])
		if err != nil {
			return nil, false, err
		}
		off = next
	}
	if nulls[i/8]&(1<<(i%8)) != 0 {
		return nil, true, nil
	}
	kind := cols[i].Kind
	if kind != KindString && kind != KindText {
		return nil, false, nerr.New(nerr.InvalidArgument, "types.PeekRowColumn", "not a string column")
	}
	b, _, err := decodeBytes(raw, off)
	return b, false, err
}

// ReplaceRowColumn rewrites column i in an encoded row. Other columns are copied.
func ReplaceRowColumn(raw []byte, cols []Type, i int, v Value) ([]byte, error) {
	if len(raw) < 8 || !bytes.Equal(raw[0:4], []byte(rowMagic)) {
		return nil, nerr.New(nerr.InvalidFormat, "types.ReplaceRowColumn", "bad row magic")
	}
	if raw[4] != rowVersion {
		return nil, nerr.New(nerr.InvalidFormat, "types.ReplaceRowColumn", "unsupported row version")
	}
	n := int(encoding.U16(raw, 6))
	if n != len(cols) || i < 0 || i >= n {
		return nil, nerr.New(nerr.InvalidArgument, "types.ReplaceRowColumn", "column out of range")
	}
	nb := (n + 7) / 8
	if len(raw) < 8+nb {
		return nil, nerr.New(nerr.InvalidFormat, "types.ReplaceRowColumn", "truncated null map")
	}
	off := 8 + nb
	start := off
	for j := 0; j < i; j++ {
		if raw[8+j/8]&(1<<(j%8)) != 0 {
			continue
		}
		next, err := skipScalar(raw, off, cols[j])
		if err != nil {
			return nil, err
		}
		off = next
		start = next
	}
	end := start
	wasNull := raw[8+i/8]&(1<<(i%8)) != 0
	if !wasNull {
		next, err := skipScalar(raw, start, cols[i])
		if err != nil {
			return nil, err
		}
		end = next
	}
	var mid []byte
	nulls := append([]byte(nil), raw[8:8+nb]...)
	if v.Null {
		nulls[i/8] |= 1 << (i % 8)
	} else {
		nulls[i/8] &^= 1 << (i % 8)
		enc, err := encodeScalar(v)
		if err != nil {
			return nil, err
		}
		mid = enc
	}
	need := 8 + nb + len(raw[8+nb:start]) + len(mid) + len(raw[end:])
	out := make([]byte, need)
	copy(out[0:8], raw[0:8])
	copy(out[8:], nulls)
	p := 8 + nb
	copy(out[p:], raw[8+nb:start])
	p += start - (8 + nb)
	copy(out[p:], mid)
	p += len(mid)
	copy(out[p:], raw[end:])
	return out, nil
}

// ReplaceRowColumnInto is ReplaceRowColumn writing into dst when cap is enough.
func ReplaceRowColumnInto(dst, raw []byte, cols []Type, i int, v Value) ([]byte, error) {
	if len(raw) < 8 || !bytes.Equal(raw[0:4], []byte(rowMagic)) {
		return nil, nerr.New(nerr.InvalidFormat, "types.ReplaceRowColumn", "bad row magic")
	}
	if raw[4] != rowVersion {
		return nil, nerr.New(nerr.InvalidFormat, "types.ReplaceRowColumn", "unsupported row version")
	}
	n := int(encoding.U16(raw, 6))
	if n != len(cols) || i < 0 || i >= n {
		return nil, nerr.New(nerr.InvalidArgument, "types.ReplaceRowColumn", "column out of range")
	}
	nb := (n + 7) / 8
	if len(raw) < 8+nb {
		return nil, nerr.New(nerr.InvalidFormat, "types.ReplaceRowColumn", "truncated null map")
	}
	off := 8 + nb
	start := off
	for j := 0; j < i; j++ {
		if raw[8+j/8]&(1<<(j%8)) != 0 {
			continue
		}
		next, err := skipScalar(raw, off, cols[j])
		if err != nil {
			return nil, err
		}
		off = next
		start = next
	}
	end := start
	wasNull := raw[8+i/8]&(1<<(i%8)) != 0
	if !wasNull {
		next, err := skipScalar(raw, start, cols[i])
		if err != nil {
			return nil, err
		}
		end = next
	}
	var mid []byte
	nulls := append([]byte(nil), raw[8:8+nb]...)
	if v.Null {
		nulls[i/8] |= 1 << (i % 8)
	} else {
		nulls[i/8] &^= 1 << (i % 8)
		enc, err := encodeScalar(v)
		if err != nil {
			return nil, err
		}
		mid = enc
	}
	need := 8 + nb + len(raw[8+nb:start]) + len(mid) + len(raw[end:])
	if cap(dst) < need {
		dst = make([]byte, need)
	} else {
		dst = dst[:need]
	}
	copy(dst[0:8], raw[0:8])
	copy(dst[8:], nulls)
	p := 8 + nb
	copy(dst[p:], raw[8+nb:start])
	p += start - (8 + nb)
	copy(dst[p:], mid)
	p += len(mid)
	copy(dst[p:], raw[end:])
	return dst, nil
}

func skipScalar(raw []byte, off int, t Type) (int, error) {
	switch t.Kind {
	case KindUUID:
		if off+16 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated uuid")
		}
		return off + 16, nil
	case KindString, KindText, KindBlob, KindJSON, KindDecimal, KindChar, KindVarchar:
		n, err := encoding.ReadU32(raw, off)
		if err != nil {
			return 0, err
		}
		end := off + 4 + int(n)
		if end > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated bytes")
		}
		return end, nil
	case KindTimestampTZ, KindTimestamp:
		if off+8 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated timestamp")
		}
		return off + 8, nil
	case KindEnum:
		if off+2 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated enum")
		}
		return off + 2, nil
	case KindFloat32:
		if off+4 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated float32")
		}
		return off + 4, nil
	case KindFloat64:
		if off+8 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated float64")
		}
		return off + 8, nil
	case KindInt8:
		if off+1 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated int8")
		}
		return off + 1, nil
	case KindInt16:
		if off+2 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated int16")
		}
		return off + 2, nil
	case KindInt32:
		if off+4 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated int32")
		}
		return off + 4, nil
	case KindInt64:
		if off+8 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated int64")
		}
		return off + 8, nil
	case KindUint8:
		if off+1 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated uint8")
		}
		return off + 1, nil
	case KindUint16:
		if off+2 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated uint16")
		}
		return off + 2, nil
	case KindUint32:
		if off+4 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated uint32")
		}
		return off + 4, nil
	case KindUint64:
		if off+8 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated uint64")
		}
		return off + 8, nil
	case KindDate:
		if off+4 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated date")
		}
		return off + 4, nil
	case KindTime:
		if off+8 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated time")
		}
		return off + 8, nil
	case KindInterval:
		if off+16 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated interval")
		}
		return off + 16, nil
	case KindVector:
		dim, err := encoding.ReadU16(raw, off)
		if err != nil {
			return 0, err
		}
		if off+2 >= len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated vector flags")
		}
		flag := raw[off+2]
		if flag&1 != 0 {
			return off + 3, nil
		}
		if flag&2 != 0 {
			nnz, err := encoding.ReadU32(raw, off+3)
			if err != nil {
				return 0, err
			}
			if nnz > MaxSparseSQLNNZ {
				return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "sparse vector too long")
			}
			end := off + 7 + int(nnz)*8
			if end > len(raw) {
				return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated sparse vector")
			}
			return end, nil
		}
		end := off + 3 + int(dim)*4
		if end > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated vector")
		}
		return end, nil
	case KindBool:
		if off >= len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated bool")
		}
		return off + 1, nil
	case KindPoint:
		if off+16 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated point")
		}
		return off + 16, nil
	case KindBox:
		if off+32 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated box")
		}
		return off + 32, nil
	case KindStruct, KindArray, KindMap, KindGeometry, KindGeography:
		// The collection and general-geometry heap-row forms are both
		// prefixed with a u32 body length, so a skip is O(1) regardless of
		// nesting depth.
		bodyLen, err := encoding.ReadU32(raw, off)
		if err != nil {
			return 0, err
		}
		end := off + 4 + int(bodyLen)
		if end > len(raw) || end < off {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated value")
		}
		return end, nil
	default:
		_, next, err := decodeScalar(raw, off, t)
		return next, err
	}
}

// EncodeScalar writes one non-null value. The caller records null separately.
func EncodeScalar(v Value) ([]byte, error) { return encodeScalar(v) }

// DecodeScalar reads one non-null value of type t starting at off.
func DecodeScalar(raw []byte, off int, t Type) (Value, int, error) {
	return decodeScalar(raw, off, t)
}

func encodeScalar(v Value) ([]byte, error) {
	switch v.Typ.Kind {
	case KindUUID:
		return append([]byte(nil), v.UUID[:]...), nil
	case KindString, KindText, KindBlob, KindChar, KindVarchar:
		return encodeBytes([]byte(v.Str)), nil
	case KindJSON:
		if err := nsjson.Validate(v.JSON); err != nil {
			return nil, err
		}
		return encodeBytes(v.JSON), nil
	case KindDecimal:
		body := encodeDecimal(v.Dec)
		return encodeBytes(body), nil
	case KindTimestampTZ, KindTimestamp:
		buf := make([]byte, 8)
		encoding.PutU64(buf, 0, uint64(v.Time))
		return buf, nil
	case KindEnum:
		if v.Int < 0 || v.Int > 0xFFFF {
			return nil, nerr.New(nerr.InvalidArgument, "types.encodeScalar", "ENUM ordinal out of range")
		}
		buf := make([]byte, 2)
		encoding.PutU16(buf, 0, uint16(v.Int))
		return buf, nil
	case KindFloat32:
		buf := make([]byte, 4)
		binary.LittleEndian.PutUint32(buf, math.Float32bits(float32(canonFloat(v.Flt))))
		return buf, nil
	case KindFloat64:
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, math.Float64bits(canonFloat(v.Flt)))
		return buf, nil
	case KindInt8:
		return []byte{byte(int8(v.Int))}, nil
	case KindInt16:
		buf := make([]byte, 2)
		encoding.PutU16(buf, 0, uint16(int16(v.Int)))
		return buf, nil
	case KindInt32:
		buf := make([]byte, 4)
		encoding.PutU32(buf, 0, uint32(int32(v.Int)))
		return buf, nil
	case KindInt64:
		buf := make([]byte, 8)
		encoding.PutU64(buf, 0, uint64(v.Int))
		return buf, nil
	case KindUint8:
		return []byte{byte(v.Uint)}, nil
	case KindUint16:
		buf := make([]byte, 2)
		encoding.PutU16(buf, 0, uint16(v.Uint))
		return buf, nil
	case KindUint32:
		buf := make([]byte, 4)
		encoding.PutU32(buf, 0, uint32(v.Uint))
		return buf, nil
	case KindUint64:
		buf := make([]byte, 8)
		encoding.PutU64(buf, 0, v.Uint)
		return buf, nil
	case KindDate:
		buf := make([]byte, 4)
		encoding.PutU32(buf, 0, uint32(int32(v.Int)))
		return buf, nil
	case KindTime:
		buf := make([]byte, 8)
		encoding.PutU64(buf, 0, uint64(v.Time))
		return buf, nil
	case KindInterval:
		// months(4) + days(4) + nanos(8), all little-endian, no sign-bit
		// flip (that only applies to encodeSortable below) — docs/design-datatypes.md D6.
		buf := make([]byte, 16)
		encoding.PutU32(buf, 0, uint32(v.IntervalMonths))
		encoding.PutU32(buf, 4, uint32(v.IntervalDays))
		encoding.PutU64(buf, 8, uint64(v.Time))
		return buf, nil
	case KindVector:
		if v.VecRef {
			dim := v.Typ.Precision
			if dim == 0 {
				dim = uint16(len(v.Vec))
			}
			buf := make([]byte, 3)
			encoding.PutU16(buf, 0, dim)
			buf[2] = 1
			return buf, nil
		}
		if v.Typ.VecElem == VecSparse || len(v.SparseIdx) > 0 {
			dim := v.Typ.Precision
			if err := ValidateSparse(uint32(dim), v.SparseIdx, v.SparseVal); err != nil {
				return nil, err
			}
			nnz := len(v.SparseIdx)
			buf := make([]byte, 7+8*nnz)
			encoding.PutU16(buf, 0, dim)
			buf[2] = 2
			encoding.PutU32(buf, 3, uint32(nnz))
			for i := 0; i < nnz; i++ {
				encoding.PutU32(buf, 7+i*8, v.SparseIdx[i])
				encoding.PutU32(buf, 11+i*8, math.Float32bits(v.SparseVal[i]))
			}
			return buf, nil
		}
		if err := ValidateVector(v.Vec); err != nil {
			return nil, err
		}
		buf := make([]byte, 3+4*len(v.Vec))
		encoding.PutU16(buf, 0, uint16(len(v.Vec)))
		buf[2] = 0
		PutF32s(buf[3:], v.Vec)
		return buf, nil
	case KindBool:
		if v.Bool {
			return []byte{1}, nil
		}
		return []byte{0}, nil
	case KindPoint:
		buf := make([]byte, 16)
		binary.LittleEndian.PutUint64(buf[0:], math.Float64bits(v.Lon))
		binary.LittleEndian.PutUint64(buf[8:], math.Float64bits(v.Lat))
		return buf, nil
	case KindBox:
		buf := make([]byte, 32)
		for i := 0; i < 4; i++ {
			binary.LittleEndian.PutUint64(buf[i*8:], math.Float64bits(v.Box[i]))
		}
		return buf, nil
	case KindLine, KindPolygon:
		return encodeGeo(v)
	case KindStruct, KindArray, KindMap:
		return encodeCollection(v)
	case KindGeometry, KindGeography:
		return encodeGeneralGeo(v)
	default:
		return nil, nerr.New(nerr.InvalidArgument, "types.encodeScalar", "unsupported type")
	}
}

// collectionMembers returns a collection value's members in encode order and
// the matching member types from t. For MAP the two are interleaved
// key,value,key,value,... so one flat loop covers every collection kind.
func collectionMembers(v Value) (members []Value, err error) {
	switch v.Typ.Kind {
	case KindStruct:
		if len(v.Coll) != len(v.Typ.Fields) {
			return nil, nerr.New(nerr.InvalidArgument, "types.collection", "STRUCT field count mismatch")
		}
		return v.Coll, nil
	case KindArray:
		return v.Coll, nil
	case KindMap:
		if len(v.CollKeys) != len(v.Coll) {
			return nil, nerr.New(nerr.InvalidArgument, "types.collection", "MAP key/value count mismatch")
		}
		out := make([]Value, 0, len(v.Coll)*2)
		for i := range v.Coll {
			out = append(out, v.CollKeys[i], v.Coll[i])
		}
		return out, nil
	default:
		return nil, nerr.New(nerr.InvalidArgument, "types.collection", "not a collection")
	}
}

// encodeCollection writes the self-describing nested heap-row form for a
// STRUCT / ARRAY / MAP value (docs/design-collections.md):
//
//	u32 body-length   (everything after this prefix, so skipScalar is O(1))
//	u32 member-count   (fields for STRUCT; elements for ARRAY; 2*entries for MAP)
//	null bitmap        ceil(member-count / 8) bytes
//	member payloads    encodeScalar(member) for each non-null member, in order
//
// Members are themselves encoded through encodeScalar, so collections nest.
func encodeCollection(v Value) ([]byte, error) {
	members, err := collectionMembers(v)
	if err != nil {
		return nil, err
	}
	n := len(members)
	if n > 2*MaxCollectionLen+2 {
		return nil, nerr.New(nerr.InvalidArgument, "types.encodeCollection", "collection too large")
	}
	nulls := make([]byte, (n+7)/8)
	var payload []byte
	for i, m := range members {
		if m.Null {
			nulls[i/8] |= 1 << (i % 8)
			continue
		}
		enc, err := encodeScalar(m)
		if err != nil {
			return nil, err
		}
		payload = append(payload, enc...)
	}
	body := make([]byte, 4+len(nulls)+len(payload))
	encoding.PutU32(body, 0, uint32(n))
	copy(body[4:], nulls)
	copy(body[4+len(nulls):], payload)
	out := make([]byte, 4+len(body))
	encoding.PutU32(out, 0, uint32(len(body)))
	copy(out[4:], body)
	return out, nil
}

// memberTypes returns the decode-order member type list for a collection Type:
// field types for STRUCT, the element type repeated n times for ARRAY, and
// alternating key/value types for MAP (n is the on-disk member count).
func memberTypes(t Type, n int) ([]Type, error) {
	switch t.Kind {
	case KindStruct:
		if n != len(t.Fields) {
			return nil, nerr.New(nerr.InvalidFormat, "types.decodeCollection", "STRUCT field count mismatch")
		}
		out := make([]Type, n)
		for i, f := range t.Fields {
			out[i] = f.Type
		}
		return out, nil
	case KindArray:
		if len(t.Elem) != 1 {
			return nil, nerr.New(nerr.InvalidFormat, "types.decodeCollection", "ARRAY missing element type")
		}
		out := make([]Type, n)
		for i := range out {
			out[i] = t.Elem[0]
		}
		return out, nil
	case KindMap:
		if len(t.Key) != 1 || len(t.Elem) != 1 {
			return nil, nerr.New(nerr.InvalidFormat, "types.decodeCollection", "MAP missing key/value type")
		}
		if n%2 != 0 {
			return nil, nerr.New(nerr.InvalidFormat, "types.decodeCollection", "MAP member count is odd")
		}
		out := make([]Type, n)
		for i := 0; i < n; i += 2 {
			out[i] = t.Key[0]
			out[i+1] = t.Elem[0]
		}
		return out, nil
	default:
		return nil, nerr.New(nerr.InvalidFormat, "types.decodeCollection", "not a collection")
	}
}

func decodeCollection(raw []byte, off int, t Type) (Value, int, error) {
	bodyLen, err := encoding.ReadU32(raw, off)
	if err != nil {
		return Value{}, 0, err
	}
	body, err := encoding.ReadBytes(raw, off+4, int(bodyLen))
	if err != nil {
		return Value{}, 0, err
	}
	end := off + 4 + int(bodyLen)
	n32, err := encoding.ReadU32(body, 0)
	if err != nil {
		return Value{}, 0, err
	}
	n := int(n32)
	// Bound the member count before allocating: each present member is at
	// least one payload byte and each null member one bitmap bit, so n can
	// never legitimately exceed the body length.
	if n < 0 || n > 2*MaxCollectionLen+2 || n > len(body) {
		return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeCollection", "collection member count out of range")
	}
	mts, err := memberTypes(t, n)
	if err != nil {
		return Value{}, 0, err
	}
	nb := (n + 7) / 8
	if len(body) < 4+nb {
		return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeCollection", "truncated collection null map")
	}
	nulls := body[4 : 4+nb]
	p := 4 + nb
	members := make([]Value, n)
	for i := 0; i < n; i++ {
		if nulls[i/8]&(1<<(i%8)) != 0 {
			members[i] = Null(mts[i])
			continue
		}
		mv, next, err := decodeScalar(body, p, mts[i])
		if err != nil {
			return Value{}, 0, err
		}
		p = next
		members[i] = mv
	}
	return assembleCollection(t, members), end, nil
}

// assembleCollection turns a flat decode-order member list back into a typed
// collection Value.
func assembleCollection(t Type, members []Value) Value {
	switch t.Kind {
	case KindMap:
		keys := make([]Value, 0, len(members)/2)
		vals := make([]Value, 0, len(members)/2)
		for i := 0; i+1 < len(members); i += 2 {
			keys = append(keys, members[i])
			vals = append(vals, members[i+1])
		}
		return Value{Typ: t, CollKeys: keys, Coll: vals}
	default: // STRUCT, ARRAY
		return Value{Typ: t, Coll: members}
	}
}

func decodeScalar(raw []byte, off int, t Type) (Value, int, error) {
	switch t.Kind {
	case KindUUID:
		b, err := encoding.ReadBytes(raw, off, 16)
		if err != nil {
			return Value{}, 0, err
		}
		var u [16]byte
		copy(u[:], b)
		return UUIDValue(u), off + 16, nil
	case KindString, KindText, KindBlob, KindChar, KindVarchar:
		b, n, err := decodeBytes(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		v := Value{Typ: t, Str: string(b)}
		return v, n, nil
	case KindJSON:
		b, n, err := decodeBytes(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		return JSONValue(b), n, nil
	case KindDecimal:
		b, n, err := decodeBytes(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		d, err := decodeDecimal(b)
		if err != nil {
			return Value{}, 0, err
		}
		return DecimalValue(d, t), n, nil
	case KindTimestampTZ:
		u, err := encoding.ReadU64(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		return TimeValue(int64(u)), off + 8, nil
	case KindTimestamp:
		u, err := encoding.ReadU64(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		return NaiveTimestampValue(int64(u)), off + 8, nil
	case KindEnum:
		u, err := encoding.ReadU16(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		v, err := EnumValueByOrdinal(int(u), t)
		if err != nil {
			return Value{}, 0, err
		}
		return v, off + 2, nil
	case KindFloat32:
		b, err := encoding.ReadBytes(raw, off, 4)
		if err != nil {
			return Value{}, 0, err
		}
		return Float32Value(float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))), off + 4, nil
	case KindFloat64:
		b, err := encoding.ReadBytes(raw, off, 8)
		if err != nil {
			return Value{}, 0, err
		}
		return Float64Value(math.Float64frombits(binary.LittleEndian.Uint64(b))), off + 8, nil
	case KindInt8:
		if off >= len(raw) {
			return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeScalar", "truncated int8")
		}
		return IntValue(KindInt8, int64(int8(raw[off]))), off + 1, nil
	case KindInt16:
		u, err := encoding.ReadU16(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		return IntValue(KindInt16, int64(int16(u))), off + 2, nil
	case KindInt32:
		u, err := encoding.ReadU32(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		return IntValue(KindInt32, int64(int32(u))), off + 4, nil
	case KindInt64:
		u, err := encoding.ReadU64(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		return IntValue(KindInt64, int64(u)), off + 8, nil
	case KindUint8:
		if off >= len(raw) {
			return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeScalar", "truncated uint8")
		}
		return UintValue(KindUint8, uint64(raw[off])), off + 1, nil
	case KindUint16:
		u, err := encoding.ReadU16(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		return UintValue(KindUint16, uint64(u)), off + 2, nil
	case KindUint32:
		u, err := encoding.ReadU32(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		return UintValue(KindUint32, uint64(u)), off + 4, nil
	case KindUint64:
		u, err := encoding.ReadU64(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		return UintValue(KindUint64, u), off + 8, nil
	case KindDate:
		u, err := encoding.ReadU32(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		return DateValue(int32(u)), off + 4, nil
	case KindTime:
		u, err := encoding.ReadU64(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		return TimeOfDayValue(int64(u)), off + 8, nil
	case KindInterval:
		if off+16 > len(raw) {
			return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeScalar", "truncated interval")
		}
		months, err := encoding.ReadU32(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		days, err := encoding.ReadU32(raw, off+4)
		if err != nil {
			return Value{}, 0, err
		}
		nanos, err := encoding.ReadU64(raw, off+8)
		if err != nil {
			return Value{}, 0, err
		}
		return IntervalValue(int32(months), int32(days), int64(nanos)), off + 16, nil
	case KindVector:
		dim, err := encoding.ReadU16(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		if off+2 >= len(raw) {
			return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeScalar", "truncated vector flags")
		}
		flag := raw[off+2]
		if flag&1 != 0 {
			out := VectorRef(t)
			if t.Precision == 0 {
				out.Typ.Precision = dim
			}
			return out, off + 3, nil
		}
		if flag&2 != 0 {
			nnz, err := encoding.ReadU32(raw, off+3)
			if err != nil {
				return Value{}, 0, err
			}
			if nnz > MaxSparseSQLNNZ {
				return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeScalar", "sparse vector too long")
			}
			need := int(nnz) * 8
			b, err := encoding.ReadBytes(raw, off+7, need)
			if err != nil {
				return Value{}, 0, err
			}
			idx := make([]uint32, nnz)
			val := make([]float32, nnz)
			for i := 0; i < int(nnz); i++ {
				idx[i] = encoding.U32(b, i*8)
				val[i] = math.Float32frombits(encoding.U32(b, i*8+4))
			}
			vt := t
			if vt.Precision == 0 {
				vt.Precision = dim
			}
			if vt.VecElem == 0 {
				vt.VecElem = VecSparse
			}
			if err := ValidateSparse(uint32(vt.Precision), idx, val); err != nil {
				return Value{}, 0, err
			}
			return SparseValue(idx, val, vt), off + 7 + need, nil
		}
		need := int(dim) * 4
		b, err := encoding.ReadBytes(raw, off+3, need)
		if err != nil {
			return Value{}, 0, err
		}
		vt := t
		if vt.Precision == 0 {
			vt.Precision = dim
		}
		return VectorValue(F32s(b), vt), off + 3 + need, nil
	case KindBool:
		if off >= len(raw) {
			return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeScalar", "truncated bool")
		}
		return BoolValue(raw[off] != 0), off + 1, nil
	case KindPoint:
		b, err := encoding.ReadBytes(raw, off, 16)
		if err != nil {
			return Value{}, 0, err
		}
		lon := math.Float64frombits(binary.LittleEndian.Uint64(b[0:8]))
		lat := math.Float64frombits(binary.LittleEndian.Uint64(b[8:16]))
		p, err := PointValue(lon, lat)
		if err != nil {
			return Value{}, 0, err
		}
		return p, off + 16, nil
	case KindBox:
		b, err := encoding.ReadBytes(raw, off, 32)
		if err != nil {
			return Value{}, 0, err
		}
		var box [4]float64
		for i := 0; i < 4; i++ {
			box[i] = math.Float64frombits(binary.LittleEndian.Uint64(b[i*8 : i*8+8]))
		}
		bv, err := BoxValue(box[0], box[1], box[2], box[3])
		if err != nil {
			return Value{}, 0, err
		}
		return bv, off + 32, nil
	case KindLine, KindPolygon:
		return decodeGeo(raw, off, t)
	case KindStruct, KindArray, KindMap:
		return decodeCollection(raw, off, t)
	case KindGeometry, KindGeography:
		return decodeGeneralGeo(raw, off, t)
	default:
		return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeScalar", "unsupported type")
	}
}

func encodeBytes(b []byte) []byte {
	out := make([]byte, 4+len(b))
	encoding.PutU32(out, 0, uint32(len(b)))
	copy(out[4:], b)
	return out
}

func decodeBytes(raw []byte, off int) ([]byte, int, error) {
	n, err := encoding.ReadU32(raw, off)
	if err != nil {
		return nil, 0, err
	}
	b, err := encoding.ReadBytes(raw, off+4, int(n))
	if err != nil {
		return nil, 0, err
	}
	return b, off + 4 + int(n), nil
}

// EncodeKey writes a sortable clustered / secondary key from column values.
func EncodeKey(vals []Value) ([]byte, error) {
	var out []byte
	for _, v := range vals {
		part, err := encodeKeyPart(v)
		if err != nil {
			return nil, err
		}
		out = append(out, part...)
	}
	if len(out) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "types.EncodeKey", "empty key")
	}
	return out, nil
}

func encodeKeyPart(v Value) ([]byte, error) {
	if v.Null {
		return []byte{0}, nil
	}
	body, err := encodeSortable(v)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 1+len(body))
	out[0] = 1
	copy(out[1:], body)
	return out, nil
}

func encodeSortable(v Value) ([]byte, error) {
	switch v.Typ.Kind {
	case KindUUID:
		return append([]byte(nil), v.UUID[:]...), nil
	case KindString, KindText, KindBlob, KindChar, KindVarchar:
		// CHAR values are already space-padded to exactly n runes at Coerce
		// time, so the zero-escaped byte-lexicographic order over the stored
		// bytes is the canonical CHAR(n) order (docs/design-datatypes.md D4).
		return encodeSortableBytes([]byte(v.Str)), nil
	case KindJSON:
		return encodeSortableBytes(v.JSON), nil
	case KindDecimal:
		return encodeSortableDecimal(v.Dec)
	case KindTimestampTZ, KindTimestamp:
		// Bias so unsigned compare matches signed time (a naive TIMESTAMP can
		// also be negative for pre-1970 civil times — same flip as TIMESTAMPTZ).
		u := uint64(v.Time) ^ (1 << 63)
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, u)
		return buf, nil
	case KindEnum:
		// Declaration-order: plain unsigned big-endian ordinal, no flip (the
		// ordinal is always >= 0), mirroring UINT16 (docs/design-datatypes.md D11).
		buf := make([]byte, 2)
		binary.BigEndian.PutUint16(buf, uint16(v.Int))
		return buf, nil
	case KindFloat32:
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, sortableFloat32Bits(v.Flt))
		return buf, nil
	case KindFloat64:
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, sortableFloat64Bits(v.Flt))
		return buf, nil
	case KindInt8:
		// Flip the sign bit so unsigned byte compare matches signed order
		// (two's-complement byte order alone sorts negatives after
		// positives — see docs/design-datatypes.md D2).
		return []byte{byte(v.Int) ^ 0x80}, nil
	case KindInt16:
		u := uint16(int16(v.Int)) ^ (1 << 15)
		buf := make([]byte, 2)
		binary.BigEndian.PutUint16(buf, u)
		return buf, nil
	case KindInt32:
		u := uint32(int32(v.Int)) ^ (1 << 31)
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, u)
		return buf, nil
	case KindInt64:
		u := uint64(v.Int) ^ (1 << 63)
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, u)
		return buf, nil
	case KindUint8:
		// No sign-bit flip needed — plain unsigned byte order already sorts
		// correctly (see docs/design-datatypes.md D3).
		return []byte{byte(v.Uint)}, nil
	case KindUint16:
		buf := make([]byte, 2)
		binary.BigEndian.PutUint16(buf, uint16(v.Uint))
		return buf, nil
	case KindUint32:
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, uint32(v.Uint))
		return buf, nil
	case KindUint64:
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, v.Uint)
		return buf, nil
	case KindDate:
		// Signed day count, same sign-bit-flip trick as KindInt32 (see
		// docs/design-datatypes.md D5).
		u := uint32(int32(v.Int)) ^ (1 << 31)
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, u)
		return buf, nil
	case KindTime:
		// Nanoseconds-since-midnight is always non-negative, so plain
		// unsigned byte order already sorts correctly — no sign-bit flip
		// needed (mirrors KindUint64, see docs/design-datatypes.md D5).
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, uint64(v.Time))
		return buf, nil
	case KindInterval:
		// Sortable key is the justified total (Cmp's own ordering, see
		// justifiedNanos in value.go), sign-bit-flipped — NOT a
		// field-by-field encoding of (months, days, nanos), which would not
		// match Cmp's order (docs/design-datatypes.md D6: `1 month` sorts
		// identically to `30 days`). Consequence: decodeSortable below
		// cannot recover the exact original (months, days) split for an
		// index-only-scan reconstruction — it returns a canonical (0 months,
		// N days, remainder nanos) value with the same justified total
		// instead. A plain heap scan (encodeScalar/decodeScalar above)
		// always returns the exact original value; only the sortable-key
		// path canonicalizes, the same class of deliberate, documented
		// canonicalization as FLOAT's -0.0 -> +0.0 (docs/design-datatypes.md D8).
		j, err := justifiedNanos(v.IntervalMonths, v.IntervalDays, v.Time)
		if err != nil {
			return nil, err
		}
		u := uint64(j) ^ (1 << 63)
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, u)
		return buf, nil
	case KindVector:
		buf := make([]byte, 2+4*len(v.Vec))
		encoding.PutU16(buf, 0, uint16(len(v.Vec)))
		PutF32s(buf[2:], v.Vec)
		return buf, nil
	case KindBool:
		if v.Bool {
			return []byte{1}, nil
		}
		return []byte{0}, nil
	case KindPoint:
		out := append(encodeSortableF64(v.Lon), encodeSortableF64(v.Lat)...)
		return out, nil
	case KindBox:
		var out []byte
		for i := 0; i < 4; i++ {
			out = append(out, encodeSortableF64(v.Box[i])...)
		}
		return out, nil
	case KindLine, KindPolygon:
		return encodeSortableGeo(v)
	case KindStruct, KindArray, KindMap:
		return encodeSortableCollection(v)
	case KindGeometry, KindGeography:
		return encodeSortableGeneralGeo(v)
	default:
		return nil, nerr.New(nerr.InvalidArgument, "types.encodeSortable", "unsupported key type")
	}
}

// Collection sortable-key framing bytes. Each member is introduced by a marker
// so a shorter collection that is a prefix of a longer one sorts first (proper
// lexicographic tuple order), and NULL members sort before present ones:
//
//	0x00  end of members
//	0x01  a NULL member (no payload)
//	0x02  a present member, followed by its own type-directed sortable bytes
//
// 0x00 < 0x01 < 0x02, so end-of-list < null < present, which is exactly the
// order Value.Cmp implements for collections. Decoding is type-directed (every
// scalar sortable form is self-delimiting given its Type), so no escaping of
// member payloads is needed.
const (
	collSortEnd     = 0x00
	collSortNull    = 0x01
	collSortPresent = 0x02
)

func encodeSortableCollection(v Value) ([]byte, error) {
	members, err := collectionMembers(v)
	if err != nil {
		return nil, err
	}
	var out []byte
	for _, m := range members {
		if m.Null {
			out = append(out, collSortNull)
			continue
		}
		body, err := encodeSortable(m)
		if err != nil {
			return nil, err
		}
		out = append(out, collSortPresent)
		out = append(out, body...)
	}
	out = append(out, collSortEnd)
	return out, nil
}

func decodeSortableCollection(raw []byte, off int, t Type) (Value, int, error) {
	// nextMemberType yields the type for member i: field types for STRUCT
	// (bounded by field count), the element type for ARRAY, alternating
	// key/value for MAP.
	nextMemberType := func(i int) (Type, bool) {
		switch t.Kind {
		case KindStruct:
			if i >= len(t.Fields) {
				return Type{}, false
			}
			return t.Fields[i].Type, true
		case KindArray:
			if len(t.Elem) != 1 {
				return Type{}, false
			}
			return t.Elem[0], true
		case KindMap:
			if len(t.Key) != 1 || len(t.Elem) != 1 {
				return Type{}, false
			}
			if i%2 == 0 {
				return t.Key[0], true
			}
			return t.Elem[0], true
		default:
			return Type{}, false
		}
	}
	var members []Value
	i := 0
	for {
		if off >= len(raw) {
			return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeSortableCollection", "unterminated collection key")
		}
		marker := raw[off]
		off++
		if marker == collSortEnd {
			break
		}
		if i >= 2*MaxCollectionLen+2 {
			return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeSortableCollection", "collection key too long")
		}
		mt, ok := nextMemberType(i)
		if !ok {
			return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeSortableCollection", "collection key has too many members")
		}
		switch marker {
		case collSortNull:
			members = append(members, Null(mt))
		case collSortPresent:
			mv, next, err := decodeSortable(raw, off, mt)
			if err != nil {
				return Value{}, 0, err
			}
			off = next
			members = append(members, mv)
		default:
			return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeSortableCollection", "bad collection key marker")
		}
		i++
	}
	if t.Kind == KindStruct && len(members) != len(t.Fields) {
		return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeSortableCollection", "STRUCT key field count mismatch")
	}
	if t.Kind == KindMap && len(members)%2 != 0 {
		return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeSortableCollection", "MAP key has an unpaired entry")
	}
	return assembleCollection(t, members), off, nil
}

// sortableFloat64Bits maps a float64 to a uint64 whose unsigned big-endian
// byte order is the canonical float total order (docs/design-datatypes.md D8):
// -Inf < negative reals < ±0 < positive reals < +Inf < NaN. -0.0 and NaN are
// already canonicalized on write; this also folds them defensively here.
func sortableFloat64Bits(f float64) uint64 {
	if math.IsNaN(f) {
		return ^uint64(0)
	}
	if f == 0 {
		f = 0 // -0 -> +0
	}
	b := math.Float64bits(f)
	if b&(1<<63) != 0 {
		return ^b
	}
	return b ^ (1 << 63)
}

func decodeSortableFloat64(u uint64) float64 {
	if u == ^uint64(0) {
		return math.NaN()
	}
	if u&(1<<63) != 0 {
		return math.Float64frombits(u ^ (1 << 63))
	}
	return math.Float64frombits(^u)
}

// sortableFloat32Bits is sortableFloat64Bits at 32-bit width.
func sortableFloat32Bits(f float64) uint32 {
	if math.IsNaN(f) {
		return ^uint32(0)
	}
	g := float32(f)
	if g == 0 {
		g = 0
	}
	b := math.Float32bits(g)
	if b&(1<<31) != 0 {
		return ^b
	}
	return b ^ (1 << 31)
}

func decodeSortableFloat32(u uint32) float64 {
	if u == ^uint32(0) {
		return math.NaN()
	}
	if u&(1<<31) != 0 {
		return float64(math.Float32frombits(u ^ (1 << 31)))
	}
	return float64(math.Float32frombits(^u))
}

func encodeSortableF64(f float64) []byte {
	u := math.Float64bits(f)
	if f < 0 {
		u = ^u
	} else {
		u ^= 1 << 63
	}
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, u)
	return buf
}

func decodeSortableF64(raw []byte, off int) (float64, int, error) {
	b, err := encoding.ReadBytes(raw, off, 8)
	if err != nil {
		return 0, 0, err
	}
	u := binary.BigEndian.Uint64(b)
	if u&(1<<63) != 0 {
		u ^= 1 << 63
	} else {
		u = ^u
	}
	return math.Float64frombits(u), off + 8, nil
}

func encodeSortableBytes(b []byte) []byte {
	out := make([]byte, 0, len(b)+2)
	for _, c := range b {
		if c == 0 {
			out = append(out, 0, 0xFF)
			continue
		}
		out = append(out, c)
	}
	out = append(out, 0, 0)
	return out
}

func encodeSortableDecimal(d Decimal) ([]byte, error) {
	if d.Coef == nil || d.Coef.Sign() == 0 {
		return []byte{1, 0}, nil
	}
	sign := byte(2)
	neg := d.Coef.Sign() < 0
	if neg {
		sign = 0
	}
	abs := d.Coef.Bytes()
	if neg {
		// invert so larger magnitude sorts first among negatives
		inv := make([]byte, len(abs))
		for i := range abs {
			inv[i] = ^abs[i]
		}
		abs = inv
	}
	// Length is stored so DecodeKey can split composite keys. Negatives
	// store 0xFFFF-len so a longer magnitude still sorts first.
	encLen := uint16(len(abs))
	if neg {
		encLen = 0xFFFF - encLen
	}
	buf := make([]byte, 5+len(abs))
	buf[0] = sign
	encoding.PutU16(buf, 1, uint16(d.Scale))
	encoding.PutU16(buf, 3, encLen)
	copy(buf[5:], abs)
	return buf, nil
}

// PrefixEnd is the exclusive upper bound of every key that starts with p.
// A nil result means the prefix has no finite successor (unbounded).
func PrefixEnd(p []byte) []byte {
	if len(p) == 0 {
		return nil
	}
	out := append([]byte(nil), p...)
	for i := len(out) - 1; i >= 0; i-- {
		if out[i] != 0xFF {
			out[i]++
			return out[:i+1]
		}
	}
	return nil
}

// DecodeKey reads values previously written by EncodeKey.
func DecodeKey(raw []byte, cols []Type) ([]Value, error) {
	if len(cols) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "types.DecodeKey", "empty key")
	}
	out := make([]Value, len(cols))
	off := 0
	for i, t := range cols {
		if off >= len(raw) {
			return nil, nerr.New(nerr.InvalidFormat, "types.DecodeKey", "truncated key")
		}
		if raw[off] == 0 {
			out[i] = Null(t)
			off++
			continue
		}
		if raw[off] != 1 {
			return nil, nerr.New(nerr.InvalidFormat, "types.DecodeKey", "bad key tag")
		}
		off++
		v, next, err := decodeSortable(raw, off, t)
		if err != nil {
			return nil, err
		}
		off = next
		out[i] = v
	}
	return out, nil
}

func decodeSortable(raw []byte, off int, t Type) (Value, int, error) {
	switch t.Kind {
	case KindUUID:
		b, err := encoding.ReadBytes(raw, off, 16)
		if err != nil {
			return Value{}, 0, err
		}
		var u [16]byte
		copy(u[:], b)
		return UUIDValue(u), off + 16, nil
	case KindString, KindText, KindBlob, KindJSON, KindChar, KindVarchar:
		b, next, err := decodeSortableBytes(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		if t.Kind == KindJSON {
			return JSONValue(b), next, nil
		}
		v := Value{Typ: t, Str: string(b)}
		return v, next, nil
	case KindDecimal:
		return decodeSortableDecimal(raw, off, t)
	case KindTimestampTZ:
		b, err := encoding.ReadBytes(raw, off, 8)
		if err != nil {
			return Value{}, 0, err
		}
		u := binary.BigEndian.Uint64(b) ^ (1 << 63)
		return TimeValue(int64(u)), off + 8, nil
	case KindTimestamp:
		b, err := encoding.ReadBytes(raw, off, 8)
		if err != nil {
			return Value{}, 0, err
		}
		u := binary.BigEndian.Uint64(b) ^ (1 << 63)
		return NaiveTimestampValue(int64(u)), off + 8, nil
	case KindEnum:
		b, err := encoding.ReadBytes(raw, off, 2)
		if err != nil {
			return Value{}, 0, err
		}
		v, err := EnumValueByOrdinal(int(binary.BigEndian.Uint16(b)), t)
		if err != nil {
			return Value{}, 0, err
		}
		return v, off + 2, nil
	case KindFloat32:
		b, err := encoding.ReadBytes(raw, off, 4)
		if err != nil {
			return Value{}, 0, err
		}
		return Float32Value(decodeSortableFloat32(binary.BigEndian.Uint32(b))), off + 4, nil
	case KindFloat64:
		b, err := encoding.ReadBytes(raw, off, 8)
		if err != nil {
			return Value{}, 0, err
		}
		return Float64Value(decodeSortableFloat64(binary.BigEndian.Uint64(b))), off + 8, nil
	case KindInt8:
		b, err := encoding.ReadBytes(raw, off, 1)
		if err != nil {
			return Value{}, 0, err
		}
		return IntValue(KindInt8, int64(int8(b[0]^0x80))), off + 1, nil
	case KindInt16:
		b, err := encoding.ReadBytes(raw, off, 2)
		if err != nil {
			return Value{}, 0, err
		}
		u := binary.BigEndian.Uint16(b) ^ (1 << 15)
		return IntValue(KindInt16, int64(int16(u))), off + 2, nil
	case KindInt32:
		b, err := encoding.ReadBytes(raw, off, 4)
		if err != nil {
			return Value{}, 0, err
		}
		u := binary.BigEndian.Uint32(b) ^ (1 << 31)
		return IntValue(KindInt32, int64(int32(u))), off + 4, nil
	case KindInt64:
		b, err := encoding.ReadBytes(raw, off, 8)
		if err != nil {
			return Value{}, 0, err
		}
		u := binary.BigEndian.Uint64(b) ^ (1 << 63)
		return IntValue(KindInt64, int64(u)), off + 8, nil
	case KindUint8:
		b, err := encoding.ReadBytes(raw, off, 1)
		if err != nil {
			return Value{}, 0, err
		}
		return UintValue(KindUint8, uint64(b[0])), off + 1, nil
	case KindUint16:
		b, err := encoding.ReadBytes(raw, off, 2)
		if err != nil {
			return Value{}, 0, err
		}
		return UintValue(KindUint16, uint64(binary.BigEndian.Uint16(b))), off + 2, nil
	case KindUint32:
		b, err := encoding.ReadBytes(raw, off, 4)
		if err != nil {
			return Value{}, 0, err
		}
		return UintValue(KindUint32, uint64(binary.BigEndian.Uint32(b))), off + 4, nil
	case KindUint64:
		b, err := encoding.ReadBytes(raw, off, 8)
		if err != nil {
			return Value{}, 0, err
		}
		return UintValue(KindUint64, binary.BigEndian.Uint64(b)), off + 8, nil
	case KindDate:
		b, err := encoding.ReadBytes(raw, off, 4)
		if err != nil {
			return Value{}, 0, err
		}
		u := binary.BigEndian.Uint32(b) ^ (1 << 31)
		return DateValue(int32(u)), off + 4, nil
	case KindTime:
		b, err := encoding.ReadBytes(raw, off, 8)
		if err != nil {
			return Value{}, 0, err
		}
		return TimeOfDayValue(int64(binary.BigEndian.Uint64(b))), off + 8, nil
	case KindInterval:
		// Canonical (0 months, N days, remainder nanos) reconstruction from
		// the justified total — see the canonicalization note in
		// encodeSortable above (docs/design-datatypes.md D6).
		b, err := encoding.ReadBytes(raw, off, 8)
		if err != nil {
			return Value{}, 0, err
		}
		j := int64(binary.BigEndian.Uint64(b) ^ (1 << 63))
		const dayNanos = int64(86400_000_000_000)
		days := j / dayNanos
		rem := j % dayNanos
		if rem < 0 {
			rem += dayNanos
			days--
		}
		if days < math.MinInt32 || days > math.MaxInt32 {
			return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.decodeSortable", "interval day count out of range")
		}
		return IntervalValue(0, int32(days), rem), off + 8, nil
	case KindVector:
		dim, err := encoding.ReadU16(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		need := int(dim) * 4
		b, err := encoding.ReadBytes(raw, off+2, need)
		if err != nil {
			return Value{}, 0, err
		}
		return VectorValue(F32s(b), t), off + 2 + need, nil
	case KindBool:
		if off >= len(raw) {
			return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.DecodeKey", "truncated bool")
		}
		return BoolValue(raw[off] != 0), off + 1, nil
	case KindPoint:
		lon, off2, err := decodeSortableF64(raw, off)
		if err != nil {
			return Value{}, 0, err
		}
		lat, off3, err := decodeSortableF64(raw, off2)
		if err != nil {
			return Value{}, 0, err
		}
		p, err := PointValue(lon, lat)
		if err != nil {
			return Value{}, 0, err
		}
		return p, off3, nil
	case KindBox:
		var box [4]float64
		next := off
		for i := 0; i < 4; i++ {
			var err error
			box[i], next, err = decodeSortableF64(raw, next)
			if err != nil {
				return Value{}, 0, err
			}
		}
		bv, err := BoxValue(box[0], box[1], box[2], box[3])
		if err != nil {
			return Value{}, 0, err
		}
		return bv, next, nil
	case KindLine, KindPolygon:
		return decodeSortableGeo(raw, off, t)
	case KindStruct, KindArray, KindMap:
		return decodeSortableCollection(raw, off, t)
	case KindGeometry, KindGeography:
		return decodeSortableGeneralGeo(raw, off, t)
	default:
		return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.DecodeKey", "unsupported key type")
	}
}

func decodeSortableBytes(raw []byte, off int) ([]byte, int, error) {
	var out []byte
	i := off
	for i < len(raw) {
		if raw[i] != 0 {
			out = append(out, raw[i])
			i++
			continue
		}
		if i+1 >= len(raw) {
			return nil, 0, nerr.New(nerr.InvalidFormat, "types.DecodeKey", "truncated bytes")
		}
		switch raw[i+1] {
		case 0:
			return out, i + 2, nil
		case 0xFF:
			out = append(out, 0)
			i += 2
		default:
			return nil, 0, nerr.New(nerr.InvalidFormat, "types.DecodeKey", "bad byte escape")
		}
	}
	return nil, 0, nerr.New(nerr.InvalidFormat, "types.DecodeKey", "unterminated bytes")
}

func decodeSortableDecimal(raw []byte, off int, t Type) (Value, int, error) {
	if off >= len(raw) {
		return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.DecodeKey", "truncated decimal")
	}
	sign := raw[off]
	if sign == 1 {
		if off+1 >= len(raw) {
			return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.DecodeKey", "truncated zero decimal")
		}
		return DecimalValue(Decimal{Coef: new(big.Int), Scale: 0}, t), off + 2, nil
	}
	if sign != 0 && sign != 2 {
		return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.DecodeKey", "bad decimal sign")
	}
	if off+5 > len(raw) {
		return Value{}, 0, nerr.New(nerr.InvalidFormat, "types.DecodeKey", "truncated decimal header")
	}
	scale := encoding.U16(raw, off+1)
	encLen := encoding.U16(raw, off+3)
	n := int(encLen)
	if sign == 0 {
		n = int(0xFFFF - encLen)
	}
	body, err := encoding.ReadBytes(raw, off+5, n)
	if err != nil {
		return Value{}, 0, err
	}
	abs := body
	if sign == 0 {
		inv := make([]byte, len(body))
		for i := range body {
			inv[i] = ^body[i]
		}
		abs = inv
	}
	d := Decimal{Scale: int(scale), Coef: new(big.Int).SetBytes(abs)}
	if sign == 0 && d.Coef.Sign() != 0 {
		d.Coef.Neg(d.Coef)
	}
	return DecimalValue(d, t), off + 5 + n, nil
}

func EncodeF32Bits(f float32) uint32 { return math.Float32bits(f) }
