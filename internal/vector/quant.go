package vector

import (
	"github.com/bzync/nextsql/internal/float16"
	"github.com/bzync/nextsql/internal/int8vec"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

// ValidQuant reports whether q names an HNSW traversal-quantisation encoding:
// 0 (none / full precision), VecF16, or VecI8.
func ValidQuant(q uint8) bool {
	return q == 0 || q == types.VecF16 || q == types.VecI8
}

// QuantizeElem returns v round-tripped through the element encoding elem
// (types.VecF16 or types.VecI8). elem 0 or types.VecF32 returns a copy of v.
// This is the value a quantised HNSW index sees for traversal-time distance.
func QuantizeElem(v []float32, elem uint8) []float32 {
	switch elem {
	case types.VecF16:
		return float16.Quantize(v)
	case types.VecI8:
		return int8vec.Quantize(v)
	default:
		return append([]float32(nil), v...)
	}
}

// QVecKey stores one HNSW traversal vector (the column value quantised to the
// index's encoding) in the graph tree, keyed by primary key. It lives beside
// the graph nodes so a quantised search never touches the column payload store.
func QVecKey(pk []byte) []byte {
	out := make([]byte, 1+len(pk))
	out[0] = kindQVec
	copy(out[1:], pk)
	return out
}

// SplitQVecKey returns the primary-key suffix of a QVecKey.
func SplitQVecKey(k []byte) ([]byte, error) {
	if len(k) < 2 || k[0] != kindQVec {
		return nil, nerr.New(nerr.InvalidFormat, "vector.SplitQVecKey", "not an HNSW quantised-vector key")
	}
	return append([]byte(nil), k[1:]...), nil
}

// QVecBounds is the exclusive key range of every HNSW traversal vector.
func QVecBounds() (start, end []byte) {
	return []byte{kindQVec}, []byte{kindQVec + 1}
}
