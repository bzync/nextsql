package int8vec

import (
	"math"
	"testing"
)

func TestZeroVectorRoundTripsExactly(t *testing.T) {
	v := []float32{0, 0, 0, 0}
	if s := Scale(v); s != 1 {
		t.Fatalf("zero-vector scale = %v, want 1", s)
	}
	q := Quantize(v)
	for i, x := range q {
		if x != 0 {
			t.Fatalf("element %d = %v, want 0", i, x)
		}
	}
}

func TestQuantizeErrorWithinHalfStep(t *testing.T) {
	v := []float32{1, -1, 0.5, -0.25, 0.03, 3.7, -2.9}
	scale := Scale(v)
	q := Quantize(v)
	for i := range v {
		if math.Abs(float64(q[i]-v[i])) > float64(scale)/2+1e-6 {
			t.Fatalf("element %d: %v -> %v, error exceeds half a step (%v)", i, v[i], q[i], scale/2)
		}
	}
}

func TestQuantizeIdempotent(t *testing.T) {
	v := []float32{0.1, 0.25, -3.5, 1000, 1e-3, 0, -0.9}
	q1 := Quantize(v)
	q2 := Quantize(q1)
	for i := range q1 {
		if q1[i] != q2[i] {
			t.Fatalf("not idempotent at %d: %v vs %v", i, q1[i], q2[i])
		}
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	v := []float32{1, 2, 3, -4.5, 0.5, -128, 127}
	buf := make([]byte, Bytes(len(v)))
	Encode(buf, v)
	got := Decode(buf)
	if len(got) != len(v) {
		t.Fatalf("len %d want %d", len(got), len(v))
	}
	want := Quantize(v)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("element %d: decode %v want %v", i, got[i], want[i])
		}
	}
}

func TestExtremeCodeNeverMinInt8(t *testing.T) {
	// The most-negative element must map to -127, never -128, so the encoding
	// stays sign-symmetric.
	v := []float32{-10, 10, -9.999}
	buf := make([]byte, Bytes(len(v)))
	Encode(buf, v)
	if b := int8(buf[4]); b != -127 {
		t.Fatalf("most-negative element encoded as %d, want -127", b)
	}
}

func FuzzRoundTrip(f *testing.F) {
	f.Add(float32(1), float32(-2), float32(0.5), float32(0))
	f.Add(float32(1e-6), float32(65000), float32(-0.001), float32(3.14159))
	f.Fuzz(func(t *testing.T, a, b, c, d float32) {
		for _, x := range []float32{a, b, c, d} {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				return
			}
		}
		v := []float32{a, b, c, d}
		buf := make([]byte, Bytes(len(v)))
		Encode(buf, v)
		got := Decode(buf)
		q := Quantize(v)
		for i := range v {
			if got[i] != q[i] {
				t.Fatalf("encode/decode disagrees with Quantize at %d: %v vs %v", i, got[i], q[i])
			}
		}
		// requantising a dequantised vector is stable
		buf2 := make([]byte, Bytes(len(q)))
		Encode(buf2, q)
		got2 := Decode(buf2)
		for i := range q {
			if got2[i] != q[i] {
				t.Fatalf("requantize unstable at %d: %v -> %v", i, q[i], got2[i])
			}
		}
	})
}
