package types

import (
	"testing"
)

func mustStruct(t *testing.T, fields ...Field) Type {
	t.Helper()
	st, err := StructType(fields)
	if err != nil {
		t.Fatalf("StructType: %v", err)
	}
	return st
}

func mustArray(t *testing.T, elem Type) Type {
	t.Helper()
	at, err := ArrayType(elem)
	if err != nil {
		t.Fatalf("ArrayType: %v", err)
	}
	return at
}

func mustMap(t *testing.T, k, v Type) Type {
	t.Helper()
	mt, err := MapType(k, v)
	if err != nil {
		t.Fatalf("MapType: %v", err)
	}
	return mt
}

func TestCollectionTypeConstruction(t *testing.T) {
	if _, err := StructType(nil); err == nil {
		t.Error("empty STRUCT should error")
	}
	if _, err := StructType([]Field{{Name: "a", Type: Int32()}, {Name: "a", Type: Int64()}}); err == nil {
		t.Error("duplicate field name should error")
	}
	if _, err := MapType(JSON(), Int32()); err == nil {
		t.Error("JSON MAP key should error")
	}
	if _, err := MapType(mustArray(t, Int32()), Int32()); err == nil {
		t.Error("ARRAY MAP key should error")
	}
	// Nesting past MaxNestDepth.
	cur := Int32()
	var err error
	for i := 0; i < MaxNestDepth+2; i++ {
		cur, err = ArrayType(cur)
		if err != nil {
			break
		}
	}
	if err == nil {
		t.Error("nesting past MaxNestDepth should error")
	}

	st := mustStruct(t, Field{Name: "x", Type: Int32()}, Field{Name: "y", Type: String()})
	if got := st.String(); got != "STRUCT<x INT32, y STRING>" {
		t.Errorf("STRUCT String = %q", got)
	}
	at := mustArray(t, st)
	if got := at.String(); got != "ARRAY<STRUCT<x INT32, y STRING>>" {
		t.Errorf("ARRAY String = %q", got)
	}
	mt := mustMap(t, String(), mustArray(t, Int32()))
	if got := mt.String(); got != "MAP<STRING, ARRAY<INT32>>" {
		t.Errorf("MAP String = %q", got)
	}
	if !at.Equals(mustArray(t, mustStruct(t, Field{Name: "x", Type: Int32()}, Field{Name: "y", Type: String()}))) {
		t.Error("Equals should hold for identical nested types")
	}
	if at.Equals(mustArray(t, Int32())) {
		t.Error("Equals should not hold for different element types")
	}
}

// buildStruct coerces raw member values into st.
func buildStruct(t *testing.T, st Type, members ...Value) Value {
	t.Helper()
	v, err := Coerce(StructValue(st, members), st)
	if err != nil {
		t.Fatalf("build STRUCT: %v", err)
	}
	return v
}

func TestCollectionRowRoundTrip(t *testing.T) {
	st := mustStruct(t,
		Field{Name: "name", Type: String()},
		Field{Name: "age", Type: Int32()},
		Field{Name: "tags", Type: mustArray(t, String())},
	)
	arrT := mustArray(t, Int32())
	mapT := mustMap(t, String(), mustArray(t, Int32()))

	cols := []Type{Int64(), st, arrT, mapT, String()}

	structVal := buildStruct(t, st,
		StringValue("alice"),
		Int32Value(30),
		Coerce1(t, ArrayValue(mustArray(t, String()), []Value{StringValue("a"), StringValue("b")}), mustArray(t, String())),
	)
	arrVal := Coerce1(t, ArrayValue(arrT, []Value{Int32Value(3), Int32Value(1), Int32Value(2)}), arrT)
	mapVal := Coerce1(t, MapValue(mapT,
		[]Value{StringValue("z"), StringValue("a")},
		[]Value{
			Coerce1(t, ArrayValue(mustArray(t, Int32()), []Value{Int32Value(9)}), mustArray(t, Int32())),
			Coerce1(t, ArrayValue(mustArray(t, Int32()), []Value{Int32Value(1), Int32Value(2)}), mustArray(t, Int32())),
		}), mapT)

	row := []Value{Int64Value(7), structVal, arrVal, mapVal, StringValue("tail")}
	enc, err := EncodeRow(row)
	if err != nil {
		t.Fatalf("EncodeRow: %v", err)
	}
	got, err := DecodeRow(enc, cols)
	if err != nil {
		t.Fatalf("DecodeRow: %v", err)
	}
	for i := range row {
		if c, err := got[i].Cmp(row[i]); err != nil || c != 0 {
			t.Fatalf("column %d round trip mismatch: got %s want %s (err %v)", i, got[i].String(), row[i].String(), err)
		}
	}

	// MAP entries must come back in canonical key order ("a" before "z").
	if got[3].CollKeys[0].Str != "a" || got[3].CollKeys[1].Str != "z" {
		t.Errorf("MAP not in canonical key order: %s", got[3].String())
	}

	// Skip past a collection column to reach the tail string.
	tail, err := DecodeRowColumn(enc, cols, 4)
	if err != nil || tail.Str != "tail" {
		t.Fatalf("DecodeRowColumn(4) = %s, %v", tail.String(), err)
	}

	// Reach the array column alone.
	a3, err := DecodeRowColumn(enc, cols, 2)
	if err != nil {
		t.Fatalf("DecodeRowColumn(2): %v", err)
	}
	if len(a3.Coll) != 3 || a3.Coll[0].Int != 3 {
		t.Errorf("array column decode wrong: %s", a3.String())
	}
}

// Coerce1 is a test helper: coerce v to dest or fail.
func Coerce1(t *testing.T, v Value, dest Type) Value {
	t.Helper()
	out, err := Coerce(v, dest)
	if err != nil {
		t.Fatalf("Coerce to %s: %v", dest.String(), err)
	}
	return out
}

func TestCollectionSortableKeyOrder(t *testing.T) {
	arrT := mustArray(t, Int32())
	mk := func(ns ...int32) Value {
		els := make([]Value, len(ns))
		for i, n := range ns {
			els[i] = Int32Value(n)
		}
		return Coerce1(t, ArrayValue(arrT, els), arrT)
	}
	// Ascending by lexicographic tuple order: [] < [1] < [1,2] < [1,3] < [2].
	ordered := []Value{mk(), mk(1), mk(1, 2), mk(1, 3), mk(2)}
	var prevKey []byte
	for i, v := range ordered {
		key, err := EncodeKey([]Value{v})
		if err != nil {
			t.Fatalf("EncodeKey[%d]: %v", i, err)
		}
		if i > 0 && !(string(prevKey) < string(key)) {
			t.Errorf("key order broken at %d: %x !< %x", i, prevKey, key)
		}
		// Cmp must agree with byte order.
		if i > 0 {
			c, err := ordered[i-1].Cmp(v)
			if err != nil || c >= 0 {
				t.Errorf("Cmp order broken at %d: %d %v", i, c, err)
			}
		}
		dec, err := DecodeKey(key, []Type{arrT})
		if err != nil {
			t.Fatalf("DecodeKey[%d]: %v", i, err)
		}
		if c, err := dec[0].Cmp(v); err != nil || c != 0 {
			t.Errorf("key round trip mismatch at %d: %s vs %s", i, dec[0].String(), v.String())
		}
		prevKey = key
	}
}

func TestStructSortableKeyWithNulls(t *testing.T) {
	st := mustStruct(t, Field{Name: "a", Type: Int32()}, Field{Name: "b", Type: String()})
	withNull := StructValue(st, []Value{Null(Int32()), StringValue("x")})
	withVal := StructValue(st, []Value{Int32Value(-1), StringValue("x")})
	k1, err := EncodeKey([]Value{withNull})
	if err != nil {
		t.Fatalf("EncodeKey null: %v", err)
	}
	k2, err := EncodeKey([]Value{withVal})
	if err != nil {
		t.Fatalf("EncodeKey val: %v", err)
	}
	if !(string(k1) < string(k2)) {
		t.Errorf("NULL field should sort before a present field: %x !< %x", k1, k2)
	}
	dec, err := DecodeKey(k1, []Type{st})
	if err != nil {
		t.Fatalf("DecodeKey: %v", err)
	}
	if !dec[0].Coll[0].Null || dec[0].Coll[1].Str != "x" {
		t.Errorf("struct-with-null key round trip wrong: %s", dec[0].String())
	}
}

func TestMapCanonicalizeAndDuplicateRejection(t *testing.T) {
	mapT := mustMap(t, String(), Int32())
	_, err := Coerce(MapValue(mapT,
		[]Value{StringValue("k"), StringValue("k")},
		[]Value{Int32Value(1), Int32Value(2)}), mapT)
	if err == nil {
		t.Error("duplicate MAP key should be rejected")
	}
	m := Coerce1(t, MapValue(mapT,
		[]Value{StringValue("c"), StringValue("a"), StringValue("b")},
		[]Value{Int32Value(3), Int32Value(1), Int32Value(2)}), mapT)
	if m.CollKeys[0].Str != "a" || m.CollKeys[1].Str != "b" || m.CollKeys[2].Str != "c" {
		t.Errorf("MAP not canonicalized: %s", m.String())
	}
}

func TestCollectionCoerceMemberTypes(t *testing.T) {
	// A STRING member coerces into an INT32 field.
	st := mustStruct(t, Field{Name: "n", Type: Int32()})
	v, err := Coerce(StructValue(st, []Value{StringValue("42")}), st)
	if err != nil {
		t.Fatalf("member coercion: %v", err)
	}
	if v.Coll[0].Typ.Kind != KindInt32 || v.Coll[0].Int != 42 {
		t.Errorf("member not coerced: %s", v.Coll[0].String())
	}
	// Out-of-range member errors.
	if _, err := Coerce(StructValue(st, []Value{StringValue("999999999999")}), st); err == nil {
		t.Error("out-of-range member should error")
	}
	// Collection -> TEXT via String().
	txt, err := Coerce(v, Text())
	if err != nil || txt.Str != "{n: 42}" {
		t.Errorf("STRUCT->TEXT = %q, %v", txt.Str, err)
	}
}
