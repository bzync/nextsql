package types

import (
	"bytes"
	"testing"
)

// TestCharCoercePadAndTrim covers CHAR(n)'s SQL-standard PADSPACE invariant
// (docs/design-datatypes.md D4): shorter input is right-padded to exactly n
// runes, longer input errors unless the excess is all spaces (then trimmed).
func TestCharCoercePadAndTrim(t *testing.T) {
	ct, err := CharType(5)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Coerce(StringValue("ab"), ct)
	if err != nil {
		t.Fatal(err)
	}
	if v.Typ.Kind != KindChar || v.Typ.Precision != 5 || v.Str != "ab   " {
		t.Fatalf("pad: %+v", v)
	}
	// Exactly n: unchanged.
	if v, err := Coerce(StringValue("abcde"), ct); err != nil || v.Str != "abcde" {
		t.Fatalf("exact: %+v %v", v, err)
	}
	// Over n but the excess is spaces: trimmed, not an error.
	if v, err := Coerce(StringValue("abc   "), ct); err != nil || v.Str != "abc  " {
		t.Fatalf("trim spaces: %q %v", v.Str, err)
	}
	// Over n with real content: error.
	if _, err := Coerce(StringValue("abcdef"), ct); err == nil {
		t.Fatal("expected CHAR(5) overflow to error")
	}
	// Multi-byte runes are counted as runes, not bytes.
	if v, err := Coerce(StringValue("héllo"), ct); err != nil || v.Str != "héllo" {
		t.Fatalf("rune count: %q %v", v.Str, err)
	}
}

// TestVarcharCoerceCeiling covers VARCHAR(n)'s length ceiling: over-length
// errors rather than truncating (mirrors D2/D3's "narrowing never wraps").
func TestVarcharCoerceCeiling(t *testing.T) {
	vt, err := VarcharType(4)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := Coerce(StringValue("abc"), vt); err != nil || v.Str != "abc" || v.Typ.Precision != 4 {
		t.Fatalf("under: %+v %v", v, err)
	}
	if v, err := Coerce(StringValue("abcd"), vt); err != nil || v.Str != "abcd" {
		t.Fatalf("exact: %+v %v", v, err)
	}
	if _, err := Coerce(StringValue("abcde"), vt); err == nil {
		t.Fatal("expected VARCHAR(4) overflow to error")
	}
}

// TestCharVarcharCrossCoerce covers the "close STRING/TEXT sibling" rule
// (docs/design-datatypes.md D4): CHAR/VARCHAR coerce out to STRING/TEXT and
// participate in the STRING source-side coercion paths (into INT, DATE, ...),
// with CHAR's space padding treated as insignificant.
func TestCharVarcharCrossCoerce(t *testing.T) {
	ct, _ := CharType(6)
	padded, _ := Coerce(StringValue("42"), ct) // "42    "

	// CHAR -> STRING keeps the stored (padded) form.
	s, err := Coerce(padded, String())
	if err != nil || s.Str != "42    " {
		t.Fatalf("char->string: %q %v", s.Str, err)
	}
	// CHAR -> INT32 parses the trimmed content.
	n, err := Coerce(padded, Int32())
	if err != nil || n.Int != 42 {
		t.Fatalf("char->int: %+v %v", n, err)
	}
	// CHAR -> DATE via ISO text (padding trimmed).
	dct, _ := CharType(12)
	dc, _ := Coerce(StringValue("2024-01-15"), dct)
	d, err := Coerce(dc, Date())
	if err != nil || d.String() != "2024-01-15" {
		t.Fatalf("char->date: %+v %v", d, err)
	}
	// STRING -> CHAR -> VARCHAR round path.
	vt, _ := VarcharType(10)
	vv, err := Coerce(padded, vt)
	if err != nil || vv.Str != "42    " {
		t.Fatalf("char->varchar: %q %v", vv.Str, err)
	}
}

// TestCharRowAndKeyRoundTrip covers heap-row and sortable-key encode/decode
// plus the canonical CHAR(n) order (byte-lexicographic over the padded form).
func TestCharRowAndKeyRoundTrip(t *testing.T) {
	ct, _ := CharType(4)
	a, _ := Coerce(StringValue("ab"), ct)  // "ab  "
	b, _ := Coerce(StringValue("abc"), ct) // "abc "
	c, _ := Coerce(StringValue("b"), ct)   // "b   "

	vals := []Value{a, Null(ct), b}
	enc, err := EncodeRow(vals)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRow(enc, []Type{ct, ct, ct})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Str != "ab  " || !got[1].Null || got[2].Str != "abc " {
		t.Fatalf("char row round trip: %+v", got)
	}

	ka, _ := EncodeKey([]Value{a})
	kb, _ := EncodeKey([]Value{b})
	kc, _ := EncodeKey([]Value{c})
	if bytes.Compare(ka, kb) >= 0 {
		t.Fatalf("CHAR key order: %q should sort before %q", a.Str, b.Str)
	}
	if bytes.Compare(kb, kc) >= 0 {
		t.Fatalf("CHAR key order: %q should sort before %q", b.Str, c.Str)
	}
	kgot, err := DecodeKey(ka, []Type{ct})
	if err != nil || kgot[0].Str != "ab  " {
		t.Fatalf("char key round trip: %+v %v", kgot, err)
	}
}

// TestCharCmp covers same-Kind comparison of already-padded CHAR values.
func TestCharCmp(t *testing.T) {
	ct, _ := CharType(3)
	x, _ := Coerce(StringValue("a"), ct)  // "a  "
	y, _ := Coerce(StringValue("ab"), ct) // "ab "
	if c, err := x.Cmp(y); err != nil || c >= 0 {
		t.Fatalf("expected 'a  ' < 'ab ': %d %v", c, err)
	}
	if c, err := x.Cmp(x); err != nil || c != 0 {
		t.Fatalf("expected equal: %d %v", c, err)
	}
}

func TestCharTypeBounds(t *testing.T) {
	if _, err := CharType(0); err == nil {
		t.Fatal("CHAR(0) should error")
	}
	if _, err := VarcharType(0); err == nil {
		t.Fatal("VARCHAR(0) should error")
	}
	if _, err := CharType(MaxCharLen); err != nil {
		t.Fatalf("CHAR(%d) should be valid: %v", MaxCharLen, err)
	}
}
