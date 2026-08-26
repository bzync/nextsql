package btree

import "testing"

func FuzzDecodeLeaf(f *testing.F) {
	rec, err := encodeLeaf([]byte("key"), []byte("value"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(rec)
	f.Add([]byte{})
	f.Add([]byte{0, 1, 2, 3})
	f.Fuzz(func(t *testing.T, data []byte) {
		k, v, err := decodeLeaf(data)
		if err != nil {
			return
		}
		enc, err := encodeLeaf(k, v)
		if err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		k2, v2, err := decodeLeaf(enc)
		if err != nil {
			t.Fatal(err)
		}
		if string(k2) != string(k) || string(v2) != string(v) {
			t.Fatal("round trip mismatch")
		}
	})
}

func FuzzDecodeInternal(f *testing.F) {
	rec, err := encodeInternal([]byte("sep"), 4)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(rec)
	f.Add([]byte{0})
	f.Fuzz(func(t *testing.T, data []byte) {
		k, child, err := decodeInternal(data)
		if err != nil {
			return
		}
		enc, err := encodeInternal(k, child)
		if err != nil {
			return
		}
		k2, child2, err := decodeInternal(enc)
		if err != nil {
			t.Fatal(err)
		}
		if string(k2) != string(k) || child2 != child {
			t.Fatal("round trip mismatch")
		}
	})
}
