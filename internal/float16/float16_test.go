package float16

import (
	"math"
	"testing"
)

func TestExactValues(t *testing.T) {
	cases := []struct {
		f float32
		h uint16
	}{
		{0, 0x0000},
		{math.Float32frombits(0x80000000), 0x8000}, // -0
		{1, 0x3c00},
		{-1, 0xbc00},
		{2, 0x4000},
		{0.5, 0x3800},
		{-0.5, 0xb800},
		{65504, 0x7bff}, // largest finite binary16
		{-65504, 0xfbff},
		{math.Float32frombits(0x33800000), 0x0001}, // smallest positive subnormal 2^-24
		{6.1035156e-05, 0x0400},                    // smallest positive normal 2^-14
	}
	for _, c := range cases {
		if got := FromFloat32(c.f); got != c.h {
			t.Errorf("FromFloat32(%v) = %#04x, want %#04x", c.f, got, c.h)
		}
		if got := ToFloat32(c.h); got != c.f {
			t.Errorf("ToFloat32(%#04x) = %v, want %v", c.h, got, c.f)
		}
	}
}

func TestOverflowToInf(t *testing.T) {
	if h := FromFloat32(70000); h != 0x7c00 {
		t.Fatalf("overflow: got %#04x, want 0x7c00 (+Inf)", h)
	}
	if h := FromFloat32(-70000); h != 0xfc00 {
		t.Fatalf("overflow: got %#04x, want 0xfc00 (-Inf)", h)
	}
	if h := FromFloat32(float32(math.Inf(1))); h != 0x7c00 {
		t.Fatalf("+Inf: got %#04x", h)
	}
	if f := ToFloat32(0x7c00); !math.IsInf(float64(f), 1) {
		t.Fatalf("ToFloat32(0x7c00) = %v, want +Inf", f)
	}
}

func TestNaN(t *testing.T) {
	h := FromFloat32(float32(math.NaN()))
	if f := ToFloat32(h); !math.IsNaN(float64(f)) {
		t.Fatalf("NaN round trip lost: %v", f)
	}
}

func TestRoundToEven(t *testing.T) {
	// 1 + 2^-11 is exactly between 0x3c00 (1.0) and 0x3c01; ties to even -> 0x3c00.
	mid := float32(1) + float32(math.Ldexp(1, -11))
	if h := FromFloat32(mid); h != 0x3c00 {
		t.Fatalf("tie-to-even: got %#04x, want 0x3c00", h)
	}
	// 1 + 3*2^-12 rounds up to 0x3c01.
	up := float32(1) + float32(3*math.Ldexp(1, -12))
	if h := FromFloat32(up); h != 0x3c01 {
		t.Fatalf("round up: got %#04x, want 0x3c01", h)
	}
}

func TestQuantizeIdempotent(t *testing.T) {
	in := []float32{0.1, 0.25, -3.5, 1000, 1e-5, 0}
	q1 := Quantize(in)
	q2 := Quantize(q1)
	for i := range q1 {
		if q1[i] != q2[i] {
			t.Fatalf("not idempotent at %d: %v vs %v", i, q1[i], q2[i])
		}
		if math.Abs(float64(q1[i]-in[i])) > 0.5 {
			t.Fatalf("quantization error too large at %d: %v -> %v", i, in[i], q1[i])
		}
	}
}

func TestPutRead(t *testing.T) {
	v := []float32{1, 2, 3, -4.5, 0.5}
	buf := make([]byte, Bytes(len(v)))
	Put(buf, v)
	got := Read(buf)
	if len(got) != len(v) {
		t.Fatalf("len %d want %d", len(got), len(v))
	}
	for i := range v {
		if got[i] != v[i] { // all exactly representable
			t.Fatalf("element %d: got %v want %v", i, got[i], v[i])
		}
	}
}

func FuzzRoundTrip(f *testing.F) {
	f.Add(uint32(0x3c000000))
	f.Add(uint32(0x7f800000))
	f.Fuzz(func(t *testing.T, bits uint32) {
		in := math.Float32frombits(bits)
		h := FromFloat32(in)
		out := ToFloat32(h)
		if math.IsNaN(float64(in)) {
			if !math.IsNaN(float64(out)) {
				t.Fatalf("NaN not preserved: %v", out)
			}
			return
		}
		// widening a binary16 is exact, then requantising must be stable.
		if h2 := FromFloat32(out); h2 != h {
			t.Fatalf("requantize unstable: %#04x -> %v -> %#04x", h, out, h2)
		}
	})
}
