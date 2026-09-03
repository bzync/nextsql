package types

import (
	"bytes"
	"testing"
)

// TestEnumTypeBounds covers EnumType's construction invariants
// (docs/design-datatypes.md D11): 1..MaxEnumLabels labels, no duplicates, no
// empty/over-long labels.
func TestEnumTypeBounds(t *testing.T) {
	if _, err := EnumType(nil); err == nil {
		t.Fatal("ENUM with zero labels should error")
	}
	if _, err := EnumType([]string{"a", "a"}); err == nil {
		t.Fatal("ENUM with a duplicate label should error")
	}
	if _, err := EnumType([]string{""}); err == nil {
		t.Fatal("ENUM with an empty label should error")
	}
	long := make([]byte, 256)
	for i := range long {
		long[i] = 'x'
	}
	if _, err := EnumType([]string{string(long)}); err == nil {
		t.Fatal("ENUM with a 256-byte label should error")
	}
	et, err := EnumType([]string{"small", "medium", "large"})
	if err != nil {
		t.Fatal(err)
	}
	if et.Kind != KindEnum || et.Precision != 3 || len(et.EnumLabels) != 3 {
		t.Fatalf("unexpected type: %+v", et)
	}
}

// TestEnumValueMembership covers EnumValue/EnumValueByOrdinal validating
// against the declared label set.
func TestEnumValueMembership(t *testing.T) {
	et, _ := EnumType([]string{"small", "medium", "large"})
	v, err := EnumValue("medium", et)
	if err != nil || v.Int != 1 || v.Str != "medium" {
		t.Fatalf("EnumValue: %+v %v", v, err)
	}
	if _, err := EnumValue("huge", et); err == nil {
		t.Fatal("expected non-member label to error")
	}
	v2, err := EnumValueByOrdinal(2, et)
	if err != nil || v2.Str != "large" {
		t.Fatalf("EnumValueByOrdinal: %+v %v", v2, err)
	}
	if _, err := EnumValueByOrdinal(3, et); err == nil {
		t.Fatal("expected out-of-range ordinal to error")
	}
	if _, err := EnumValueByOrdinal(-1, et); err == nil {
		t.Fatal("expected negative ordinal to error")
	}
}

// TestEnumCoerceIsolated covers CAST to ENUM validating label-set membership
// and ENUM's isolation from every family but text (docs/design-datatypes.md
// D11, mirroring D1-D8's isolation precedent).
func TestEnumCoerceIsolated(t *testing.T) {
	et, _ := EnumType([]string{"small", "medium", "large"})

	// STRING/TEXT -> ENUM validates membership.
	v, err := Coerce(StringValue("large"), et)
	if err != nil || v.Str != "large" || v.Int != 2 {
		t.Fatalf("string->enum: %+v %v", v, err)
	}
	if _, err := Coerce(StringValue("huge"), et); err == nil {
		t.Fatal("expected non-member string to error")
	}
	if _, err := Coerce(TextValue("medium"), et); err != nil {
		t.Fatal(err)
	}

	// ENUM -> STRING/TEXT exposes the label.
	s, err := Coerce(v, String())
	if err != nil || s.Str != "large" {
		t.Fatalf("enum->string: %+v %v", s, err)
	}

	// Isolated from int/bool/blob/uuid: none of these coerce into ENUM.
	if _, err := Coerce(IntValue(KindInt32, 1), et); err == nil {
		t.Fatal("expected INT->ENUM to be rejected")
	}
	if _, err := Coerce(BoolValue(true), et); err == nil {
		t.Fatal("expected BOOL->ENUM to be rejected")
	}
	if _, err := Coerce(BlobValue([]byte("x")), et); err == nil {
		t.Fatal("expected BLOB->ENUM to be rejected")
	}

	// Two ENUM types with different label sets are different types: coercing
	// between them re-resolves the label against the destination's list.
	et2, _ := EnumType([]string{"large", "medium", "small"})
	v2, err := Coerce(v, et2)
	if err != nil || v2.Str != "large" || v2.Int != 0 {
		t.Fatalf("enum->enum re-resolve: %+v %v", v2, err)
	}
	et3, _ := EnumType([]string{"medium", "large"})
	if _, err := Coerce(StringValue("small"), et3); err == nil {
		t.Fatal("expected a label absent from the destination set to error")
	}
}

// TestEnumCmpDeclarationOrder covers the whole point of ENUM: comparison is
// by declaration position, not lexicographic (docs/design-datatypes.md D11).
// "large" sorts lexicographically before "medium" and "small", but here it
// must compare greatest.
func TestEnumCmpDeclarationOrder(t *testing.T) {
	et, _ := EnumType([]string{"small", "medium", "large"})
	small, _ := EnumValue("small", et)
	medium, _ := EnumValue("medium", et)
	large, _ := EnumValue("large", et)

	if c, err := small.Cmp(medium); err != nil || c >= 0 {
		t.Fatalf("expected small < medium: %d %v", c, err)
	}
	if c, err := medium.Cmp(large); err != nil || c >= 0 {
		t.Fatalf("expected medium < large: %d %v", c, err)
	}
	if c, err := large.Cmp(large); err != nil || c != 0 {
		t.Fatalf("expected equal: %d %v", c, err)
	}
}

// TestEnumRowAndKeyRoundTrip covers heap-row (u16 ordinal) and sortable-key
// encode/decode, and confirms the sortable key orders by declaration
// position rather than the label's lexicographic order.
func TestEnumRowAndKeyRoundTrip(t *testing.T) {
	et, _ := EnumType([]string{"small", "medium", "large"})
	small, _ := EnumValue("small", et)
	medium, _ := EnumValue("medium", et)
	large, _ := EnumValue("large", et)

	vals := []Value{small, Null(et), large}
	enc, err := EncodeRow(vals)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRow(enc, []Type{et, et, et})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Str != "small" || !got[1].Null || got[2].Str != "large" {
		t.Fatalf("enum row round trip: %+v", got)
	}

	// Declaration order is small(0) < medium(1) < large(2) — the exact
	// reverse of lexicographic order ("large" < "medium" < "small"). These
	// two assertions only hold under declaration-order comparison.
	ksmall, _ := EncodeKey([]Value{small})
	kmedium, _ := EncodeKey([]Value{medium})
	klarge, _ := EncodeKey([]Value{large})
	if bytes.Compare(ksmall, kmedium) >= 0 {
		t.Fatal("expected small's key to sort before medium's, by declaration order")
	}
	if bytes.Compare(kmedium, klarge) >= 0 {
		t.Fatal("expected medium's key to sort before large's, by declaration order")
	}

	kgot, err := DecodeKey(klarge, []Type{et})
	if err != nil || kgot[0].Str != "large" {
		t.Fatalf("enum key round trip: %+v %v", kgot, err)
	}
}

// TestEnumTypeEquals covers Type.Equals treating two ENUMs with different
// declared label lists as different types (needed for FK/coercion checks).
func TestEnumTypeEquals(t *testing.T) {
	a, _ := EnumType([]string{"x", "y"})
	b, _ := EnumType([]string{"x", "y"})
	c, _ := EnumType([]string{"x", "z"})
	if !a.Equals(b) {
		t.Fatal("expected identical label lists to be Equals")
	}
	if a.Equals(c) {
		t.Fatal("expected differing label lists to not be Equals")
	}
}
