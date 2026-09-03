package types

import (
	"bytes"
	"math"
	"sort"
	"testing"
)

// TestFloatCanonicalization covers D8 (Datatype expansion track): -0.0 is
// canonicalized to +0.0 and every NaN payload collapses to one value, so
// equal values always have identical bits.
func TestFloatCanonicalization(t *testing.T) {
	nz := Float64Value(math.Copysign(0, -1))
	if math.Signbit(nz.Flt) {
		t.Fatalf("-0.0 was not canonicalized: %v", nz.Flt)
	}
	if c, _ := nz.Cmp(Float64Value(0)); c != 0 {
		t.Fatalf("-0.0 != +0.0: %d", c)
	}
	a := Float64Value(math.NaN())
	b := Float64Value(math.Float64frombits(0x7FF8000000000001)) // a different NaN payload
	if math.Float64bits(a.Flt) != math.Float64bits(b.Flt) {
		t.Fatalf("NaN payloads not collapsed: %x vs %x", math.Float64bits(a.Flt), math.Float64bits(b.Flt))
	}
	if c, _ := a.Cmp(b); c != 0 {
		t.Fatalf("NaN != NaN in total order: %d", c)
	}
}

// TestFloatTotalOrder is the critical index-key correctness test: the sortable
// key order must be -Inf < negatives < 0 < positives < +Inf < NaN.
func TestFloatTotalOrder(t *testing.T) {
	vals := []float64{
		math.Inf(-1), -1e308, -1.5, -math.SmallestNonzeroFloat64, 0, math.SmallestNonzeroFloat64,
		1.5, 1e308, math.Inf(1), math.NaN(),
	}
	// Shuffle-independent: sort the encoded keys and confirm they come back
	// in the intended order.
	type kv struct {
		f float64
		k []byte
	}
	var enc []kv
	for _, f := range vals {
		k, err := EncodeKey([]Value{Float64Value(f)})
		if err != nil {
			t.Fatal(err)
		}
		enc = append(enc, kv{f, k})
	}
	sort.Slice(enc, func(i, j int) bool { return bytes.Compare(enc[i].k, enc[j].k) < 0 })
	for i, e := range enc {
		want := vals[i]
		got := e.f
		if math.IsNaN(want) {
			if !math.IsNaN(got) {
				t.Fatalf("position %d: want NaN, got %v", i, got)
			}
			continue
		}
		if got != want {
			t.Fatalf("float key order wrong at %d: got %v want %v", i, got, want)
		}
	}

	// Same for FLOAT32.
	f32vals := []float64{math.Inf(-1), -2, -0.5, 0, 0.5, 2, math.Inf(1)}
	var e32 [][]byte
	for _, f := range f32vals {
		k, _ := EncodeKey([]Value{Float32Value(f)})
		e32 = append(e32, k)
	}
	for i := 1; i < len(e32); i++ {
		if bytes.Compare(e32[i-1], e32[i]) >= 0 {
			t.Fatalf("float32 key order wrong at %d", i)
		}
	}
}

// TestFloatRowAndKeyRoundTrip covers heap-row and sortable-key encode/decode
// for both widths, including non-finite values.
func TestFloatRowAndKeyRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		typ Type
		f   float64
	}{
		{Float64(), 3.141592653589793},
		{Float64(), -2.5e-10},
		{Float64(), math.Inf(1)},
		{Float32(), 1.5},
		{Float32(), -0.25},
	} {
		v := FloatValue(tc.typ.Kind, tc.f)
		enc, err := EncodeRow([]Value{v})
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeRow(enc, []Type{tc.typ})
		if err != nil {
			t.Fatal(err)
		}
		if got[0].Flt != v.Flt {
			t.Fatalf("row round trip %v: got %v want %v", tc.typ, got[0].Flt, v.Flt)
		}
		k, err := EncodeKey([]Value{v})
		if err != nil {
			t.Fatal(err)
		}
		kgot, err := DecodeKey(k, []Type{tc.typ})
		if err != nil {
			t.Fatal(err)
		}
		if kgot[0].Flt != v.Flt && !(math.IsNaN(kgot[0].Flt) && math.IsNaN(v.Flt)) {
			t.Fatalf("key round trip %v: got %v want %v", tc.typ, kgot[0].Flt, v.Flt)
		}
	}
}

// TestFloatCoerce covers the coercible numeric group (float <-> int/uint/
// decimal/text) and its error paths.
func TestFloatCoerce(t *testing.T) {
	// int -> float
	if v, err := Coerce(Int32Value(7), Float64()); err != nil || v.Flt != 7 {
		t.Fatalf("int->float: %+v %v", v, err)
	}
	// float -> int, exact
	if v, err := Coerce(Float64Value(7), Int32()); err != nil || v.Int != 7 {
		t.Fatalf("float->int: %+v %v", v, err)
	}
	// float -> int, fractional: error
	if _, err := Coerce(Float64Value(7.5), Int32()); err == nil {
		t.Fatal("expected fractional float->int to error")
	}
	// float -> int, out of range: error
	if _, err := Coerce(Float64Value(1e30), Int32()); err == nil {
		t.Fatal("expected out-of-range float->int to error")
	}
	// NaN -> int / decimal: error
	if _, err := Coerce(Float64Value(math.NaN()), Int64()); err == nil {
		t.Fatal("expected NaN->int to error")
	}
	if _, err := Coerce(Float64Value(math.Inf(1)), Type{Kind: KindDecimal, Precision: 10, Scale: 2}); err == nil {
		t.Fatal("expected Inf->decimal to error")
	}
	// decimal -> float
	d, _ := ParseDecimal("3.25")
	if v, err := Coerce(DecimalValue(d, Type{Kind: KindDecimal, Scale: 2}), Float64()); err != nil || v.Flt != 3.25 {
		t.Fatalf("decimal->float: %+v %v", v, err)
	}
	// float -> decimal
	if v, err := Coerce(Float64Value(1.5), Type{Kind: KindDecimal, Precision: 10, Scale: 4}); err != nil || v.Dec.String() != "1.5000" {
		t.Fatalf("float->decimal: %+v %v", v, err)
	}
	// text -> float (scientific ok)
	if v, err := Coerce(StringValue("1.5e3"), Float64()); err != nil || v.Flt != 1500 {
		t.Fatalf("text->float: %+v %v", v, err)
	}
	// f64 -> f32 rounds
	if v, err := Coerce(Float64Value(0.1), Float32()); err != nil || v.Flt != float64(float32(0.1)) {
		t.Fatalf("f64->f32: %+v %v", v, err)
	}
	// float is isolated from BLOB/BOOL/date
	if _, err := Coerce(BoolValue(true), Float64()); err == nil {
		t.Fatal("expected BOOL->float to error")
	}
}
