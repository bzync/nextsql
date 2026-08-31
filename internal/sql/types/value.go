package types

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/bitvec"
	"github.com/bzync/nextsql/internal/float16"
	"github.com/bzync/nextsql/internal/int8vec"
	nsjson "github.com/bzync/nextsql/internal/json"
	"github.com/bzync/nextsql/internal/nerr"
)

// Value is a runtime SQL value.
type Value struct {
	Typ    Type
	Null   bool
	UUID   [16]byte
	Str    string
	Dec    Decimal
	Time   int64 // UTC unix nanoseconds
	JSON   []byte
	Vec       []float32
	VecRef    bool     // payload lives in the table vector store
	SparseIdx []uint32 // SPARSEVECTOR non-zero indices; unused for dense vectors
	SparseVal []float32
	Bool      bool
	Lon    float64    // POINT longitude
	Lat    float64    // POINT latitude
	Box    [4]float64 // BOX west, south, east, north
	Coords []float64  // LINESTRING / POLYGON interleaved lon, lat
	Rings  []int      // POLYGON vertex counts per ring (includes closing vertex)
}

func Null(t Type) Value { return Value{Typ: t, Null: true} }

func UUIDValue(u [16]byte) Value { return Value{Typ: UUID(), UUID: u} }

func StringValue(s string) Value { return Value{Typ: String(), Str: s} }

func TextValue(s string) Value { return Value{Typ: Text(), Str: s} }

func DecimalValue(d Decimal, t Type) Value { return Value{Typ: t, Dec: d} }

func TimeValue(ns int64) Value { return Value{Typ: TimestampTZ(), Time: ns} }

func JSONValue(b []byte) Value { return Value{Typ: JSON(), JSON: append([]byte(nil), b...)} }

func VectorValue(v []float32, t Type) Value {
	cp := make([]float32, len(v))
	copy(cp, v)
	return Value{Typ: t, Vec: cp}
}

// SparseValue is a SPARSEVECTOR runtime value: parallel index/value lists of
// the non-zero coordinates. Indices must be strictly ascending, below t.Precision,
// and values finite and non-zero; use Coerce or ValidateSparse to enforce that.
func SparseValue(idx []uint32, val []float32, t Type) Value {
	return Value{
		Typ:       t,
		SparseIdx: append([]uint32(nil), idx...),
		SparseVal: append([]float32(nil), val...),
	}
}

// VectorRef is a heap-row placeholder: dimension is known, floats are not inline.
func VectorRef(t Type) Value {
	return Value{Typ: t, VecRef: true}
}

// ValidateVector rejects NaN and Inf elements.
func ValidateVector(v []float32) error {
	for _, f := range v {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return nerr.New(nerr.InvalidArgument, "types.ValidateVector", "VECTOR element is not finite")
		}
	}
	return nil
}

// ValidateSparse rejects a malformed SPARSEVECTOR: length mismatch, too many
// non-zeros, an index at or above dim, an out-of-order or duplicate index, or a
// zero / non-finite value.
func ValidateSparse(dim uint32, idx []uint32, val []float32) error {
	if dim == 0 || dim > uint32(MaxSparseSQLDim) {
		return nerr.New(nerr.InvalidArgument, "types.ValidateSparse", "SPARSEVECTOR dimension out of range")
	}
	if len(idx) != len(val) {
		return nerr.New(nerr.InvalidArgument, "types.ValidateSparse", "sparse index/value length mismatch")
	}
	if len(idx) > MaxSparseSQLNNZ {
		return nerr.New(nerr.InvalidArgument, "types.ValidateSparse", "too many non-zero coordinates")
	}
	for i, ix := range idx {
		if ix >= dim {
			return nerr.New(nerr.InvalidArgument, "types.ValidateSparse", "sparse index out of range")
		}
		if i > 0 && idx[i-1] >= ix {
			return nerr.New(nerr.InvalidArgument, "types.ValidateSparse", "sparse indices not strictly ascending")
		}
		f := val[i]
		if f == 0 {
			return nerr.New(nerr.InvalidArgument, "types.ValidateSparse", "sparse value is zero")
		}
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return nerr.New(nerr.InvalidArgument, "types.ValidateSparse", "sparse value is not finite")
		}
	}
	return nil
}

// DenseToSparse drops zeros from a dense vector. Non-finite values fail closed.
func DenseToSparse(vec []float32) (idx []uint32, val []float32, err error) {
	if err := ValidateVector(vec); err != nil {
		return nil, nil, err
	}
	for i, f := range vec {
		if f == 0 {
			continue
		}
		idx = append(idx, uint32(i))
		val = append(val, f)
	}
	if len(idx) > MaxSparseSQLNNZ {
		return nil, nil, nerr.New(nerr.InvalidArgument, "types.DenseToSparse", "too many non-zero coordinates")
	}
	return idx, val, nil
}

// DecimalFromFloat stores f with 8 decimal places.
func DecimalFromFloat(f float64) (Value, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.DecimalFromFloat", "not finite")
	}
	s := strconv.FormatFloat(f, 'f', 8, 64)
	d, err := ParseDecimal(s)
	if err != nil {
		return Value{}, err
	}
	return DecimalValue(d, Type{Kind: KindDecimal, Precision: 38, Scale: 8}), nil
}

func BoolValue(b bool) Value { return Value{Typ: Bool(), Bool: b} }

// Clone returns a deep copy of v.
func (v Value) Clone() Value {
	if v.JSON != nil {
		v.JSON = append([]byte(nil), v.JSON...)
	}
	if v.Vec != nil {
		v.Vec = append([]float32(nil), v.Vec...)
	}
	if v.SparseIdx != nil {
		v.SparseIdx = append([]uint32(nil), v.SparseIdx...)
	}
	if v.SparseVal != nil {
		v.SparseVal = append([]float32(nil), v.SparseVal...)
	}
	if v.Dec.Coef != nil {
		v.Dec = v.Dec.Clone()
	}
	if v.Coords != nil {
		v.Coords = append([]float64(nil), v.Coords...)
	}
	if v.Rings != nil {
		v.Rings = append([]int(nil), v.Rings...)
	}
	return v
}

func NewUUID() (Value, error) {
	var u [16]byte
	if _, err := rand.Read(u[:]); err != nil {
		return Value{}, nerr.Wrap(nerr.Internal, "types.NewUUID", "random", err)
	}
	u[6] = (u[6] & 0x0f) | 0x40
	u[8] = (u[8] & 0x3f) | 0x80
	return UUIDValue(u), nil
}

func Now() Value { return TimeValue(time.Now().UTC().UnixNano()) }

func ParseUUID(s string) (Value, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "-", "")
	if len(s) != 32 {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseUUID", "invalid UUID")
	}
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != 16 {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseUUID", "invalid UUID")
	}
	var u [16]byte
	copy(u[:], raw)
	return UUIDValue(u), nil
}

func FormatUUID(u [16]byte) string {
	s := hex.EncodeToString(u[:])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

func ParseTimestamp(s string) (Value, error) {
	s = strings.TrimSpace(s)
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, l := range layouts {
		if ts, err := time.Parse(l, s); err == nil {
			return TimeValue(ts.UTC().UnixNano()), nil
		}
	}
	return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseTimestamp", "invalid timestamptz")
}

func (v Value) String() string {
	if v.Null {
		return "NULL"
	}
	switch v.Typ.Kind {
	case KindUUID:
		return FormatUUID(v.UUID)
	case KindString, KindText:
		return v.Str
	case KindDecimal:
		return v.Dec.String()
	case KindTimestampTZ:
		return time.Unix(0, v.Time).UTC().Format(time.RFC3339Nano)
	case KindJSON:
		if v.JSON == nil {
			return "null"
		}
		txt, err := nsjson.ToText(v.JSON)
		if err != nil {
			return "?"
		}
		return string(txt)
	case KindVector:
		if v.Typ.VecElem == VecSparse || len(v.SparseIdx) > 0 {
			var b strings.Builder
			b.WriteByte('{')
			for i, idx := range v.SparseIdx {
				if i > 0 {
					b.WriteByte(',')
				}
				b.WriteString(strconv.FormatUint(uint64(idx), 10))
				b.WriteByte(':')
				var f float32
				if i < len(v.SparseVal) {
					f = v.SparseVal[i]
				}
				b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
			}
			b.WriteByte('}')
			return b.String()
		}
		var b strings.Builder
		b.WriteByte('[')
		for i, f := range v.Vec {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
		}
		b.WriteByte(']')
		return b.String()
	case KindBool:
		if v.Bool {
			return "TRUE"
		}
		return "FALSE"
	case KindPoint:
		return formatPoint(v.Lon, v.Lat)
	case KindBox:
		return formatBox(v.Box)
	case KindLine:
		return formatLine(v.Coords)
	case KindPolygon:
		return formatPolygon(v.Coords, v.Rings)
	default:
		return "?"
	}
}

func (v Value) Cmp(o Value) (int, error) {
	if v.Null || o.Null {
		return 0, nerr.New(nerr.InvalidArgument, "types.Value.Cmp", "NULL comparison")
	}
	if v.Typ.Kind != o.Typ.Kind && !stringish(v.Typ.Kind, o.Typ.Kind) {
		return 0, nerr.New(nerr.InvalidArgument, "types.Value.Cmp", "type mismatch")
	}
	switch v.Typ.Kind {
	case KindUUID:
		return bytes.Compare(v.UUID[:], o.UUID[:]), nil
	case KindString, KindText:
		return strings.Compare(v.Str, o.Str), nil
	case KindDecimal:
		return v.Dec.Cmp(o.Dec), nil
	case KindTimestampTZ:
		switch {
		case v.Time < o.Time:
			return -1, nil
		case v.Time > o.Time:
			return 1, nil
		default:
			return 0, nil
		}
	case KindBool:
		switch {
		case !v.Bool && o.Bool:
			return -1, nil
		case v.Bool && !o.Bool:
			return 1, nil
		default:
			return 0, nil
		}
	case KindJSON:
		return bytes.Compare(v.JSON, o.JSON), nil
	case KindPoint:
		if v.Lon < o.Lon {
			return -1, nil
		}
		if v.Lon > o.Lon {
			return 1, nil
		}
		switch {
		case v.Lat < o.Lat:
			return -1, nil
		case v.Lat > o.Lat:
			return 1, nil
		default:
			return 0, nil
		}
	case KindBox:
		for i := 0; i < 4; i++ {
			if v.Box[i] < o.Box[i] {
				return -1, nil
			}
			if v.Box[i] > o.Box[i] {
				return 1, nil
			}
		}
		return 0, nil
	case KindLine, KindPolygon:
		if n := len(v.Rings) - len(o.Rings); n != 0 {
			if n < 0 {
				return -1, nil
			}
			return 1, nil
		}
		for i := range v.Rings {
			if v.Rings[i] < o.Rings[i] {
				return -1, nil
			}
			if v.Rings[i] > o.Rings[i] {
				return 1, nil
			}
		}
		n := len(v.Coords)
		if len(o.Coords) < n {
			n = len(o.Coords)
		}
		for i := 0; i < n; i++ {
			if v.Coords[i] < o.Coords[i] {
				return -1, nil
			}
			if v.Coords[i] > o.Coords[i] {
				return 1, nil
			}
		}
		switch {
		case len(v.Coords) < len(o.Coords):
			return -1, nil
		case len(v.Coords) > len(o.Coords):
			return 1, nil
		default:
			return 0, nil
		}
	default:
		return 0, nerr.New(nerr.InvalidArgument, "types.Value.Cmp", "type is not comparable")
	}
}

func stringish(a, b Kind) bool {
	return (a == KindString || a == KindText) && (b == KindString || b == KindText)
}

func Coerce(v Value, dest Type) (Value, error) {
	if v.Null {
		return Null(dest), nil
	}
	if v.Typ.Kind == dest.Kind {
		switch dest.Kind {
		case KindDecimal:
			d, err := v.Dec.Rescale(int(dest.Precision), int(dest.Scale))
			if err != nil {
				return Value{}, err
			}
			return DecimalValue(d, dest), nil
		case KindVector:
			if v.VecRef {
				if dest.Precision != 0 && v.Typ.Precision != dest.Precision {
					return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "VECTOR dimension mismatch")
				}
				if dest.VecElem == VecSparse && v.Typ.VecElem != VecSparse && v.Typ.VecElem != 0 {
					return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "cannot coerce a dense VECTOR reference to SPARSEVECTOR")
				}
				if dest.VecElem != VecSparse && v.Typ.VecElem == VecSparse {
					return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "cannot widen a SPARSEVECTOR to another vector type")
				}
				v.Typ = dest
				return v, nil
			}
			if dest.VecElem == VecSparse {
				return coerceSparse(v, dest)
			}
			if v.Typ.VecElem == VecSparse || len(v.SparseIdx) > 0 {
				return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "cannot widen a SPARSEVECTOR to another vector type")
			}
			if err := ValidateVector(v.Vec); err != nil {
				return Value{}, err
			}
			if uint16(len(v.Vec)) != dest.Precision {
				return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "VECTOR dimension mismatch")
			}
			if dest.VecElem != v.Typ.VecElem {
				switch dest.VecElem {
				case VecF16:
					v.Vec = float16.Quantize(v.Vec)
				case VecI8:
					v.Vec = int8vec.Quantize(v.Vec)
				case VecBit:
					if err := bitvec.Validate(v.Vec); err != nil {
						return Value{}, err
					}
				default:
					if v.Typ.VecElem == VecBit {
						return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "cannot widen a BITVECTOR to another vector type")
					}
				}
			}
			v.Typ = dest
			return v, nil
		case KindJSON:
			if err := nsjson.Validate(v.JSON); err != nil {
				return Value{}, err
			}
			v.Typ = dest
			return v, nil
		default:
			v.Typ = dest
			return v, nil
		}
	}
	switch dest.Kind {
	case KindString, KindText:
		out := dest
		return Value{Typ: out, Str: v.String()}, nil
	case KindUUID:
		if v.Typ.Kind == KindString || v.Typ.Kind == KindText {
			return ParseUUID(v.Str)
		}
	case KindDecimal:
		switch v.Typ.Kind {
		case KindString, KindText:
			d, err := ParseDecimal(v.Str)
			if err != nil {
				return Value{}, err
			}
			d, err = d.Rescale(int(dest.Precision), int(dest.Scale))
			if err != nil {
				return Value{}, err
			}
			return DecimalValue(d, dest), nil
		}
	case KindPoint, KindBox, KindLine, KindPolygon:
		if v.Typ.Kind == KindString || v.Typ.Kind == KindText {
			g, err := ParseWKT(v.Str)
			if err != nil {
				return Value{}, err
			}
			if g.Typ.Kind != dest.Kind {
				return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "cannot coerce "+g.Typ.String()+" to "+dest.String())
			}
			return g, nil
		}
	case KindJSON:
		if v.Typ.Kind == KindString || v.Typ.Kind == KindText {
			return JSONFromText(v.Str)
		}
	}
	return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "cannot coerce "+v.Typ.String()+" to "+dest.String())
}

func coerceSparse(v Value, dest Type) (Value, error) {
	if dest.VecElem != VecSparse || dest.Precision < 1 {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.coerceSparse", "destination is not a SPARSEVECTOR")
	}
	if v.Typ.VecElem == VecBit {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "cannot coerce a BITVECTOR to SPARSEVECTOR")
	}
	if v.Typ.VecElem == VecSparse || len(v.SparseIdx) > 0 {
		if v.Typ.Precision != 0 && v.Typ.Precision != dest.Precision {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "VECTOR dimension mismatch")
		}
		if err := ValidateSparse(uint32(dest.Precision), v.SparseIdx, v.SparseVal); err != nil {
			return Value{}, err
		}
		return SparseValue(v.SparseIdx, v.SparseVal, dest), nil
	}
	if uint16(len(v.Vec)) != dest.Precision {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "VECTOR dimension mismatch")
	}
	idx, val, err := DenseToSparse(v.Vec)
	if err != nil {
		return Value{}, err
	}
	if err := ValidateSparse(uint32(dest.Precision), idx, val); err != nil {
		return Value{}, err
	}
	return SparseValue(idx, val, dest), nil
}

// JSONFromText parses UTF-8 JSON into the compact binary stored form.
func JSONFromText(s string) (Value, error) {
	doc, err := nsjson.FromText([]byte(s))
	if err != nil {
		return Value{}, err
	}
	return JSONValue(doc), nil
}

// ExtractJSON reads a path from a binary JSON document without decoding unused siblings.
func ExtractJSON(doc []byte, path []string) (Value, error) {
	if len(doc) == 0 {
		return Null(JSON()), nil
	}
	r, err := nsjson.Extract(doc, path)
	if err != nil {
		return Value{}, err
	}
	switch r.Kind {
	case nsjson.KindMissing, nsjson.KindNull:
		return Null(JSON()), nil
	case nsjson.KindBool:
		return BoolValue(r.Bool), nil
	case nsjson.KindInt:
		return DecimalValue(Decimal{Coef: big.NewInt(r.Int), Scale: 0}, Type{Kind: KindDecimal}), nil
	case nsjson.KindNumber:
		d, err := ParseDecimal(r.Str)
		if err != nil {
			return Value{}, err
		}
		return DecimalValue(d, Type{Kind: KindDecimal, Scale: uint16(d.Scale)}), nil
	case nsjson.KindString:
		return StringValue(r.Str), nil
	case nsjson.KindArray, nsjson.KindObject:
		return JSONValue(r.Raw), nil
	default:
		return Null(JSON()), nil
	}
}

func EncodeUUID(u [16]byte) []byte { return append([]byte(nil), u[:]...) }

func PutF32s(dst []byte, v []float32) {
	for i, f := range v {
		binary.LittleEndian.PutUint32(dst[i*4:], math.Float32bits(f))
	}
}

func F32s(src []byte) []float32 {
	n := len(src) / 4
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(src[i*4:]))
	}
	return out
}
