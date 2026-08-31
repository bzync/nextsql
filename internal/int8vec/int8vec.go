// Package int8vec provides portable per-vector symmetric int8 quantisation for
// the compressed vector storage type VECTOR<I8,N>.
//
// Each vector is quantised independently: the scale is derived from the vector's
// own largest-magnitude element, so no catalog-side calibration or data scan is
// needed. On disk a quantised vector is a little-endian float32 scale followed
// by one signed byte per element; on read every element is widened back to
// float32 (q*scale) and all distance, algebra, NEAREST, and HNSW math stays
// float32, exactly as for VECTOR<F16,N>.
//
// No unsafe, no cgo, no assembly: the production vector path stays portable Go
// (see internal/vector.TestPortableProductionPath).
package int8vec

import (
	"encoding/binary"
	"math"
)

// qMax is the symmetric quantisation range: elements map to [-127, 127] so the
// encoding is sign-symmetric (the -128 code is never produced).
const qMax = 127

// Bytes is the on-disk size of a quantised n-element vector: a float32 scale
// followed by n signed bytes.
func Bytes(n int) int { return 4 + n }

// Scale returns the per-vector quantisation scale: absmax(v) / 127, or 1 when v
// is all zeros (so a zero vector round-trips exactly).
func Scale(v []float32) float32 {
	var absmax float32
	for _, f := range v {
		a := f
		if a < 0 {
			a = -a
		}
		if a > absmax {
			absmax = a
		}
	}
	if absmax == 0 {
		return 1
	}
	return absmax / qMax
}

// quantElem rounds x/scale to the nearest integer (ties away from zero, matching
// math.Round) and clamps into [-127, 127].
func quantElem(x, scale float32) int8 {
	q := math.Round(float64(x / scale))
	if q > qMax {
		q = qMax
	} else if q < -qMax {
		q = -qMax
	}
	return int8(q)
}

// Encode writes Scale(v) as a little-endian float32 followed by len(v) signed
// bytes into dst, which must be at least Bytes(len(v)) long.
func Encode(dst []byte, v []float32) {
	scale := Scale(v)
	binary.LittleEndian.PutUint32(dst, math.Float32bits(scale))
	for i, f := range v {
		dst[4+i] = byte(quantElem(f, scale))
	}
}

// Decode widens a Bytes(n)-long quantised vector (4-byte scale + n signed bytes)
// back to n float32 elements.
func Decode(src []byte) []float32 {
	scale := math.Float32frombits(binary.LittleEndian.Uint32(src))
	out := make([]float32, len(src)-4)
	for i := range out {
		out[i] = float32(int8(src[4+i])) * scale
	}
	return out
}

// Quantize returns v round-tripped through int8 quantisation: the values that
// would be read back after storing v in a VECTOR<I8,N> column.
func Quantize(v []float32) []float32 {
	scale := Scale(v)
	out := make([]float32, len(v))
	for i, f := range v {
		out[i] = float32(quantElem(f, scale)) * scale
	}
	return out
}
