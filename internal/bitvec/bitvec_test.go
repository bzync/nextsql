package bitvec

import "testing"

func TestBytes(t *testing.T) {
	for _, tc := range []struct{ n, want int }{{1, 1}, {8, 1}, {9, 2}, {16, 2}, {17, 3}, {0, 0}} {
		if got := Bytes(tc.n); got != tc.want {
			t.Fatalf("Bytes(%d) = %d, want %d", tc.n, got, tc.want)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := Validate([]float32{0, 1, 1, 0, 1}); err != nil {
		t.Fatalf("valid bit vector rejected: %v", err)
	}
	for _, bad := range [][]float32{{0.5}, {2}, {-1}, {1, 1, 0.0001}} {
		if err := Validate(bad); err == nil {
			t.Fatalf("Validate(%v) accepted a non-bit element", bad)
		}
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := [][]float32{
		{1},
		{0, 0, 0, 0, 0, 0, 0, 0},
		{1, 0, 1, 1, 0, 0, 1, 0, 1},
		make([]float32, 100),
	}
	cases[3][0], cases[3][7], cases[3][63], cases[3][64], cases[3][99] = 1, 1, 1, 1, 1
	for _, v := range cases {
		buf := make([]byte, Bytes(len(v)))
		Encode(buf, v)
		got := Decode(buf, len(v))
		if len(got) != len(v) {
			t.Fatalf("len %d want %d", len(got), len(v))
		}
		for i := range v {
			if got[i] != v[i] {
				t.Fatalf("element %d: got %v want %v", i, got[i], v[i])
			}
		}
	}
}

func TestEncodeClearsPadBits(t *testing.T) {
	buf := []byte{0xff}             // 1 byte = Bytes(3)
	Encode(buf, []float32{1, 0, 0}) // 3 elements, 5 pad bits in this byte
	if buf[0] != 0x01 {
		t.Fatalf("pad bits not cleared: %#v", buf)
	}
}

func TestHamming(t *testing.T) {
	a := []float32{1, 0, 1, 0, 1}
	b := []float32{1, 1, 1, 1, 0}
	if d := Hamming(a, b); d != 3 {
		t.Fatalf("Hamming = %v, want 3", d)
	}
	if d := Hamming(a, a); d != 0 {
		t.Fatalf("Hamming(a,a) = %v, want 0", d)
	}
}

func FuzzRoundTrip(f *testing.F) {
	f.Add([]byte{0b10110010}, 8)
	f.Add([]byte{0xff, 0x01}, 9)
	f.Fuzz(func(t *testing.T, packed []byte, n int) {
		if n < 1 || n > 4096 || len(packed) < Bytes(n) {
			return
		}
		v := Decode(packed, n)
		if err := Validate(v); err != nil {
			t.Fatalf("decoded vector fails Validate: %v", err)
		}
		buf := make([]byte, Bytes(n))
		Encode(buf, v)
		got := Decode(buf, n)
		for i := range v {
			if got[i] != v[i] {
				t.Fatalf("re-encode disagrees at %d: %v vs %v", i, got[i], v[i])
			}
		}
	})
}
