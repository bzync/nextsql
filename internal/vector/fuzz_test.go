package vector

import (
	"testing"

	"github.com/bzync/nextsql/internal/sql/types"
)

func FuzzDecodePayload(f *testing.F) {
	raw, err := EncodePayload([]float32{1, -2, 0.5})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(raw)
	if q, err := EncodePayloadElem([]float32{1, -2, 0.5, 0.1}, types.VecF16); err == nil {
		f.Add(q)
	}
	if q, err := EncodePayloadElem([]float32{1, -2, 0.5, 0.1}, types.VecI8); err == nil {
		f.Add(q)
	}
	if q, err := EncodePayloadElem([]float32{1, 0, 1, 1, 0, 1, 0, 0, 1}, types.VecBit); err == nil {
		f.Add(q)
	}
	f.Add([]byte("NSVV"))
	f.Add([]byte{0, 1, 2, 255})
	f.Fuzz(func(t *testing.T, in []byte) {
		_, err := DecodePayload(in)
		if err == nil && len(in) < 7 {
			t.Fatal("decoded truncated payload")
		}
	})
}

func FuzzDecodeMeta(f *testing.F) {
	raw, err := EncodeMeta(DefaultMeta(8, MetricCosine))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(raw)
	f.Add([]byte("NSHM"))
	q := DefaultMeta(8, MetricCosine)
	q.Quant = types.VecI8
	q.Entry = []byte("pk")
	if qraw, qerr := EncodeMeta(q); qerr == nil {
		f.Add(qraw)
	}
	h := DefaultMeta(8, MetricHamming)
	h.Entry = []byte("pk")
	if hraw, herr := EncodeMeta(h); herr == nil {
		f.Add(hraw)
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		_, err := DecodeMeta(in)
		if err == nil && len(in) < 22 {
			t.Fatal("decoded truncated meta")
		}
	})
}

func FuzzDecodeIVFList(f *testing.F) {
	if raw, err := EncodeIVFList([][]byte{
		[]byte("row-0001"), []byte("row-0002"), []byte("row-0010"), []byte("row-0100"),
	}); err == nil {
		f.Add(raw)
	}
	if raw, err := EncodeIVFList(nil); err == nil {
		f.Add(raw)
	}
	f.Add([]byte("NSIL"))
	f.Add([]byte{'N', 'S', 'I', 'L', 1, 0x02, 0x05, 0x01, 'x'})
	// Oversized shared/suffix varints must not allocate.
	f.Add([]byte("NSIL\x01\x02\x97\x97\x97\x97\x97\x97\x97\x97\xab\x01"))
	f.Fuzz(func(t *testing.T, in []byte) {
		got, err := DecodeIVFList(in)
		if err != nil {
			return
		}
		re, err := EncodeIVFList(got)
		if err != nil {
			t.Fatalf("re-encode a decoded posting list: %v", err)
		}
		again, err := DecodeIVFList(re)
		if err != nil {
			t.Fatalf("decode a re-encoded posting list: %v", err)
		}
		if len(again) != len(got) {
			t.Fatalf("posting list length changed on round trip: %d vs %d", len(again), len(got))
		}
	})
}

func FuzzDecodeIVFMeta(f *testing.F) {
	m := DefaultIVFMeta(8, MetricCosine, 4)
	m.Trained = true
	m.Count = 10
	if raw, err := EncodeIVFMeta(m); err == nil {
		f.Add(raw)
	}
	f.Add([]byte("NSIV"))
	f.Add(make([]byte, 25))
	f.Fuzz(func(t *testing.T, in []byte) {
		_, err := DecodeIVFMeta(in)
		if err == nil && len(in) != 25 {
			t.Fatal("decoded wrong-length IVF meta")
		}
	})
}

func FuzzDecodePQList(f *testing.F) {
	if raw, err := EncodePQList([]PQEntry{
		{PK: []byte("row-0001"), Code: []byte{1, 2, 3, 4}},
		{PK: []byte("row-0002"), Code: []byte{5, 6, 7, 8}},
		{PK: []byte("row-0100"), Code: []byte{9, 10, 11, 12}},
	}, 4); err == nil {
		f.Add(raw)
	}
	if raw, err := EncodePQList(nil, 4); err == nil {
		f.Add(raw)
	}
	f.Add([]byte("NSPL"))
	f.Add([]byte{'N', 'S', 'P', 'L', 1, 0x04, 0x01, 0x05, 0x01, 'x', 0, 0, 0, 0})
	// Oversized shared/suffix varints must not allocate.
	f.Add([]byte("NSPL\x01\x04\x01\x97\x97\x97\x97\x97\x97\x97\x97\xab\x01"))
	f.Fuzz(func(t *testing.T, in []byte) {
		got, err := DecodePQList(in)
		if err != nil {
			return
		}
		m := 1
		if len(got) > 0 {
			m = len(got[0].Code)
		}
		re, err := EncodePQList(got, m)
		if err != nil {
			t.Fatalf("re-encode a decoded IVF-PQ posting list: %v", err)
		}
		again, err := DecodePQList(re)
		if err != nil {
			t.Fatalf("decode a re-encoded IVF-PQ posting list: %v", err)
		}
		if len(again) != len(got) {
			t.Fatalf("IVF-PQ posting list length changed on round trip: %d vs %d", len(again), len(got))
		}
	})
}

func FuzzDecodePQCodebook(f *testing.F) {
	cb := &PQCodebook{M: 2, SubDim: 3, Ksub: 2, Sub: [][][]float32{
		{{1, 2, 3}, {-1, -2, -3}},
		{{0, 0, 1}, {4, 5, 6}},
	}}
	if raw, err := EncodePQCodebook(cb); err == nil {
		f.Add(raw)
	}
	f.Add([]byte("NSPC"))
	f.Add(make([]byte, 11))
	// Oversized header must not allocate.
	f.Add([]byte("NSPC\x01\xff\xff\xff\xff\xff\xff"))
	f.Fuzz(func(t *testing.T, in []byte) {
		_, _ = DecodePQCodebook(in)
	})
}

func FuzzDecodeIVFPQMeta(f *testing.F) {
	m := DefaultIVFPQMeta(8, MetricL2, 4, 4)
	m.Trained = true
	m.Count = 10
	if raw, err := EncodeIVFPQMeta(m); err == nil {
		f.Add(raw)
	}
	f.Add([]byte("NSPQ"))
	f.Add(make([]byte, 32))
	f.Fuzz(func(t *testing.T, in []byte) {
		_, err := DecodeIVFPQMeta(in)
		if err == nil && len(in) != 32 {
			t.Fatal("decoded wrong-length IVF-PQ meta")
		}
	})
}

func FuzzDecodeSparse(f *testing.F) {
	sv, err := NewSparseVec(64, []uint32{0, 3, 9, 40}, []float32{1, -0.5, 2, 4})
	if err != nil {
		f.Fatal(err)
	}
	raw, err := EncodeSparse(sv)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(raw)
	if empty, err := NewSparseVec(8, nil, nil); err == nil {
		if eraw, eerr := EncodeSparse(empty); eerr == nil {
			f.Add(eraw)
		}
	}
	f.Add([]byte("NSSV"))
	f.Add(make([]byte, 13))
	// Overflowing index delta must not decode (10-byte 0xff varint).
	f.Add([]byte{'N', 'S', 'S', 'V', 1, 8, 0, 0, 0, 1, 0, 0, 0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 1, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, in []byte) {
		got, err := DecodeSparse(in)
		if err != nil {
			return
		}
		re, err := EncodeSparse(got)
		if err != nil {
			t.Fatalf("re-encode a decoded sparse vector: %v", err)
		}
		again, err := DecodeSparse(re)
		if err != nil {
			t.Fatalf("decode a re-encoded sparse vector: %v", err)
		}
		if again.Dim != got.Dim || len(again.Indices) != len(got.Indices) {
			t.Fatalf("sparse vector shape changed on round trip")
		}
	})
}

func FuzzDecodeSparseList(f *testing.F) {
	if raw, err := EncodeSparseList([]SparsePosting{
		{PK: []byte("row-0001"), Value: 1.5},
		{PK: []byte("row-0002"), Value: -0.25},
		{PK: []byte("row-0100"), Value: 4},
	}); err == nil {
		f.Add(raw)
	}
	if raw, err := EncodeSparseList(nil); err == nil {
		f.Add(raw)
	}
	f.Add([]byte("NSSP"))
	f.Add([]byte{'N', 'S', 'S', 'P', 1, 0x01, 0x05, 0x01, 'x', 0, 0, 0x80, 0x3f})
	// Oversized shared/suffix varints must not allocate.
	f.Add([]byte("NSSP\x01\x02\x97\x97\x97\x97\x97\x97\x97\x97\xab\x01"))
	f.Fuzz(func(t *testing.T, in []byte) {
		got, err := DecodeSparseList(in)
		if err != nil {
			return
		}
		re, err := EncodeSparseList(got)
		if err != nil {
			t.Fatalf("re-encode a decoded sparse posting list: %v", err)
		}
		again, err := DecodeSparseList(re)
		if err != nil {
			t.Fatalf("decode a re-encoded sparse posting list: %v", err)
		}
		if len(again) != len(got) {
			t.Fatalf("sparse posting list length changed on round trip: %d vs %d", len(again), len(got))
		}
	})
}

func FuzzDecodeSparseMeta(f *testing.F) {
	m := DefaultSparseMeta(64, MetricCosine)
	m.Count = 10
	if raw, err := EncodeSparseMeta(m); err == nil {
		f.Add(raw)
	}
	f.Add([]byte("NSSM"))
	f.Add(make([]byte, 21))
	f.Fuzz(func(t *testing.T, in []byte) {
		_, err := DecodeSparseMeta(in)
		if err == nil && len(in) != 21 {
			t.Fatal("decoded wrong-length sparse meta")
		}
	})
}

func FuzzDecodeNode(f *testing.F) {
	n := Node{Level: 0, Neighbors: [][][]byte{{{1, 2, 3}}}}
	raw, err := EncodeNode(n)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(raw)
	multi := Node{Level: 2, Neighbors: [][][]byte{
		{[]byte("row-0001"), []byte("row-0002"), []byte("row-0010")},
		{[]byte("row-0001"), []byte("row-0100")},
		{[]byte("row-0001")},
	}}
	if mraw, merr := EncodeNode(multi); merr == nil {
		f.Add(mraw)
	}
	// A hand-built node format v1 blob keeps the legacy decoder fuzzed.
	f.Add([]byte{1, 0, 0, 1, 0x01, 0x00, 0x02, 0x00, 'p', 'k'})
	f.Add([]byte{1, 0, 0, 1})
	f.Add([]byte{2, 0, 0, 1})
	// v2 blob with an oversized suffix-length varint (must not allocate).
	f.Add([]byte("\x02\x020\x030\x97\x97\x97\x97\x97\x97\x97\x97\xab\x01\x00"))
	f.Fuzz(func(t *testing.T, in []byte) {
		got, err := DecodeNode(in)
		if err == nil && len(in) < 4 {
			t.Fatal("decoded truncated node")
		}
		if err != nil {
			return
		}
		// A decodable node must re-encode and round-trip identically.
		re, err := EncodeNode(got)
		if err != nil {
			t.Fatalf("re-encode a decoded node: %v", err)
		}
		again, err := DecodeNode(re)
		if err != nil {
			t.Fatalf("decode a re-encoded node: %v", err)
		}
		if len(again.Neighbors) != len(got.Neighbors) {
			t.Fatal("neighbour layer count changed on round trip")
		}
	})
}
