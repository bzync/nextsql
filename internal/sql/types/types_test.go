package types

import (
	"bytes"
	"math"
	"testing"
)

func TestDecimalFromInt64(t *testing.T) {
	for _, n := range []int64{0, 1, -1, 255, 256, 9999999, -42} {
		got := DecimalFromInt64(n)
		want, err := ParseDecimal(itoa64(n))
		if err != nil {
			t.Fatal(err)
		}
		if got.Cmp(want) != 0 {
			t.Fatalf("%d: got %s want %s", n, got.String(), want.String())
		}
	}
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var tmp [24]byte
	i := len(tmp)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		tmp[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		tmp[i] = '-'
	}
	return string(tmp[i:])
}

func TestDecimalParseCmpRescale(t *testing.T) {
	a, err := ParseDecimal("1000.5")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseDecimal("1000.50")
	if err != nil {
		t.Fatal(err)
	}
	if a.Cmp(b) != 0 {
		t.Fatalf("1000.5 vs 1000.50: %d", a.Cmp(b))
	}
	c, err := a.Rescale(12, 2)
	if err != nil {
		t.Fatal(err)
	}
	if c.String() != "1000.50" {
		t.Fatalf("got %s", c.String())
	}
	lossy, err := ParseDecimal("1.234")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lossy.Rescale(4, 2); err == nil {
		t.Fatal("expected scale loss")
	}
}

func TestReplaceRowColumn(t *testing.T) {
	dec, err := DecimalType(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	d1, _ := ParseDecimal("42")
	d0, _ := ParseDecimal("0")
	cols := []Type{String(), String(), dec}
	raw, err := EncodeRow([]Value{StringValue("s1"), StringValue("a"), DecimalValue(d1, dec)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReplaceRowColumn(raw, cols, 2, DecimalValue(d0, dec))
	if err != nil {
		t.Fatal(err)
	}
	row, err := DecodeRow(got, cols)
	if err != nil {
		t.Fatal(err)
	}
	if row[0].Str != "s1" || row[1].Str != "a" || row[2].Dec.String() != "0" {
		t.Fatalf("%+v", row)
	}
	into, err := ReplaceRowColumnInto(nil, raw, cols, 2, DecimalValue(d0, dec))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, into) {
		t.Fatal("ReplaceRowColumnInto mismatch")
	}
	b, null, err := PeekRowColumn(raw, cols, 1)
	if err != nil || null || string(b) != "a" {
		t.Fatalf("peek k %q null=%v err=%v", b, null, err)
	}
}

func TestRowRoundTrip(t *testing.T) {
	dec, err := DecimalType(12, 2)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := ParseDecimal("12.50")
	d, _ = d.Rescale(12, 2)
	u, err := NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	vals := []Value{
		u,
		StringValue("hello"),
		Null(Text()),
		DecimalValue(d, dec),
		Now(),
		mustJSON(t, `{"a":1}`),
	}
	raw, err := EncodeRow(vals)
	if err != nil {
		t.Fatal(err)
	}
	cols := []Type{UUID(), String(), Text(), dec, TimestampTZ(), JSON()}
	got, err := DecodeRow(raw, cols)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(vals) {
		t.Fatalf("len %d", len(got))
	}
	if got[0].UUID != vals[0].UUID || got[1].Str != "hello" || !got[2].Null {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if got[3].Dec.Cmp(d) != 0 {
		t.Fatalf("decimal %s", got[3].Dec.String())
	}
	if !bytes.Equal(got[5].JSON, vals[5].JSON) {
		t.Fatalf("json %q", got[5].JSON)
	}
	if got[5].String() != `{"a":1}` {
		t.Fatalf("json text %s", got[5].String())
	}
	col1, err := DecodeRowColumn(raw, cols, 1)
	if err != nil || col1.Str != "hello" {
		t.Fatalf("column 1 %q %v", col1.Str, err)
	}
	col2, err := DecodeRowColumn(raw, cols, 2)
	if err != nil || !col2.Null {
		t.Fatalf("column 2 null %v %v", col2.Null, err)
	}
}

func TestEncodeKeyOrder(t *testing.T) {
	a, _ := EncodeKey([]Value{StringValue("a")})
	b, _ := EncodeKey([]Value{StringValue("b")})
	if bytes.Compare(a, b) >= 0 {
		t.Fatalf("string key order")
	}
	d1, _ := ParseDecimal("10")
	d2, _ := ParseDecimal("20")
	k1, _ := EncodeKey([]Value{DecimalValue(d1, Type{Kind: KindDecimal, Precision: 12, Scale: 0})})
	k2, _ := EncodeKey([]Value{DecimalValue(d2, Type{Kind: KindDecimal, Precision: 12, Scale: 0})})
	if bytes.Compare(k1, k2) >= 0 {
		t.Fatalf("decimal key order")
	}
}

func TestDecodeKeyRoundTrip(t *testing.T) {
	dec, err := DecimalType(12, 2)
	if err != nil {
		t.Fatal(err)
	}
	dneg, _ := ParseDecimal("-256.50")
	dneg, _ = dneg.Rescale(12, 2)
	dpos, _ := ParseDecimal("10.25")
	dpos, _ = dpos.Rescale(12, 2)
	u, err := NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	vals := []Value{u, StringValue("a\x00b"), DecimalValue(dneg, dec), DecimalValue(dpos, dec), Null(String()), BoolValue(true)}
	cols := []Type{UUID(), String(), dec, dec, String(), Bool()}
	raw, err := EncodeKey(vals)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeKey(raw, cols)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(vals) {
		t.Fatalf("len %d", len(got))
	}
	if got[0].UUID != u.UUID || got[1].Str != "a\x00b" || !got[4].Null || !got[5].Bool {
		t.Fatalf("%+v", got)
	}
	if got[2].Dec.Cmp(dneg) != 0 || got[3].Dec.Cmp(dpos) != 0 {
		t.Fatalf("dec %s %s", got[2].Dec.String(), got[3].Dec.String())
	}
	n1, _ := EncodeKey([]Value{DecimalValue(mustDecVal(t, "-256"), Type{Kind: KindDecimal})})
	n2, _ := EncodeKey([]Value{DecimalValue(mustDecVal(t, "-1"), Type{Kind: KindDecimal})})
	if bytes.Compare(n1, n2) >= 0 {
		t.Fatalf("negative decimal order")
	}
	p := []byte{1, 2, 3}
	end := PrefixEnd(p)
	if bytes.Compare(p, end) >= 0 {
		t.Fatalf("prefix end %v %v", p, end)
	}
}

func TestBlobRowRoundTrip(t *testing.T) {
	raw := []byte{0x00, 0xFF, 'h', 'i', 0x00, 0xDE, 0xAD, 0xBE, 0xEF}
	vals := []Value{BlobValue(raw), Null(Blob()), BlobValue(nil)}
	cols := []Type{Blob(), Blob(), Blob()}
	enc, err := EncodeRow(vals)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRow(enc, cols)
	if err != nil {
		t.Fatal(err)
	}
	if []byte(got[0].Str)[0] != 0 || got[0].Str != string(raw) {
		t.Fatalf("blob round trip mismatch: %x want %x", []byte(got[0].Str), raw)
	}
	if !got[1].Null {
		t.Fatalf("expected NULL blob")
	}
	if got[2].Str != "" {
		t.Fatalf("expected empty blob, got %x", []byte(got[2].Str))
	}
}

func TestBlobKeyOrderAndRoundTrip(t *testing.T) {
	a, err := EncodeKey([]Value{BlobValue([]byte{0x00, 0x01})})
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncodeKey([]Value{BlobValue([]byte{0x00, 0x02})})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Compare(a, b) >= 0 {
		t.Fatalf("blob key order: %x >= %x", a, b)
	}
	raw := []byte{0xFF, 0x00, 0x00, 0x7A}
	enc, err := EncodeKey([]Value{BlobValue(raw)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeKey(enc, []Type{Blob()})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Str != string(raw) {
		t.Fatalf("blob key round trip: %x want %x", []byte(got[0].Str), raw)
	}
}

func TestBlobCmp(t *testing.T) {
	lo := BlobValue([]byte{0x01})
	hi := BlobValue([]byte{0x02})
	c, err := lo.Cmp(hi)
	if err != nil || c >= 0 {
		t.Fatalf("cmp = %d, %v", c, err)
	}
	if _, err := lo.Cmp(StringValue("x")); err == nil {
		t.Fatal("expected type mismatch between BLOB and STRING")
	}
}

func TestBlobHexParseAndFormat(t *testing.T) {
	v, err := ParseHexBlob("deadBEEF")
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != "deadbeef" {
		t.Fatalf("format = %q", v.String())
	}
	if _, err := ParseHexBlob("zz"); err == nil {
		t.Fatal("expected error for invalid hex")
	}
	if _, err := ParseHexBlob("abc"); err == nil {
		t.Fatal("expected error for odd-length hex")
	}
}

func TestBlobCoerce(t *testing.T) {
	v, err := Coerce(StringValue("deadbeef"), Blob())
	if err != nil {
		t.Fatal(err)
	}
	if v.Str != string([]byte{0xDE, 0xAD, 0xBE, 0xEF}) {
		t.Fatalf("coerced bytes = %x", []byte(v.Str))
	}
	if _, err := Coerce(StringValue("not hex!"), Blob()); err == nil {
		t.Fatal("expected error coercing non-hex text to BLOB")
	}
	back, err := Coerce(BlobValue([]byte{0xAB, 0xCD}), String())
	if err != nil {
		t.Fatal(err)
	}
	if back.Str != "abcd" {
		t.Fatalf("blob-to-string coercion = %q", back.Str)
	}
	if _, err := Coerce(BlobValue([]byte{1}), UUID()); err == nil {
		t.Fatal("expected BLOB to stay isolated from UUID coercion")
	}
}

func mustDecVal(t *testing.T, s string) Decimal {
	t.Helper()
	d, err := ParseDecimal(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mustJSON(t *testing.T, src string) Value {
	t.Helper()
	v, err := JSONFromText(src)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestExtractJSONPath(t *testing.T) {
	doc := mustJSON(t, `{"category":"electronics","n":2}`)
	got, err := ExtractJSON(doc.JSON, []string{"category"})
	if err != nil || got.Str != "electronics" {
		t.Fatalf("%+v %v", got, err)
	}
	n, err := ExtractJSON(doc.JSON, []string{"n"})
	if err != nil || n.Dec.String() != "2" {
		t.Fatalf("%+v %v", n, err)
	}
	miss, err := ExtractJSON(doc.JSON, []string{"nope"})
	if err != nil || !miss.Null {
		t.Fatalf("missing %+v %v", miss, err)
	}
}

func TestCoerceJSONRejectsTextBlob(t *testing.T) {
	v, err := Coerce(StringValue(`{"a":1}`), JSON())
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != `{"a":1}` {
		t.Fatalf("%s", v.String())
	}
	if bytes.Equal(v.JSON, []byte(`{"a":1}`)) {
		t.Fatal("stored UTF-8 JSON text")
	}
	if _, err := Coerce(StringValue(`{`), JSON()); err == nil {
		t.Fatal("expected invalid JSON")
	}
}

// TestIntRowRoundTrip covers D2 (Datatype expansion track): fixed-width
// payload encode/decode for every width, including boundary values.
func TestIntRowRoundTrip(t *testing.T) {
	vals := []Value{
		Int8Value(math.MinInt8), Int8Value(math.MaxInt8),
		Int16Value(math.MinInt16), Int16Value(math.MaxInt16),
		Int32Value(math.MinInt32), Int32Value(math.MaxInt32),
		Int64Value(math.MinInt64), Int64Value(math.MaxInt64),
		Null(Int32()),
	}
	cols := []Type{Int8(), Int8(), Int16(), Int16(), Int32(), Int32(), Int64(), Int64(), Int32()}
	enc, err := EncodeRow(vals)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRow(enc, cols)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range vals {
		if v.Null {
			if !got[i].Null {
				t.Fatalf("index %d: expected NULL", i)
			}
			continue
		}
		if got[i].Int != v.Int {
			t.Fatalf("index %d: roundtrip mismatch got %d want %d", i, got[i].Int, v.Int)
		}
	}
}

// TestIntKeyOrderAndRoundTrip is the critical sign-bit-ordering test at the
// types-package level (see docs/design-datatypes.md D2): naive two's
// complement byte order would sort every negative value after every
// positive one, which EncodeKey must not do.
func TestIntKeyOrderAndRoundTrip(t *testing.T) {
	widths := []struct {
		name string
		typ  Type
		vals []Value
	}{
		{"int8", Int8(), []Value{Int8Value(-128), Int8Value(-1), Int8Value(0), Int8Value(1), Int8Value(127)}},
		{"int16", Int16(), []Value{Int16Value(math.MinInt16), Int16Value(-1), Int16Value(0), Int16Value(1), Int16Value(math.MaxInt16)}},
		{"int32", Int32(), []Value{Int32Value(math.MinInt32), Int32Value(-1), Int32Value(0), Int32Value(1), Int32Value(math.MaxInt32)}},
		{"int64", Int64(), []Value{Int64Value(math.MinInt64), Int64Value(-1), Int64Value(0), Int64Value(1), Int64Value(math.MaxInt64)}},
	}
	for _, w := range widths {
		var keys [][]byte
		for _, v := range w.vals {
			k, err := EncodeKey([]Value{v})
			if err != nil {
				t.Fatalf("%s: %v", w.name, err)
			}
			keys = append(keys, k)
		}
		for i := 1; i < len(keys); i++ {
			if bytes.Compare(keys[i-1], keys[i]) >= 0 {
				t.Fatalf("%s: sortable key order broken at %d: %x >= %x", w.name, i, keys[i-1], keys[i])
			}
		}
		for _, v := range w.vals {
			enc, err := EncodeKey([]Value{v})
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodeKey(enc, []Type{w.typ})
			if err != nil {
				t.Fatal(err)
			}
			if got[0].Int != v.Int {
				t.Fatalf("%s: key roundtrip mismatch got %d want %d", w.name, got[0].Int, v.Int)
			}
		}
	}
}

func TestIntCmpAndCrossWidth(t *testing.T) {
	c, err := Int32Value(-5).Cmp(Int32Value(3))
	if err != nil || c >= 0 {
		t.Fatalf("cmp = %d, %v", c, err)
	}
	// Different widths compare by numeric value (both sign-extended to int64).
	c2, err := Int8Value(100).Cmp(Int32Value(100))
	if err != nil || c2 != 0 {
		t.Fatalf("cross-width cmp = %d, %v", c2, err)
	}
	if _, err := Int32Value(1).Cmp(StringValue("x")); err == nil {
		t.Fatal("expected type mismatch between INT32 and STRING")
	}
}

func TestIntCoerce(t *testing.T) {
	// Narrowing within range succeeds.
	v, err := Coerce(Int32Value(100), Int8())
	if err != nil || v.Int != 100 {
		t.Fatalf("narrow in range: %+v %v", v, err)
	}
	// Narrowing out of range errors rather than wrapping.
	if _, err := Coerce(Int32Value(300), Int8()); err == nil {
		t.Fatal("expected 300 to overflow INT8")
	}
	// Widening always succeeds.
	w, err := Coerce(Int8Value(-1), Int64())
	if err != nil || w.Int != -1 {
		t.Fatalf("widen: %+v %v", w, err)
	}
	// DECIMAL <-> int, both directions.
	d, err := Coerce(Int16Value(42), Type{Kind: KindDecimal})
	if err != nil || d.Dec.String() != "42" {
		t.Fatalf("int->decimal: %+v %v", d, err)
	}
	back, err := Coerce(DecimalValue(DecimalFromInt64(42), Type{Kind: KindDecimal}), Int16())
	if err != nil || back.Int != 42 {
		t.Fatalf("decimal->int: %+v %v", back, err)
	}
	if _, err := Coerce(DecimalValue(mustParseDecimalForTest(t, "1.5"), Type{Kind: KindDecimal}), Int32()); err == nil {
		t.Fatal("expected fractional DECIMAL to be rejected coercing to INT32")
	}
	// Decimal text parses then range-checks the same way.
	fromText, err := Coerce(StringValue("42"), Int8())
	if err != nil || fromText.Int != 42 {
		t.Fatalf("text->int: %+v %v", fromText, err)
	}
	if _, err := Coerce(StringValue("300"), Int8()); err == nil {
		t.Fatal("expected out-of-range text to be rejected coercing to INT8")
	}
	// int -> STRING/TEXT formats as plain decimal digits.
	s, err := Coerce(Int32Value(-7), String())
	if err != nil || s.Str != "-7" {
		t.Fatalf("int->string: %+v %v", s, err)
	}
	// Deliberately isolated from BLOB/UUID/BOOL/JSON/geo.
	if _, err := Coerce(BlobValue([]byte{1}), Int8()); err == nil {
		t.Fatal("expected INT8 to stay isolated from BLOB coercion")
	}
	if _, err := Coerce(BoolValue(true), Int8()); err == nil {
		t.Fatal("expected INT8 to stay isolated from BOOL coercion")
	}
}

func mustParseDecimalForTest(t *testing.T, s string) Decimal {
	t.Helper()
	d, err := ParseDecimal(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestNewIntRange(t *testing.T) {
	if _, err := NewInt(KindInt8, 128); err == nil {
		t.Fatal("expected 128 to overflow INT8")
	}
	if _, err := NewInt(KindInt8, -129); err == nil {
		t.Fatal("expected -129 to overflow INT8")
	}
	if v, err := NewInt(KindInt64, math.MaxInt64); err != nil || v.Int != math.MaxInt64 {
		t.Fatalf("int64 max: %+v %v", v, err)
	}
	if _, err := NewInt(KindString, 1); err == nil {
		t.Fatal("expected NewInt to reject a non-integer kind")
	}
}

func TestUintKeyOrderAndRoundTrip(t *testing.T) {
	widths := []struct {
		name string
		typ  Type
		vals []Value
	}{
		{"uint8", Uint8(), []Value{Uint8Value(0), Uint8Value(1), Uint8Value(math.MaxUint8)}},
		{"uint16", Uint16(), []Value{Uint16Value(0), Uint16Value(1), Uint16Value(math.MaxUint16)}},
		{"uint32", Uint32(), []Value{Uint32Value(0), Uint32Value(1), Uint32Value(math.MaxUint32)}},
		{"uint64", Uint64(), []Value{Uint64Value(0), Uint64Value(1), Uint64Value(math.MaxUint64)}},
	}
	for _, w := range widths {
		var keys [][]byte
		for _, v := range w.vals {
			k, err := EncodeKey([]Value{v})
			if err != nil {
				t.Fatalf("%s: %v", w.name, err)
			}
			keys = append(keys, k)
		}
		for i := 1; i < len(keys); i++ {
			if bytes.Compare(keys[i-1], keys[i]) >= 0 {
				t.Fatalf("%s: sortable key order broken at %d: %x >= %x", w.name, i, keys[i-1], keys[i])
			}
		}
		for _, v := range w.vals {
			enc, err := EncodeKey([]Value{v})
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodeKey(enc, []Type{w.typ})
			if err != nil {
				t.Fatal(err)
			}
			if got[0].Uint != v.Uint {
				t.Fatalf("%s: key roundtrip mismatch got %d want %d", w.name, got[0].Uint, v.Uint)
			}
		}
	}
}

func TestUintCmpAndCrossWidth(t *testing.T) {
	c, err := Uint32Value(3).Cmp(Uint32Value(5))
	if err != nil || c >= 0 {
		t.Fatalf("cmp = %d, %v", c, err)
	}
	// Different widths compare by numeric value (both zero-extended to uint64).
	c2, err := Uint8Value(100).Cmp(Uint32Value(100))
	if err != nil || c2 != 0 {
		t.Fatalf("cross-width cmp = %d, %v", c2, err)
	}
	if _, err := Uint32Value(1).Cmp(StringValue("x")); err == nil {
		t.Fatal("expected type mismatch between UINT32 and STRING")
	}
	// Signed and unsigned stay isolated for a direct Cmp call (the eval.go
	// binary-comparison path coerces one side to match first).
	if _, err := Uint32Value(1).Cmp(Int32Value(1)); err == nil {
		t.Fatal("expected type mismatch between UINT32 and INT32 for a direct Cmp")
	}
}

func TestUintCoerce(t *testing.T) {
	// Narrowing within range succeeds.
	v, err := Coerce(Uint32Value(100), Uint8())
	if err != nil || v.Uint != 100 {
		t.Fatalf("narrow in range: %+v %v", v, err)
	}
	// Narrowing out of range errors rather than wrapping.
	if _, err := Coerce(Uint32Value(300), Uint8()); err == nil {
		t.Fatal("expected 300 to overflow UINT8")
	}
	// Widening always succeeds.
	w, err := Coerce(Uint8Value(255), Uint64())
	if err != nil || w.Uint != 255 {
		t.Fatalf("widen: %+v %v", w, err)
	}
	// DECIMAL <-> uint, both directions, including a magnitude above
	// math.MaxInt64.
	d, err := Coerce(Uint64Value(math.MaxUint64), Type{Kind: KindDecimal})
	if err != nil || d.Dec.String() != "18446744073709551615" {
		t.Fatalf("uint->decimal: %+v %v", d, err)
	}
	back, err := Coerce(d, Uint64())
	if err != nil || back.Uint != math.MaxUint64 {
		t.Fatalf("decimal->uint: %+v %v", back, err)
	}
	if _, err := Coerce(DecimalValue(mustParseDecimalForTest(t, "1.5"), Type{Kind: KindDecimal}), Uint32()); err == nil {
		t.Fatal("expected fractional DECIMAL to be rejected coercing to UINT32")
	}
	if _, err := Coerce(DecimalValue(mustParseDecimalForTest(t, "-1"), Type{Kind: KindDecimal}), Uint32()); err == nil {
		t.Fatal("expected negative DECIMAL to be rejected coercing to UINT32")
	}
	// Decimal text parses then range-checks the same way.
	fromText, err := Coerce(StringValue("42"), Uint8())
	if err != nil || fromText.Uint != 42 {
		t.Fatalf("text->uint: %+v %v", fromText, err)
	}
	if _, err := Coerce(StringValue("300"), Uint8()); err == nil {
		t.Fatal("expected out-of-range text to be rejected coercing to UINT8")
	}
	// uint -> STRING/TEXT formats as plain decimal digits.
	s, err := Coerce(Uint32Value(7), String())
	if err != nil || s.Str != "7" {
		t.Fatalf("uint->string: %+v %v", s, err)
	}
	// Signed <-> unsigned coerce directly, range/sign checked either way
	// (D3 treats both fixed-width families as one coercible group).
	fromInt, err := Coerce(Int32Value(7), Uint32())
	if err != nil || fromInt.Uint != 7 {
		t.Fatalf("int->uint: %+v %v", fromInt, err)
	}
	if _, err := Coerce(Int32Value(-1), Uint32()); err == nil {
		t.Fatal("expected negative INT32 to be rejected coercing to UINT32")
	}
	toInt, err := Coerce(Uint32Value(7), Int32())
	if err != nil || toInt.Int != 7 {
		t.Fatalf("uint->int: %+v %v", toInt, err)
	}
	if _, err := Coerce(Uint64Value(math.MaxUint64), Int64()); err == nil {
		t.Fatal("expected out-of-range UINT64 to be rejected coercing to INT64")
	}
	// Deliberately isolated from BLOB/UUID/BOOL/JSON/geo.
	if _, err := Coerce(BlobValue([]byte{1}), Uint8()); err == nil {
		t.Fatal("expected UINT8 to stay isolated from BLOB coercion")
	}
	if _, err := Coerce(BoolValue(true), Uint8()); err == nil {
		t.Fatal("expected UINT8 to stay isolated from BOOL coercion")
	}
}

func TestNewUintRange(t *testing.T) {
	if _, err := NewUint(KindUint8, 256); err == nil {
		t.Fatal("expected 256 to overflow UINT8")
	}
	if v, err := NewUint(KindUint64, math.MaxUint64); err != nil || v.Uint != math.MaxUint64 {
		t.Fatalf("uint64 max: %+v %v", v, err)
	}
	if _, err := NewUint(KindString, 1); err == nil {
		t.Fatal("expected NewUint to reject a non-unsigned-integer kind")
	}
}

// TestDateParseFormatRoundTrip covers D5 (Datatype expansion track): ISO
// 8601 parse/format, pre-epoch negative day counts, and leap-day handling.
func TestDateParseFormatRoundTrip(t *testing.T) {
	cases := []struct {
		s    string
		days int64
	}{
		{"1970-01-01", 0},
		{"1970-01-02", 1},
		{"1969-12-31", -1},
		{"2000-02-29", 11016}, // leap day
		{"0001-01-01", -719162},
	}
	for _, c := range cases {
		v, err := ParseDate(c.s)
		if err != nil {
			t.Fatalf("ParseDate(%q): %v", c.s, err)
		}
		if v.Typ.Kind != KindDate || v.Int != c.days {
			t.Fatalf("ParseDate(%q) = %+v, want days=%d", c.s, v, c.days)
		}
		if got := v.String(); got != c.s {
			t.Fatalf("round trip %q -> %q", c.s, got)
		}
	}
	if _, err := ParseDate("2024-02-30"); err == nil {
		t.Fatal("expected Feb 30 to be rejected (not a real calendar date)")
	}
	if _, err := ParseDate("not-a-date"); err == nil {
		t.Fatal("expected malformed date text to be rejected")
	}
}

// TestDateRowAndKeyRoundTrip mirrors TestBlobRowRoundTrip/
// TestBlobKeyOrderAndRoundTrip: heap-row and sortable-key encode/decode,
// including a straddle-the-epoch ordering check (the case a sign-bit-flip
// bug would get wrong).
func TestDateRowAndKeyRoundTrip(t *testing.T) {
	pre, _ := ParseDate("1969-12-31")  // days = -1
	post, _ := ParseDate("1970-01-02") // days = 1
	vals := []Value{pre, Null(Date()), post}
	cols := []Type{Date(), Date(), Date()}
	enc, err := EncodeRow(vals)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRow(enc, cols)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Int != -1 || !got[1].Null || got[2].Int != 1 {
		t.Fatalf("date row round trip mismatch: %+v", got)
	}

	a, err := EncodeKey([]Value{pre})
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncodeKey([]Value{post})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Compare(a, b) >= 0 {
		t.Fatalf("date key order: pre-epoch %x should sort before post-epoch %x", a, b)
	}
	kgot, err := DecodeKey(a, []Type{Date()})
	if err != nil {
		t.Fatal(err)
	}
	if kgot[0].Int != -1 {
		t.Fatalf("date key round trip: got %d want -1", kgot[0].Int)
	}
}

func TestNewDateRange(t *testing.T) {
	if _, err := NewDate(math.MaxInt32 + 1); err == nil {
		t.Fatal("expected day count beyond int32 to be rejected")
	}
	if v, err := NewDate(math.MinInt32); err != nil || v.Int != math.MinInt32 {
		t.Fatalf("min int32 day count: %+v %v", v, err)
	}
}

// TestTimeOfDayParseFormatRoundTrip covers TIME's ISO 8601 parse/format,
// including nanosecond-precision fractional seconds and trailing-zero
// trimming on format.
func TestTimeOfDayParseFormatRoundTrip(t *testing.T) {
	cases := []struct {
		s  string
		ns int64
	}{
		{"00:00:00", 0},
		{"23:59:59.999999999", 86399999999999},
		{"12:00:00.5", 12*int64(3600e9) + 500000000},
		{"01:02:03", 1*3600e9 + 2*60e9 + 3e9},
	}
	for _, c := range cases {
		v, err := ParseTimeOfDay(c.s)
		if err != nil {
			t.Fatalf("ParseTimeOfDay(%q): %v", c.s, err)
		}
		if v.Typ.Kind != KindTime || v.Time != c.ns {
			t.Fatalf("ParseTimeOfDay(%q) = %+v, want ns=%d", c.s, v, c.ns)
		}
	}
	if got := TimeOfDayValue(0).String(); got != "00:00:00" {
		t.Fatalf("format 0ns = %q, want 00:00:00", got)
	}
	if got := TimeOfDayValue(500000000).String(); got != "00:00:00.5" {
		t.Fatalf("format 0.5s = %q, want 00:00:00.5", got)
	}
	if _, err := ParseTimeOfDay("24:00:00"); err == nil {
		t.Fatal("expected hour 24 to be rejected")
	}
	if _, err := ParseTimeOfDay("not-a-time"); err == nil {
		t.Fatal("expected malformed time text to be rejected")
	}
}

// TestTimeOfDayRowAndKeyRoundTrip mirrors TestDateRowAndKeyRoundTrip: TIME
// uses plain unsigned byte order (no sign-bit flip, always non-negative).
func TestTimeOfDayRowAndKeyRoundTrip(t *testing.T) {
	lo := TimeOfDayValue(0)
	hi := TimeOfDayValue(86399999999999)
	vals := []Value{lo, Null(TimeOfDay()), hi}
	cols := []Type{TimeOfDay(), TimeOfDay(), TimeOfDay()}
	enc, err := EncodeRow(vals)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRow(enc, cols)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Time != 0 || !got[1].Null || got[2].Time != 86399999999999 {
		t.Fatalf("time row round trip mismatch: %+v", got)
	}

	a, err := EncodeKey([]Value{lo})
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncodeKey([]Value{hi})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Compare(a, b) >= 0 {
		t.Fatalf("time key order: %x should sort before %x", a, b)
	}
}

func TestNewTimeOfDayRange(t *testing.T) {
	if _, err := NewTimeOfDay(-1); err == nil {
		t.Fatal("expected negative nanoseconds to be rejected")
	}
	if _, err := NewTimeOfDay(86400 * 1e9); err == nil {
		t.Fatal("expected exactly one day of nanoseconds to be rejected (exclusive bound)")
	}
	if v, err := NewTimeOfDay(86399999999999); err != nil || v.Time != 86399999999999 {
		t.Fatalf("max valid time: %+v %v", v, err)
	}
}

// TestDateTimeCmpIsolated confirms DATE and TIME reject cross-kind Cmp
// (mirrors TestBlobCmp) and stay isolated from every non-text Coerce source.
func TestDateTimeCmpIsolated(t *testing.T) {
	d, _ := ParseDate("2024-01-01")
	if _, err := d.Cmp(StringValue("x")); err == nil {
		t.Fatal("expected type mismatch between DATE and STRING")
	}
	tm, _ := ParseTimeOfDay("00:00:00")
	if _, err := tm.Cmp(d); err == nil {
		t.Fatal("expected type mismatch between TIME and DATE")
	}
	if _, err := Coerce(Int32Value(0), Date()); err == nil {
		t.Fatal("expected DATE to stay isolated from INT32 coercion")
	}
	if _, err := Coerce(DecimalValue(Decimal{}, Type{Kind: KindDecimal}), TimeOfDay()); err == nil {
		t.Fatal("expected TIME to stay isolated from DECIMAL coercion")
	}
	// STRING/TEXT coercion is the one path in.
	if _, err := Coerce(StringValue("2024-01-01"), Date()); err != nil {
		t.Fatalf("expected DATE to coerce from STRING: %v", err)
	}
	if _, err := Coerce(TextValue("00:00:00"), TimeOfDay()); err != nil {
		t.Fatalf("expected TIME to coerce from TEXT: %v", err)
	}
	// And Coerce to STRING/TEXT formats via Value.String().
	s, err := Coerce(d, String())
	if err != nil || s.Str != "2024-01-01" {
		t.Fatalf("DATE->STRING: %+v %v", s, err)
	}
}
