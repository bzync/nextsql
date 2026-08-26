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
	case KindString, KindText, KindJSON, KindDecimal:
		n, err := encoding.ReadU32(raw, off)
		if err != nil {
			return 0, err
		}
		end := off + 4 + int(n)
		if end > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated bytes")
		}
		return end, nil
	case KindTimestampTZ:
		if off+8 > len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated timestamp")
		}
		return off + 8, nil
	case KindVector:
		dim, err := encoding.ReadU16(raw, off)
		if err != nil {
			return 0, err
		}
		if off+2 >= len(raw) {
			return 0, nerr.New(nerr.InvalidFormat, "types.skipScalar", "truncated vector flags")
		}
		if raw[off+2]&1 != 0 {
			return off + 3, nil
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
	case KindString, KindText:
		return encodeBytes([]byte(v.Str)), nil
	case KindJSON:
		if err := nsjson.Validate(v.JSON); err != nil {
			return nil, err
		}
		return encodeBytes(v.JSON), nil
	case KindDecimal:
		body := encodeDecimal(v.Dec)
		return encodeBytes(body), nil
	case KindTimestampTZ:
		buf := make([]byte, 8)
		encoding.PutU64(buf, 0, uint64(v.Time))
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
	default:
		return nil, nerr.New(nerr.InvalidArgument, "types.encodeScalar", "unsupported type")
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
	case KindString, KindText:
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
	case KindString, KindText:
		return encodeSortableBytes([]byte(v.Str)), nil
	case KindJSON:
		return encodeSortableBytes(v.JSON), nil
	case KindDecimal:
		return encodeSortableDecimal(v.Dec)
	case KindTimestampTZ:
		// Bias so unsigned compare matches signed time.
		u := uint64(v.Time) ^ (1 << 63)
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
	default:
		return nil, nerr.New(nerr.InvalidArgument, "types.encodeSortable", "unsupported key type")
	}
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
	case KindString, KindText, KindJSON:
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
