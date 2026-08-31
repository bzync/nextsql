// Package float16 provides portable IEEE 754 half-precision (binary16)
// conversion for the quantised vector storage types (VECTOR<F16,N>).
//
// No unsafe, no cgo, no assembly: the production vector path stays portable Go
// (see internal/vector.TestPortableProductionPath). Conversion is
// round-to-nearest, ties-to-even, matching hardware f32->f16 rounding.
package float16

import (
	"encoding/binary"
	"math"
)

// FromFloat32 rounds f to the nearest binary16 value (ties to even) and returns
// its 16-bit encoding. Finite values whose magnitude exceeds the binary16 range
// round to +/-Inf; NaN stays NaN; sign is always preserved.
func FromFloat32(f float32) uint16 {
	b := math.Float32bits(f)
	sign := uint16((b >> 16) & 0x8000)
	rawExp := (b >> 23) & 0xff
	mant := b & 0x7fffff

	if rawExp == 0xff { // Inf / NaN
		if mant != 0 {
			return sign | 0x7e00 // quiet NaN
		}
		return sign | 0x7c00 // Inf
	}

	exp := int32(rawExp) - 127 + 15
	if exp >= 0x1f { // overflow -> Inf
		return sign | 0x7c00
	}
	if exp <= 0 { // subnormal or underflow
		if exp < -10 {
			return sign // rounds to signed zero
		}
		mant |= 0x800000 // restore implicit leading bit
		shift := uint32(14 - exp)
		half := uint32(1) << (shift - 1)
		round := mant & ((1 << shift) - 1)
		res := mant >> shift
		if round > half || (round == half && res&1 == 1) {
			res++
		}
		return sign | uint16(res)
	}

	// normal: narrow the 23-bit mantissa to 10 bits with round-to-even.
	h := sign | uint16(exp<<10) | uint16(mant>>13)
	half := uint32(1) << 12
	round := mant & 0x1fff
	if round > half || (round == half && h&1 == 1) {
		h++ // carry propagates into the exponent, and to Inf on overflow
	}
	return h
}

// ToFloat32 widens a binary16 value to the float32 that represents it exactly.
func ToFloat32(h uint16) float32 {
	sign := uint32(h&0x8000) << 16
	exp := uint32(h>>10) & 0x1f
	mant := uint32(h & 0x3ff)

	switch exp {
	case 0:
		if mant == 0 {
			return math.Float32frombits(sign)
		}
		// subnormal: normalise into a float32 normal.
		var e uint32
		for mant&0x400 == 0 {
			mant <<= 1
			e++
		}
		mant &= 0x3ff
		return math.Float32frombits(sign | (113-e)<<23 | mant<<13)
	case 0x1f:
		if mant == 0 {
			return math.Float32frombits(sign | 0x7f800000)
		}
		return math.Float32frombits(sign | 0x7f800000 | mant<<13)
	default:
		return math.Float32frombits(sign | (exp+112)<<23 | mant<<13)
	}
}

// Bytes is the on-disk size of n binary16 elements.
func Bytes(n int) int { return n * 2 }

// Put writes v as little-endian binary16 elements into dst, which must be at
// least Bytes(len(v)) long. Each element is quantised via FromFloat32.
func Put(dst []byte, v []float32) {
	for i, f := range v {
		binary.LittleEndian.PutUint16(dst[i*2:], FromFloat32(f))
	}
}

// Read decodes len(src)/2 little-endian binary16 elements to float32.
func Read(src []byte) []float32 {
	out := make([]float32, len(src)/2)
	for i := range out {
		out[i] = ToFloat32(binary.LittleEndian.Uint16(src[i*2:]))
	}
	return out
}

// Quantize returns v round-tripped through binary16: the values that would be
// read back after storing v in a VECTOR<F16,N> column.
func Quantize(v []float32) []float32 {
	out := make([]float32, len(v))
	for i, f := range v {
		out[i] = ToFloat32(FromFloat32(f))
	}
	return out
}
