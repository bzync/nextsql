package vector

import "testing"

func FuzzDecodePayload(f *testing.F) {
	raw, err := EncodePayload([]float32{1, -2, 0.5})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(raw)
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
	f.Fuzz(func(t *testing.T, in []byte) {
		_, err := DecodeMeta(in)
		if err == nil && len(in) < 22 {
			t.Fatal("decoded truncated meta")
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
	f.Add([]byte{1, 0, 0, 1})
	f.Fuzz(func(t *testing.T, in []byte) {
		_, err := DecodeNode(in)
		if err == nil && len(in) < 4 {
			t.Fatal("decoded truncated node")
		}
	})
}
