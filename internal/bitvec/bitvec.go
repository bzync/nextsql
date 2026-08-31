// Package bitvec provides portable bit-packing for the binary vector storage
// type BITVECTOR<N>.
//
// A BITVECTOR<N> column stores N single-bit elements. On disk the vector is
// ceil(N/8) bytes, one bit per element, LSB-first within each byte (element i
// lives in bit i&7 of byte i>>3; trailing pad bits in the final byte are zero).
// On read every element is widened back to a float32 that is exactly 0 or 1, so
// all distance, NEAREST, and HNSW math stays float32 exactly as for the other
// vector element types. The natural distance for a BITVECTOR is HAMMING (the
// number of differing bits); on 0/1 float vectors that equals the L1 distance.
//
// No unsafe, no cgo, no assembly: the production vector path stays portable Go
// (see internal/vector.TestPortableProductionPath).
package bitvec

import "github.com/bzync/nextsql/internal/nerr"

// Bytes is the on-disk size of an n-element bit vector.
func Bytes(n int) int { return (n + 7) / 8 }

// Validate rejects a vector whose elements are not each exactly 0 or 1. A
// BITVECTOR column never silently rounds a real-valued vector to bits.
func Validate(v []float32) error {
	for _, f := range v {
		if f != 0 && f != 1 {
			return nerr.New(nerr.InvalidArgument, "bitvec.Validate", "BITVECTOR element must be 0 or 1")
		}
	}
	return nil
}

// Encode packs v into dst, which must be at least Bytes(len(v)) long. Elements
// must already be 0 or 1 (call Validate first); any non-zero element sets its
// bit. Pad bits in the final byte are cleared.
func Encode(dst []byte, v []float32) {
	n := Bytes(len(v))
	for i := 0; i < n; i++ {
		dst[i] = 0
	}
	for i, f := range v {
		if f != 0 {
			dst[i>>3] |= 1 << uint(i&7)
		}
	}
}

// Decode unpacks the first n elements of a bit-packed vector to float32 values
// that are each exactly 0 or 1. src must be at least Bytes(n) long.
func Decode(src []byte, n int) []float32 {
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		if src[i>>3]&(1<<uint(i&7)) != 0 {
			out[i] = 1
		}
	}
	return out
}

// Hamming is the number of positions at which a and b differ. On 0/1 vectors
// this is the standard bit Hamming distance. a and b must have equal length.
func Hamming(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var d int
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			d++
		}
	}
	return float64(d)
}
