package types

import (
	"math"

	"github.com/bzync/nextsql/internal/nerr"
)

// Kind is a catalog / runtime type tag.
type Kind uint8

const (
	KindInvalid Kind = iota
	KindUUID
	KindString
	KindText
	KindDecimal
	KindTimestampTZ
	KindJSON
	KindVector
	KindBool
	KindNull
	KindPoint
	KindBox
	KindLine
	KindPolygon
	// KindBlob is variable-length raw bytes (D1, Datatype expansion track).
	// Appended last so the numeric tag matches across every driver's own
	// Kind table without needing a version bump anywhere it is persisted.
	KindBlob
	// KindInt8/16/32/64 are fixed-width two's-complement signed integers
	// (D2, Datatype expansion track). Appended last for the same reason as
	// KindBlob above — no catalog/protocol version bump needed.
	KindInt8
	KindInt16
	KindInt32
	KindInt64
	// KindUint8/16/32/64 are fixed-width unsigned integers (D3, Datatype
	// expansion track). Appended last for the same reason as KindInt8 above —
	// no catalog/protocol version bump needed.
	KindUint8
	KindUint16
	KindUint32
	KindUint64
	// KindDate is a fixed-width signed day count since the Unix epoch (D5,
	// Datatype expansion track). KindTime is fixed-width nanoseconds since
	// midnight. Appended last for the same reason as KindInt8 above — no
	// catalog/protocol version bump needed.
	KindDate
	KindTime
	// KindChar is a fixed-width, space-padded string CHAR(n) (D4, Datatype
	// expansion track); KindVarchar is a length-capped string VARCHAR(n).
	// Both reuse STRING/TEXT's exact on-disk encoding (u32 byte-length prefix
	// + UTF-8 bytes) with n (a rune count, not byte count) carried in
	// Type.Precision, the same field DECIMAL already uses for a parameter.
	// Appended last for the same reason as KindInt8 above — no
	// catalog/protocol version bump needed.
	KindChar
	KindVarchar
	// KindTimestamp is a plain date-and-time with no timezone (D7, Datatype
	// expansion track): int64 nanoseconds since 1970-01-01T00:00:00, the civil
	// value read literally with no offset applied. Reuses Value.Time, the same
	// field TIMESTAMPTZ/TIME use, disambiguated by Value.Typ.Kind. Deliberately
	// isolated from TIMESTAMPTZ (converting between them needs an assumed zone)
	// — text coercion only, same as DATE/TIME. Appended last: no
	// catalog/protocol version bump needed.
	KindTimestamp
	// KindFloat32/KindFloat64 are IEEE-754 binary floating point (D8, Datatype
	// expansion track). Unlike DECIMAL they are inexact by design — added for
	// interop with external numeric data. Canonical total order for index
	// keys: -Inf < negative reals < 0 < positive reals < +Inf < NaN; -0.0 is
	// canonicalized to +0.0 on write, and all NaN payloads collapse to one
	// value. Stored in Value.Flt. Appended last: no catalog/protocol version
	// bump needed.
	KindFloat32
	KindFloat64
	// KindEnum is a named-label enumeration (D11, Datatype expansion track).
	// Stored on disk as a u16 ordinal into the column's declared label list;
	// ordered by declaration position, NOT lexicographically (the whole point
	// of ENUM). The label list travels on Type.EnumLabels. Unlike every other
	// D-type this needs new per-column catalog metadata, so it DID take a
	// catalog version bump (NSCT v10 -> v11).
	KindEnum
	// KindInterval is a calendar duration (D6, Datatype expansion track):
	// Postgres-style 3-field storage — months (int32) + days (int32) + nanos
	// (int64, time-of-day component) — chosen so that DATE/TIMESTAMP
	// arithmetic can apply calendar-correct month/day steps (clamping to the
	// target month's last day, e.g. Jan 31 + 1 month = Feb 28/29) before
	// falling back to a fixed nanosecond duration for the sub-day remainder.
	// Comparison uses Postgres's own "justified" heuristic (1 month = 30
	// days = 24h) to give intervals a total order despite months/days/nanos
	// being fundamentally different units — this means two intervals that
	// are unequal in their raw fields can compare equal (e.g. `1 month` =
	// `30 days`), matching Postgres's documented behavior exactly, not an
	// approximation of it. Appended last: no catalog/protocol version bump
	// needed (unlike KindEnum, INTERVAL's 3 fields fit in the existing fixed
	// Value shape, no per-column metadata).
	KindInterval
)

func (k Kind) String() string {
	switch k {
	case KindUUID:
		return "UUID"
	case KindString:
		return "STRING"
	case KindText:
		return "TEXT"
	case KindDecimal:
		return "DECIMAL"
	case KindTimestampTZ:
		return "TIMESTAMPTZ"
	case KindJSON:
		return "JSON"
	case KindVector:
		return "VECTOR"
	case KindBool:
		return "BOOL"
	case KindNull:
		return "NULL"
	case KindPoint:
		return "POINT"
	case KindBox:
		return "BOX"
	case KindLine:
		return "LINESTRING"
	case KindPolygon:
		return "POLYGON"
	case KindBlob:
		return "BLOB"
	case KindInt8:
		return "INT8"
	case KindInt16:
		return "INT16"
	case KindInt32:
		return "INT32"
	case KindInt64:
		return "INT64"
	case KindUint8:
		return "UINT8"
	case KindUint16:
		return "UINT16"
	case KindUint32:
		return "UINT32"
	case KindUint64:
		return "UINT64"
	case KindDate:
		return "DATE"
	case KindTime:
		return "TIME"
	case KindChar:
		return "CHAR"
	case KindVarchar:
		return "VARCHAR"
	case KindTimestamp:
		return "TIMESTAMP"
	case KindFloat32:
		return "FLOAT32"
	case KindFloat64:
		return "FLOAT64"
	case KindEnum:
		return "ENUM"
	case KindInterval:
		return "INTERVAL"
	default:
		return "INVALID"
	}
}

// IntRange returns the inclusive representable range of a fixed-width signed
// integer Kind. ok is false for any non-integer Kind.
func IntRange(k Kind) (lo, hi int64, ok bool) {
	switch k {
	case KindInt8:
		return math.MinInt8, math.MaxInt8, true
	case KindInt16:
		return math.MinInt16, math.MaxInt16, true
	case KindInt32:
		return math.MinInt32, math.MaxInt32, true
	case KindInt64:
		return math.MinInt64, math.MaxInt64, true
	default:
		return 0, 0, false
	}
}

// IsInt reports whether k is one of the fixed-width signed integer kinds.
func IsInt(k Kind) bool {
	_, _, ok := IntRange(k)
	return ok
}

// UintRange returns the inclusive representable range of a fixed-width
// unsigned integer Kind. lo is always 0; ok is false for any non-unsigned-int
// Kind.
func UintRange(k Kind) (hi uint64, ok bool) {
	switch k {
	case KindUint8:
		return math.MaxUint8, true
	case KindUint16:
		return math.MaxUint16, true
	case KindUint32:
		return math.MaxUint32, true
	case KindUint64:
		return math.MaxUint64, true
	default:
		return 0, false
	}
}

// IsUint reports whether k is one of the fixed-width unsigned integer kinds.
func IsUint(k Kind) bool {
	_, ok := UintRange(k)
	return ok
}

// Vector element encodings. VecF32 is the original full-precision layout;
// VecF16 (Phase 23) stores each element as an IEEE 754 half on disk; VecI8
// (Phase 23) stores each element as a signed byte with a per-vector scale;
// VecBit (Phase 23) stores each element as a single bit (BITVECTOR<N>);
// VecSparse (Phase 23) stores only non-zero (index, weight) pairs (SPARSEVECTOR<N>).
const (
	VecF32    uint8 = 1
	VecF16    uint8 = 2
	VecI8     uint8 = 3
	VecBit    uint8 = 4
	VecSparse uint8 = 5
)

// VecElemName is the vector element type spelling used inside VECTOR<...>.
// BITVECTOR<N> and SPARSEVECTOR<N> have their own top-level spelling; see Type.String.
func VecElemName(e uint8) string {
	switch e {
	case VecF16:
		return "F16"
	case VecI8:
		return "I8"
	case VecBit:
		return "BIT"
	case VecSparse:
		return "SPARSE"
	default:
		return "F32"
	}
}

// VecElemBytes is the on-disk width of one vector element. I8 also carries a
// per-vector 4-byte scale not counted here; BIT packs 8 elements per byte, so
// its per-element width rounds up to 1.
func VecElemBytes(e uint8) int {
	switch e {
	case VecF16:
		return 2
	case VecI8:
		return 1
	case VecBit:
		return 1
	case VecSparse:
		return 0
	default:
		return 4
	}
}

// VecElemQuantised reports whether e is a compressed encoding whose value is
// re-encoded on write and widened to float32 on read (F16 / I8 quantise, BIT
// packs). Sparse stays sparse at runtime and is not a widening encoding.
func VecElemQuantised(e uint8) bool { return e == VecF16 || e == VecI8 || e == VecBit }

// Type is a catalog column type.
//
// EnumLabels holds an ENUM column's ordered label list (declaration order).
// Because it is a slice, Type is no longer comparable with ==; use Equals.
// Every non-ENUM Type leaves it nil.
type Type struct {
	Kind       Kind
	Precision  uint16 // DECIMAL p or VECTOR dimension; ENUM label count
	Scale      uint16 // DECIMAL s
	VecElem    uint8  // VecF32
	EnumLabels []string
}

// MaxEnumLabels bounds an ENUM's declared label list. The on-disk ordinal is
// a u16, so 65535 is the hard ceiling; the practical limit is lower.
const MaxEnumLabels = 4096

// EnumType builds an ENUM column type from an ordered, non-empty, unique
// label list (docs/design-datatypes.md D11).
func EnumType(labels []string) (Type, error) {
	if len(labels) == 0 || len(labels) > MaxEnumLabels {
		return Type{}, nerr.New(nerr.InvalidArgument, "types.EnumType", "ENUM needs 1..MaxEnumLabels labels")
	}
	seen := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		if l == "" || len(l) > 255 {
			return Type{}, nerr.New(nerr.InvalidArgument, "types.EnumType", "ENUM label length is invalid")
		}
		if _, dup := seen[l]; dup {
			return Type{}, nerr.New(nerr.InvalidArgument, "types.EnumType", "duplicate ENUM label")
		}
		seen[l] = struct{}{}
	}
	cp := append([]string(nil), labels...)
	return Type{Kind: KindEnum, Precision: uint16(len(cp)), EnumLabels: cp}, nil
}

// EnumOrdinal returns the 0-based declaration position of label, or -1.
func (t Type) EnumOrdinal(label string) int {
	for i, l := range t.EnumLabels {
		if l == label {
			return i
		}
	}
	return -1
}

func (t Type) String() string {
	switch t.Kind {
	case KindDecimal:
		return "DECIMAL"
	case KindVector:
		if t.VecElem == VecBit {
			return "BITVECTOR<" + itoa(int(t.Precision)) + ">"
		}
		if t.VecElem == VecSparse {
			return "SPARSEVECTOR<" + itoa(int(t.Precision)) + ">"
		}
		return "VECTOR<" + VecElemName(t.VecElem) + "," + itoa(int(t.Precision)) + ">"
	case KindChar:
		return "CHAR(" + itoa(int(t.Precision)) + ")"
	case KindVarchar:
		return "VARCHAR(" + itoa(int(t.Precision)) + ")"
	case KindEnum:
		out := "ENUM("
		for i, l := range t.EnumLabels {
			if i > 0 {
				out += ", "
			}
			out += "'" + l + "'"
		}
		return out + ")"
	default:
		return t.Kind.String()
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func (t Type) Equals(o Type) bool {
	if t.Kind != o.Kind || t.Precision != o.Precision || t.Scale != o.Scale || t.VecElem != o.VecElem {
		return false
	}
	if len(t.EnumLabels) != len(o.EnumLabels) {
		return false
	}
	for i := range t.EnumLabels {
		if t.EnumLabels[i] != o.EnumLabels[i] {
			return false
		}
	}
	return true
}

func UUID() Type        { return Type{Kind: KindUUID} }
func String() Type      { return Type{Kind: KindString} }
func Text() Type        { return Type{Kind: KindText} }
func TimestampTZ() Type { return Type{Kind: KindTimestampTZ} }
func JSON() Type        { return Type{Kind: KindJSON} }
func Bool() Type        { return Type{Kind: KindBool} }
func NullType() Type    { return Type{Kind: KindNull} }
func Blob() Type        { return Type{Kind: KindBlob} }
func Int8() Type        { return Type{Kind: KindInt8} }
func Int16() Type       { return Type{Kind: KindInt16} }
func Int32() Type       { return Type{Kind: KindInt32} }
func Int64() Type       { return Type{Kind: KindInt64} }
func Uint8() Type       { return Type{Kind: KindUint8} }
func Uint16() Type      { return Type{Kind: KindUint16} }
func Uint32() Type      { return Type{Kind: KindUint32} }
func Uint64() Type      { return Type{Kind: KindUint64} }
func Date() Type        { return Type{Kind: KindDate} }

// TimeOfDay is the TIME (no date component) column type. Named TimeOfDay,
// not Time, to stay distinct from the pre-existing TimeValue/Value.Time
// (TIMESTAMPTZ's UTC-epoch-nanoseconds constructor/field).
func TimeOfDay() Type { return Type{Kind: KindTime} }

// Timestamp is the plain TIMESTAMP (no timezone) column type (D7). Named
// Timestamp, not distinguished by a suffix, with TimestampTZ carrying the
// "with timezone" spelling.
func Timestamp() Type { return Type{Kind: KindTimestamp} }

// Float32 / Float64 are the IEEE-754 binary floating point column types (D8).
func Float32() Type { return Type{Kind: KindFloat32} }
func Float64() Type { return Type{Kind: KindFloat64} }

// IsFloat reports whether k is one of the IEEE-754 floating point kinds.
func IsFloat(k Kind) bool { return k == KindFloat32 || k == KindFloat64 }

// Interval is the calendar-duration column type (D6).
func Interval() Type { return Type{Kind: KindInterval} }

func DecimalType(p, s uint16) (Type, error) {
	if p < 1 || p > 38 || s > p {
		return Type{}, nerr.New(nerr.InvalidArgument, "types.DecimalType", "DECIMAL precision/scale out of range")
	}
	return Type{Kind: KindDecimal, Precision: p, Scale: s}, nil
}

// MaxCharLen bounds CHAR(n)/VARCHAR(n)'s declared length (a rune count, not a
// byte count). Type.Precision is a uint16, so this is also its hard ceiling.
const MaxCharLen = 65535

// CharType is the CHAR(n) column type: fixed-width, space-padded on write,
// always exactly n runes once stored (docs/design-datatypes.md D4).
func CharType(n uint16) (Type, error) {
	if n < 1 || n > MaxCharLen {
		return Type{}, nerr.New(nerr.InvalidArgument, "types.CharType", "CHAR length out of range")
	}
	return Type{Kind: KindChar, Precision: n}, nil
}

// VarcharType is the VARCHAR(n) column type: same encoding as STRING, with a
// declared maximum rune count enforced at write/coercion time
// (docs/design-datatypes.md D4).
func VarcharType(n uint16) (Type, error) {
	if n < 1 || n > MaxCharLen {
		return Type{}, nerr.New(nerr.InvalidArgument, "types.VarcharType", "VARCHAR length out of range")
	}
	return Type{Kind: KindVarchar, Precision: n}, nil
}

// MaxVectorDim is the Phase 11 abuse limit for VECTOR<F32,N>.
const MaxVectorDim = 8192

// MaxSparseSQLDim is the SQL SPARSEVECTOR<N> abuse limit. The portable sparse
// core allows 2^24; the catalog type stores dimension in a u16.
const MaxSparseSQLDim = 65535

// MaxSparseSQLNNZ bounds the number of non-zero coordinates in one SQL sparse
// vector (matches vector.MaxSparseNNZ; duplicated so types does not import it).
const MaxSparseSQLNNZ = 1 << 16

// MaxGeoVertices is the abuse limit for LINESTRING / POLYGON coordinates.
// 256 * 16 bytes fits in a 16 KiB page with room for other columns.
const MaxGeoVertices = 256

func VectorF32(n uint16) (Type, error) { return vectorType(n, VecF32, "types.VectorF32") }

// VectorF16 is the half-precision quantised vector column type (Phase 23).
func VectorF16(n uint16) (Type, error) { return vectorType(n, VecF16, "types.VectorF16") }

// VectorI8 is the signed-8-bit quantised vector column type (Phase 23).
func VectorI8(n uint16) (Type, error) { return vectorType(n, VecI8, "types.VectorI8") }

// VectorBit is the packed single-bit vector column type BITVECTOR<N> (Phase 23).
func VectorBit(n uint16) (Type, error) { return vectorType(n, VecBit, "types.VectorBit") }

// VectorSparse is the sparse vector column type SPARSEVECTOR<N> (Phase 23).
// N is the ambient dimension (vocabulary size), stored as Type.Precision.
func VectorSparse(n uint16) (Type, error) {
	if n < 1 {
		return Type{}, nerr.New(nerr.InvalidArgument, "types.VectorSparse", "SPARSEVECTOR dimension out of range")
	}
	return Type{Kind: KindVector, Precision: n, VecElem: VecSparse}, nil
}

// IsSparse reports whether t is a SPARSEVECTOR column.
func (t Type) IsSparse() bool { return t.Kind == KindVector && t.VecElem == VecSparse }

func vectorType(n uint16, elem uint8, op string) (Type, error) {
	if n < 1 || n > MaxVectorDim {
		return Type{}, nerr.New(nerr.InvalidArgument, op, "VECTOR dimension out of range")
	}
	return Type{Kind: KindVector, Precision: n, VecElem: elem}, nil
}

func (t Type) Comparable() bool {
	switch t.Kind {
	case KindUUID, KindString, KindText, KindDecimal, KindTimestampTZ, KindBool, KindPoint, KindBox, KindLine, KindPolygon, KindBlob,
		KindInt8, KindInt16, KindInt32, KindInt64, KindUint8, KindUint16, KindUint32, KindUint64, KindDate, KindTime,
		KindChar, KindVarchar, KindTimestamp, KindFloat32, KindFloat64, KindEnum, KindInterval:
		return true
	default:
		return false
	}
}
