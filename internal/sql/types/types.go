package types

import "github.com/bzync/nextsql/internal/nerr"

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
	default:
		return "INVALID"
	}
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
type Type struct {
	Kind      Kind
	Precision uint16 // DECIMAL p or VECTOR dimension
	Scale     uint16 // DECIMAL s
	VecElem   uint8  // VecF32
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
	return t.Kind == o.Kind && t.Precision == o.Precision && t.Scale == o.Scale && t.VecElem == o.VecElem
}

func UUID() Type        { return Type{Kind: KindUUID} }
func String() Type      { return Type{Kind: KindString} }
func Text() Type        { return Type{Kind: KindText} }
func TimestampTZ() Type { return Type{Kind: KindTimestampTZ} }
func JSON() Type        { return Type{Kind: KindJSON} }
func Bool() Type        { return Type{Kind: KindBool} }
func NullType() Type    { return Type{Kind: KindNull} }

func DecimalType(p, s uint16) (Type, error) {
	if p < 1 || p > 38 || s > p {
		return Type{}, nerr.New(nerr.InvalidArgument, "types.DecimalType", "DECIMAL precision/scale out of range")
	}
	return Type{Kind: KindDecimal, Precision: p, Scale: s}, nil
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
	case KindUUID, KindString, KindText, KindDecimal, KindTimestampTZ, KindBool, KindPoint, KindBox, KindLine, KindPolygon:
		return true
	default:
		return false
	}
}
