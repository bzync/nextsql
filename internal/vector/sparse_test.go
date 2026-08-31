package vector

import (
	"bytes"
	"math"
	"math/rand"
	"testing"
)

func randSparse(rng *rand.Rand, dim uint32, nnz int) SparseVec {
	if nnz > int(dim) {
		nnz = int(dim)
	}
	used := make(map[uint32]struct{}, nnz)
	idx := make([]uint32, 0, nnz)
	val := make([]float32, 0, nnz)
	for len(idx) < nnz {
		i := uint32(rng.Intn(int(dim)))
		if _, ok := used[i]; ok {
			continue
		}
		used[i] = struct{}{}
		// Positive weights keep inner-product ranking aligned with inverted-index
		// accumulation: a zero-overlap document scores 0 and ranks behind any
		// document that shares a term.
		v := float32(rng.Float64()) + 0.05
		idx = append(idx, i)
		val = append(val, v)
	}
	sv, err := NewSparseVec(dim, idx, val)
	if err != nil {
		panic(err)
	}
	return sv
}

func buildSparse(t *testing.T, dim uint32, n, nnz int, metric Metric) (*SparseMem, []SparseCand, *rand.Rand) {
	t.Helper()
	rng := rand.New(rand.NewSource(11))
	meta := DefaultSparseMeta(dim, metric)
	m := NewSparseMem(meta)
	cands := make([]SparseCand, n)
	for i := 0; i < n; i++ {
		sv := randSparse(rng, dim, nnz)
		pk := pk32(i)
		if err := AddSparse(m, pk, sv); err != nil {
			t.Fatalf("AddSparse(%d): %v", i, err)
		}
		cands[i] = SparseCand{PK: pk, Vec: sv}
	}
	if m.Meta.Count != uint64(n) {
		t.Fatalf("Count = %d, want %d", m.Meta.Count, n)
	}
	return m, cands, rng
}

func TestSparseVecRoundTrip(t *testing.T) {
	sv, err := NewSparseVec(1000, []uint32{3, 9, 0, 40}, []float32{1.5, -0.25, 2, 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(sv.Indices) != 4 || sv.Indices[0] != 0 || sv.Indices[1] != 3 || sv.Indices[2] != 9 || sv.Indices[3] != 40 {
		t.Fatalf("sorted indices = %v", sv.Indices)
	}
	raw, err := EncodeSparse(sv)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw[0:4], []byte(sparseVecMagic)) || raw[4] != sparseVecVersion {
		t.Fatalf("bad header %q", raw[:5])
	}
	got, err := DecodeSparse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Dim != sv.Dim || len(got.Indices) != len(sv.Indices) {
		t.Fatalf("got dim=%d nnz=%d", got.Dim, len(got.Indices))
	}
	for i := range sv.Indices {
		if got.Indices[i] != sv.Indices[i] || got.Values[i] != sv.Values[i] {
			t.Fatalf("entry[%d] = %d/%v want %d/%v", i, got.Indices[i], got.Values[i], sv.Indices[i], sv.Values[i])
		}
	}
	// Empty (all-zero) vector round trips.
	empty, err := NewSparseVec(8, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = EncodeSparse(empty)
	if err != nil {
		t.Fatal(err)
	}
	got, err = DecodeSparse(raw)
	if err != nil || got.Dim != 8 || len(got.Indices) != 0 {
		t.Fatalf("empty: %+v %v", got, err)
	}
	// Fail closed.
	for _, bad := range [][]byte{
		nil,
		[]byte("NSSV"),
		append([]byte("XXXX"), raw[4:]...),
		raw[:len(raw)-1],
	} {
		if _, err := DecodeSparse(bad); err == nil {
			t.Fatalf("decoded bad sparse vector %q", bad)
		}
	}
	// A delta that wraps uint64 must not decode as a small index.
	// dim=8, nnz=1, a 10-byte 0xff varint is MaxUint64.
	wrap := []byte{'N', 'S', 'S', 'V', 1, 8, 0, 0, 0, 1, 0, 0, 0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 1, 0, 0, 0, 0}
	if _, err := DecodeSparse(wrap); err == nil {
		t.Fatal("decoded overflowing sparse index delta")
	}
}

func TestNewSparseVecRejects(t *testing.T) {
	cases := []struct {
		name    string
		dim     uint32
		indices []uint32
		values  []float32
	}{
		{"len mismatch", 8, []uint32{1}, []float32{1, 2}},
		{"dim 0", 0, nil, nil},
		{"dim too big", MaxSparseDim + 1, []uint32{0}, []float32{1}},
		{"index oob", 8, []uint32{8}, []float32{1}},
		{"duplicate", 8, []uint32{1, 1}, []float32{1, 2}},
		{"zero value", 8, []uint32{1}, []float32{0}},
		{"nan", 8, []uint32{1}, []float32{float32(math.NaN())}},
		{"inf", 8, []uint32{1}, []float32{float32(math.Inf(1))}},
	}
	for _, tc := range cases {
		if _, err := NewSparseVec(tc.dim, tc.indices, tc.values); err == nil {
			t.Fatalf("%s: accepted", tc.name)
		}
	}
}

func TestSparseDot(t *testing.T) {
	a, err := NewSparseVec(16, []uint32{1, 3, 7}, []float32{2, 3, 4})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewSparseVec(16, []uint32{0, 3, 7, 9}, []float32{9, 5, 0.5, 1})
	if err != nil {
		t.Fatal(err)
	}
	// overlap at 3 and 7: 3*5 + 4*0.5 = 17
	if d := SparseDot(a, b); d != 17 {
		t.Fatalf("dot = %v, want 17", d)
	}
	if d := SparseDot(a, a); d != 4+9+16 {
		t.Fatalf("self dot = %v", d)
	}
	z, err := NewSparseVec(16, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d := SparseDot(a, z); d != 0 {
		t.Fatalf("dot with empty = %v", d)
	}
	if s := SparseSimilarity(MetricCosine, a, a); math.Abs(s-1) > 1e-6 {
		t.Fatalf("self cosine = %v", s)
	}
}

func TestSparseMetaRoundTrip(t *testing.T) {
	m := DefaultSparseMeta(1<<20, MetricIP)
	m.Count = 4096
	raw, err := EncodeSparseMeta(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != sparseMetaLen {
		t.Fatalf("meta len = %d, want %d", len(raw), sparseMetaLen)
	}
	got, err := DecodeSparseMeta(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != m {
		t.Fatalf("round trip: got %+v want %+v", got, m)
	}
	for _, bad := range [][]byte{
		nil,
		[]byte("NSSM"),
		append([]byte("XXXX"), raw[4:]...),
		raw[:len(raw)-1],
	} {
		if _, err := DecodeSparseMeta(bad); err == nil {
			t.Fatalf("decoded bad meta %q", bad)
		}
	}
	bad := DefaultSparseMeta(64, MetricL2)
	if _, err := EncodeSparseMeta(bad); err == nil {
		t.Fatal("encoded sparse meta with L2")
	}
	bad = DefaultSparseMeta(64, MetricHamming)
	if _, err := EncodeSparseMeta(bad); err == nil {
		t.Fatal("encoded sparse meta with HAMMING")
	}
	bad = DefaultSparseMeta(0, MetricCosine)
	if _, err := EncodeSparseMeta(bad); err == nil {
		t.Fatal("encoded sparse meta with dim 0")
	}
}

func TestSparseListRoundTrip(t *testing.T) {
	entries := []SparsePosting{
		{PK: []byte("row-00000010"), Value: 1.5},
		{PK: []byte("row-00000002"), Value: -0.25},
		{PK: []byte("row-00000002"), Value: 9}, // dup PK, dropped
		{PK: []byte("row-00000001"), Value: 2},
		{PK: []byte("row-00000100"), Value: 4},
	}
	raw, err := EncodeSparseList(entries)
	if err != nil {
		t.Fatal(err)
	}
	flat := 0
	for _, e := range entries {
		flat += len(e.PK) + 2 + 4
	}
	if len(raw) >= flat {
		t.Fatalf("front-coded list %d not smaller than flat %d", len(raw), flat)
	}
	got, err := DecodeSparseList(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []SparsePosting{
		{PK: []byte("row-00000001"), Value: 2},
		{PK: []byte("row-00000002"), Value: -0.25},
		{PK: []byte("row-00000010"), Value: 1.5},
		{PK: []byte("row-00000100"), Value: 4},
	}
	if len(got) != len(want) {
		t.Fatalf("list len %d want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i].PK, want[i].PK) || got[i].Value != want[i].Value {
			t.Fatalf("entry[%d] = %q/%v want %q/%v", i, got[i].PK, got[i].Value, want[i].PK, want[i].Value)
		}
	}
	raw, err = EncodeSparseList(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeSparseList(raw); err != nil || len(got) != 0 {
		t.Fatalf("empty list: %v %v", got, err)
	}
	if _, err := EncodeSparseList([]SparsePosting{{PK: []byte("x"), Value: 0}}); err == nil {
		t.Fatal("encoded zero posting value")
	}
	if _, err := EncodeSparseList([]SparsePosting{{PK: nil, Value: 1}}); err == nil {
		t.Fatal("encoded empty posting key")
	}
	// Fail closed on an impossible shared prefix.
	if _, err := DecodeSparseList([]byte{'N', 'S', 'S', 'P', 1, 0x01, 0x05, 0x01, 'x', 0, 0, 0x80, 0x3f}); err == nil {
		t.Fatal("decoded impossible shared prefix")
	}
}

func TestSparseSearchRecall(t *testing.T) {
	const (
		dim = 4096
		n   = 400
		nnz = 24
	)
	idx, cands, rng := buildSparse(t, dim, n, nnz, MetricIP)

	const queries = 24
	var ipRecall float64
	for qn := 0; qn < queries; qn++ {
		q := randSparse(rng, dim, 16)
		truth, err := SparseFlat(q, MetricIP, cands, 10)
		if err != nil {
			t.Fatal(err)
		}
		got, err := SearchSparse(idx, q, 10, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		ipRecall += RecallAt(truth, got, 10)
	}
	ipRecall /= queries
	if ipRecall < 0.999 {
		t.Fatalf("inner-product inverted-index search must be exact, recall@10 = %.3f", ipRecall)
	}

	cos, candsC, rngC := buildSparse(t, dim, n, nnz, MetricCosine)
	var cosAll, cosRerank float64
	for qn := 0; qn < queries; qn++ {
		q := randSparse(rngC, dim, 16)
		truth, err := SparseFlat(q, MetricCosine, candsC, 10)
		if err != nil {
			t.Fatal(err)
		}
		all, err := SearchSparse(cos, q, 10, n, 1)
		if err != nil {
			t.Fatal(err)
		}
		approx, err := SearchSparse(cos, q, 10, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		cosAll += RecallAt(truth, all, 10)
		cosRerank += RecallAt(truth, approx, 10)
	}
	cosAll /= queries
	cosRerank /= queries
	t.Logf("sparse recall@10: IP %.3f, COSINE rerank-all %.3f, COSINE rerank=4k %.3f (dim=%d n=%d nnz=%d)",
		ipRecall, cosAll, cosRerank, dim, n, nnz)
	if cosAll < 0.999 {
		t.Fatalf("cosine with full re-rank must be exact, recall@10 = %.3f", cosAll)
	}
	if cosRerank < 0.90 {
		t.Fatalf("cosine re-rank 4k recall@10 too low: %.3f", cosRerank)
	}
}

func TestSparseAddRemove(t *testing.T) {
	m, _, rng := buildSparse(t, 512, 120, 8, MetricIP)
	removed := map[string]bool{}
	for i := 0; i < 120; i += 2 {
		ok, err := RemoveSparse(m, pk32(i))
		if err != nil || !ok {
			t.Fatalf("RemoveSparse(%d): ok=%v err=%v", i, ok, err)
		}
		removed[string(pk32(i))] = true
	}
	if m.Meta.Count != 60 {
		t.Fatalf("Count after removals = %d, want 60", m.Meta.Count)
	}
	if ok, err := RemoveSparse(m, pk32(0)); err != nil || ok {
		t.Fatalf("second RemoveSparse should be a no-op: ok=%v err=%v", ok, err)
	}
	q := randSparse(rng, 512, 8)
	hits, err := SearchSparse(m, q, 20, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if removed[string(h.PK)] {
			t.Fatalf("search returned removed pk %x", h.PK)
		}
	}
}

func TestSparsePersistLoad(t *testing.T) {
	src, cands, rng := buildSparse(t, 1024, 200, 12, MetricIP)

	dst := NewSparseMem(SparseMeta{})
	if err := PersistSparse(dst, src); err != nil {
		t.Fatal(err)
	}
	rawMeta, err := EncodeSparseMeta(dst.Meta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSparseMeta(rawMeta); err != nil {
		t.Fatal(err)
	}
	dims := make([]uint32, 0, len(dst.Lists))
	for d := range dst.Lists {
		raw, err := EncodeSparseList(dst.Lists[d])
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeSparseList(raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(dst.Lists[d]) {
			t.Fatalf("list %d len %d want %d", d, len(got), len(dst.Lists[d]))
		}
		dims = append(dims, d)
	}

	for i := range cands {
		dst.PutVec(cands[i].PK, cands[i].Vec)
	}
	loaded, err := LoadSparseMem(dst, dims)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Meta.Count != src.Meta.Count {
		t.Fatalf("loaded Count %d want %d", loaded.Meta.Count, src.Meta.Count)
	}
	q := randSparse(rng, 1024, 12)
	a, err := SearchSparse(src, q, 10, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := SearchSparse(loaded, q, 10, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if RecallAt(a, b, 10) != 1.0 {
		t.Fatalf("loaded index search differs from source: %v vs %v", a, b)
	}
}

func TestSparseKeyRoundTrip(t *testing.T) {
	if !bytes.Equal(SparseMetaKey(), []byte{kindSparseMeta}) {
		t.Fatal("SparseMetaKey")
	}
	k := SparsePostingKey(0x00abcdef)
	d, err := SplitSparsePostingKey(k)
	if err != nil || d != 0x00abcdef {
		t.Fatalf("SplitSparsePostingKey: %d %v", d, err)
	}
	if _, err := SplitSparsePostingKey([]byte{kindSparsePosting}); err == nil {
		t.Fatal("split short key")
	}
}
