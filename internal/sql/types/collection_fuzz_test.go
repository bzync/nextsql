package types

import "testing"

// FuzzDecodeCollectionRow feeds arbitrary bytes to the nested heap-row and
// sortable-key collection decoders (untrusted-decoder entry points per
// SKILLS.md) against a fixed nested collection column type. A decode must
// either fail cleanly or produce a value that re-encodes without panicking.
func FuzzDecodeCollectionRow(f *testing.F) {
	st, _ := StructType([]Field{
		{Name: "a", Type: Int32()},
		{Name: "b", Type: String()},
		{Name: "c", Type: mustFuzzArray(Int64())},
	})
	mp, _ := MapType(String(), mustFuzzArray(Int32()))
	cols := []Type{st, mustFuzzArray(st), mp}

	// A couple of valid encodings as seeds.
	seed := func(vals []Value) {
		if raw, err := EncodeRow(vals); err == nil {
			f.Add(raw, 0)
			f.Add(raw, 1)
			f.Add(raw, 2)
		}
	}
	sv, _ := Coerce(StructValue(st, []Value{Int32Value(1), StringValue("x"),
		Value{Typ: mustFuzzArray(Int64()), Coll: []Value{Int64Value(9)}}}), st)
	seed([]Value{sv, Value{Typ: mustFuzzArray(st), Coll: []Value{sv}}, Null(mp)})

	f.Fuzz(func(t *testing.T, raw []byte, which int) {
		if which < 0 || which >= len(cols) {
			return
		}
		col := cols[which]
		vals, err := DecodeRow(raw, []Type{col})
		if err != nil {
			return
		}
		// A successfully decoded value must survive a re-encode.
		if _, err := EncodeRow(vals); err != nil {
			t.Fatalf("re-encode after decode failed: %v", err)
		}
		if _, err := EncodeKey(vals); err == nil {
			if _, err := DecodeKey(mustEncodeKey(t, vals), []Type{col}); err != nil {
				t.Fatalf("key round trip failed: %v", err)
			}
		}
	})
}

func mustFuzzArray(elem Type) Type {
	at, err := ArrayType(elem)
	if err != nil {
		panic(err)
	}
	return at
}

func mustEncodeKey(t *testing.T, vals []Value) []byte {
	t.Helper()
	k, err := EncodeKey(vals)
	if err != nil {
		t.Fatalf("EncodeKey: %v", err)
	}
	return k
}
