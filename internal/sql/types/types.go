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

// VecF32 is the only vector element type in Phase 5.
const VecF32 uint8 = 1

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
		return "VECTOR<F32," + itoa(int(t.Precision)) + ">"
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

// MaxGeoVertices is the abuse limit for LINESTRING / POLYGON coordinates.
// 256 * 16 bytes fits in a 16 KiB page with room for other columns.
const MaxGeoVertices = 256

func VectorF32(n uint16) (Type, error) {
	if n < 1 || n > MaxVectorDim {
		return Type{}, nerr.New(nerr.InvalidArgument, "types.VectorF32", "VECTOR dimension out of range")
	}
	return Type{Kind: KindVector, Precision: n, VecElem: VecF32}, nil
}

func (t Type) Comparable() bool {
	switch t.Kind {
	case KindUUID, KindString, KindText, KindDecimal, KindTimestampTZ, KindBool, KindPoint, KindBox, KindLine, KindPolygon:
		return true
	default:
		return false
	}
}
