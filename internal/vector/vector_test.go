package vector

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand"
	"testing"

	"github.com/bzync/nextsql/internal/float16"
	"github.com/bzync/nextsql/internal/int8vec"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestDistanceFixtures(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	c := []float32{1, 0, 0}
	if g := CosineSimilarity(a, c); math.Abs(g-1) > 1e-6 {
		t.Fatalf("identical cosine %v", g)
	}
	if g := CosineSimilarity(a, b); math.Abs(g) > 1e-6 {
		t.Fatalf("orthogonal cosine %v", g)
	}
	if g := L2(a, c); g != 0 {
		t.Fatalf("identical L2 %v", g)
	}
	if g := L2(a, b); math.Abs(g-math.Sqrt(2)) > 1e-6 {
		t.Fatalf("unit L2 %v", g)
	}
	if g := InnerProduct(a, a); math.Abs(g-1) > 1e-6 {
		t.Fatalf("ip %v", g)
	}
	if d := Distance(MetricCosine, a, c); d > 1e-6 {
		t.Fatalf("cosine dist identical %v", d)
	}
	if Distance(MetricIP, a, a) >= Distance(MetricIP, a, b) {
		t.Fatal("IP should rank self closer than orthogonal")
	}
}

func TestPayloadRoundTrip(t *testing.T) {
	v := []float32{1.5, -2, 0, 3.25}
	raw, err := EncodePayload(v)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodePayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(v) {
		t.Fatalf("%v", got)
	}
	for i := range v {
		if got[i] != v[i] {
			t.Fatalf("%v vs %v", got, v)
		}
	}
}

func TestPayloadF16Quantized(t *testing.T) {
	v := []float32{0.1, 0.2, 0.3, -0.4, 1000}
	raw, err := EncodePayloadElem(v, types.VecF16)
	if err != nil {
		t.Fatal(err)
	}
	// Half the on-disk element width vs the F32 layout.
	f32raw, _ := EncodePayload(v)
	if len(raw) >= len(f32raw) || len(raw) != 8+2*len(v) {
		t.Fatalf("f16 payload size %d, f32 %d", len(raw), len(f32raw))
	}
	got, err := DecodePayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := float16.Quantize(v)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("element %d: got %v want %v", i, got[i], want[i])
		}
	}
	// A truncated F16 payload fails closed.
	if _, err := DecodePayload(raw[:len(raw)-1]); err == nil {
		t.Fatal("expected truncated f16 payload rejection")
	}
	raw[5] = 0x7f
	if _, err := DecodePayload(raw); err == nil {
		t.Fatal("expected unknown element encoding rejection")
	}
}

func TestPayloadI8Quantized(t *testing.T) {
	v := []float32{0.1, 0.2, 0.3, -0.4, 10}
	raw, err := EncodePayloadElem(v, types.VecI8)
	if err != nil {
		t.Fatal(err)
	}
	// One byte per element plus a 4-byte scale: smaller than the F32 layout.
	f32raw, _ := EncodePayload(v)
	if len(raw) >= len(f32raw) || len(raw) != 8+int8vec.Bytes(len(v)) {
		t.Fatalf("i8 payload size %d, f32 %d", len(raw), len(f32raw))
	}
	got, err := DecodePayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := int8vec.Quantize(v)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("element %d: got %v want %v", i, got[i], want[i])
		}
	}
	// A truncated I8 payload fails closed.
	if _, err := DecodePayload(raw[:len(raw)-1]); err == nil {
		t.Fatal("expected truncated i8 payload rejection")
	}
	// An unknown element tag fails closed.
	bad := append([]byte(nil), raw...)
	bad[5] = 0x7f
	if _, err := DecodePayload(bad); err == nil {
		t.Fatal("expected unknown element encoding rejection")
	}
}

func TestPayloadBitPacked(t *testing.T) {
	v := []float32{1, 0, 1, 1, 0, 0, 0, 1, 1, 0} // 10 bits
	raw, err := EncodePayloadElem(v, types.VecBit)
	if err != nil {
		t.Fatal(err)
	}
	// Two bytes of packed bits: far smaller than the F32 layout.
	f32raw, _ := EncodePayload(v)
	if len(raw) != 8+2 || len(raw) >= len(f32raw) {
		t.Fatalf("bit payload size %d, f32 %d", len(raw), len(f32raw))
	}
	got, err := DecodePayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	for i := range v {
		if got[i] != v[i] {
			t.Fatalf("element %d: got %v want %v", i, got[i], v[i])
		}
	}
	// A non-bit element is rejected on write.
	if _, err := EncodePayloadElem([]float32{0, 1, 0.5}, types.VecBit); err == nil {
		t.Fatal("expected non-bit element rejection")
	}
	// A truncated bit payload fails closed.
	if _, err := DecodePayload(raw[:len(raw)-1]); err == nil {
		t.Fatal("expected truncated bit payload rejection")
	}
}

func TestHammingDistance(t *testing.T) {
	a := []float32{1, 0, 1, 0, 1, 1}
	b := []float32{1, 1, 1, 1, 0, 1}
	if d := Distance(MetricHamming, a, b); d != 3 {
		t.Fatalf("Hamming distance = %v, want 3", d)
	}
	if m, err := ParseMetric("hamming"); err != nil || m != MetricHamming {
		t.Fatalf("ParseMetric(hamming) = %v, %v", m, err)
	}
	if MetricHamming.String() != "hamming" {
		t.Fatalf("MetricHamming.String() = %q", MetricHamming.String())
	}
	// A Hamming HNSW header round-trips.
	meta := DefaultMeta(6, MetricHamming)
	enc, err := EncodeMeta(meta)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := DecodeMeta(enc)
	if err != nil || dec.Metric != MetricHamming {
		t.Fatalf("meta round trip: %v %v", dec.Metric, err)
	}
}

func TestPayloadRejectsBad(t *testing.T) {
	if _, err := EncodePayload(nil); err == nil {
		t.Fatal("empty")
	}
	if _, err := EncodePayload([]float32{float32(math.NaN())}); err == nil {
		t.Fatal("nan")
	}
	if _, err := DecodePayload([]byte("xxxx")); err == nil {
		t.Fatal("magic")
	}
	raw, err := EncodePayload([]float32{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	raw[4] = 99
	if _, err := DecodePayload(raw); err == nil {
		t.Fatal("version")
	}
}

func TestMetaNodeRoundTrip(t *testing.T) {
	m := DefaultMeta(8, MetricL2)
	m.Count = 3
	m.MaxLevel = 2
	m.Entry = []byte{1, 2, 3}
	raw, err := EncodeMeta(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeMeta(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Dim != 8 || got.Metric != MetricL2 || got.Count != 3 || string(got.Entry) != "\x01\x02\x03" {
		t.Fatalf("%+v", got)
	}
	n := Node{
		Level: 1,
		Neighbors: [][][]byte{
			{[]byte("a"), []byte("b")},
			{[]byte("c")},
		},
	}
	nb, err := EncodeNode(n)
	if err != nil {
		t.Fatal(err)
	}
	gn, err := DecodeNode(nb)
	if err != nil {
		t.Fatal(err)
	}
	if gn.Level != 1 || len(gn.Neighbors) != 2 || string(gn.Neighbors[1][0]) != "c" {
		t.Fatalf("%+v", gn)
	}
}

func TestCompressedNeighborLists(t *testing.T) {
	// Realistic neighbours: 8-byte big-endian row ids in a dense id space, so
	// every key shares its leading bytes with its neighbours.
	mk := func(id uint64) []byte {
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, id)
		return b
	}
	layer0 := make([][]byte, 0, 32)
	for i := 0; i < 32; i++ {
		layer0 = append(layer0, mk(uint64(1_000_000+i*7)))
	}
	layer1 := [][]byte{mk(1_000_050), mk(1_000_003), mk(1_000_120)}
	n := Node{Level: 1, Neighbors: [][][]byte{layer0, layer1}}

	raw, err := EncodeNode(n)
	if err != nil {
		t.Fatal(err)
	}
	if raw[0] != nodeVersionC {
		t.Fatalf("EncodeNode wrote version %d, want %d", raw[0], nodeVersionC)
	}

	// v1 would spend: 4 header + per layer (2 count + per key 2 len + key bytes).
	v1 := 4
	for _, layer := range n.Neighbors {
		v1 += 2
		for _, pk := range layer {
			v1 += 2 + len(pk)
		}
	}
	if len(raw) >= v1 {
		t.Fatalf("compressed node %d bytes is not smaller than v1 %d", len(raw), v1)
	}
	if len(raw)*2 > v1 {
		t.Fatalf("compressed node %d bytes: expected at least ~2x shrink vs v1 %d", len(raw), v1)
	}

	got, err := DecodeNode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Level != 1 || got.Deleted || len(got.Neighbors) != 2 {
		t.Fatalf("decoded %+v", got)
	}
	for li, layer := range n.Neighbors {
		if len(got.Neighbors[li]) != len(layer) {
			t.Fatalf("layer %d: got %d neighbours, want %d", li, len(got.Neighbors[li]), len(layer))
		}
		want := map[string]bool{}
		for _, pk := range layer {
			want[string(pk)] = true
		}
		for j, pk := range got.Neighbors[li] {
			if !want[string(pk)] {
				t.Fatalf("layer %d neighbour %d %x not in input", li, j, pk)
			}
			if j > 0 && bytes.Compare(got.Neighbors[li][j-1], pk) > 0 {
				t.Fatalf("layer %d neighbours not sorted", li)
			}
		}
	}

	// A v1 node still decodes.
	v1raw := make([]byte, 0, 32)
	v1raw = append(v1raw, nodeVersion, 0, 0, 1) // version, level 0, live, 1 layer
	v1raw = append(v1raw, 0x02, 0x00)           // count = 2 (u16 LE)
	for _, pk := range [][]byte{[]byte("aa"), []byte("ab")} {
		v1raw = append(v1raw, byte(len(pk)), 0x00)
		v1raw = append(v1raw, pk...)
	}
	vn, err := DecodeNode(v1raw)
	if err != nil {
		t.Fatalf("v1 node decode: %v", err)
	}
	if len(vn.Neighbors) != 1 || len(vn.Neighbors[0]) != 2 || string(vn.Neighbors[0][1]) != "ab" {
		t.Fatalf("v1 decoded %+v", vn)
	}

	// Unordered / bad-prefix v2 blobs fail closed.
	bad := []byte{nodeVersionC, 0, 0, 1, 0x01, 0x05, 0x01, 'z'} // shared 5 > prev len 0
	if _, err := DecodeNode(bad); err == nil {
		t.Fatal("decoded node with impossible shared-prefix length")
	}
}

func TestMetaQuantRoundTrip(t *testing.T) {
	m := DefaultMeta(8, MetricCosine)
	m.Count = 2
	m.Entry = []byte("pk")
	m.Quant = types.VecI8
	raw, err := EncodeMeta(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeMeta(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Quant != types.VecI8 || string(got.Entry) != "pk" || got.Count != 2 {
		t.Fatalf("%+v", got)
	}

	// A v1 header (no quant byte) decodes with Quant == 0.
	m.Quant = 0
	rawV1, err := EncodeMeta(m)
	if err != nil {
		t.Fatal(err)
	}
	if rawV1[4] != metaVersion {
		t.Fatalf("expected v1 header, got version %d", rawV1[4])
	}
	gotV1, err := DecodeMeta(rawV1)
	if err != nil {
		t.Fatal(err)
	}
	if gotV1.Quant != 0 {
		t.Fatalf("v1 quant = %d", gotV1.Quant)
	}

	// A bad quantisation tag fails closed.
	bad := append([]byte(nil), raw...)
	bad[len(bad)-1] = 0x7f
	if _, err := DecodeMeta(bad); err == nil {
		t.Fatal("expected bad quantisation tag to be rejected")
	}

	pk, err := SplitQVecKey(QVecKey([]byte("row-1")))
	if err != nil || string(pk) != "row-1" {
		t.Fatalf("QVecKey round trip: %q %v", pk, err)
	}
}

func TestQuantizedHNSWRerank(t *testing.T) {
	const (
		dim = 16
		n   = 300
	)
	rng := rand.New(rand.NewSource(7))
	g := NewMem(dim, MetricCosine)
	g.Meta.Quant = types.VecI8
	var cands []Candidate
	for i := 0; i < n; i++ {
		pk := make([]byte, 4)
		binary.LittleEndian.PutUint32(pk, uint32(i))
		v := randUnit(rng, dim)
		if err := Insert(g, pk, v); err != nil {
			t.Fatal(err)
		}
		cands = append(cands, Candidate{PK: pk, Vec: v})
	}
	// The traversal store holds quantised vectors; the full store is exact.
	if len(g.Full) != n || len(g.Vecs) != n {
		t.Fatalf("store sizes: full=%d vecs=%d", len(g.Full), len(g.Vecs))
	}
	differs := false
	for k, full := range g.Full {
		if !equalVec(full, g.Vecs[k]) {
			differs = true
			break
		}
	}
	if !differs {
		t.Fatal("no traversal vector was quantised away from its full value")
	}
	var r10 float64
	const queries = 20
	for qn := 0; qn < queries; qn++ {
		q := randUnit(rng, dim)
		truth, err := FlatSearch(q, MetricCosine, cands, 10, 1)
		if err != nil {
			t.Fatal(err)
		}
		approx, err := Search(g, q, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		// Re-rank uses exact distances, so the reported distance of the top hit
		// matches an exact computation.
		if len(approx) > 0 {
			want := Distance(MetricCosine, q, g.Full[string(approx[0].PK)])
			if math.Abs(want-approx[0].Dist) > 1e-6 {
				t.Fatalf("top hit distance not re-ranked: got %v want %v", approx[0].Dist, want)
			}
		}
		r10 += RecallAt(truth, approx, 10)
	}
	r10 /= queries
	t.Logf("quantised HNSW recall@10=%.3f", r10)
	if r10 < 0.80 {
		t.Fatalf("quantised recall@10 too low: %.3f", r10)
	}
}

func TestFlatSearchExact(t *testing.T) {
	query := []float32{1, 0}
	cands := []Candidate{
		{PK: []byte("far"), Vec: []float32{0, 1}},
		{PK: []byte("near"), Vec: []float32{0.9, 0.1}},
		{PK: []byte("mid"), Vec: []float32{0.5, 0.5}},
	}
	hits, err := FlatSearch(query, MetricCosine, cands, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || string(hits[0].PK) != "near" || string(hits[1].PK) != "mid" {
		t.Fatalf("%+v", hits)
	}
	par, err := FlatSearch(query, MetricCosine, cands, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if string(par[0].PK) != string(hits[0].PK) || par[0].Dist != hits[0].Dist {
		t.Fatalf("parallel %+v vs %+v", par, hits)
	}
}

func TestHNSWMatchesFlatSmall(t *testing.T) {
	const dim = 8
	rng := rand.New(rand.NewSource(1))
	g := NewMem(dim, MetricCosine)
	var cands []Candidate
	for i := 0; i < 40; i++ {
		pk := []byte{byte(i)}
		v := randUnit(rng, dim)
		g.PutVec(pk, v)
		if err := Insert(g, pk, v); err != nil {
			t.Fatal(err)
		}
		cands = append(cands, Candidate{PK: pk, Vec: v})
	}
	q := randUnit(rng, dim)
	want, err := FlatSearch(q, MetricCosine, cands, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Search(g, q, 5, 40)
	if err != nil {
		t.Fatal(err)
	}
	if RecallAt(want, got, 5) < 1 {
		t.Fatalf("small graph must be exact: want %+v got %+v", pks(want), pks(got))
	}
}

func TestHNSWRecall(t *testing.T) {
	const (
		dim = 16
		n   = 400
	)
	rng := rand.New(rand.NewSource(42))
	g := NewMem(dim, MetricCosine)
	var cands []Candidate
	for i := 0; i < n; i++ {
		pk := make([]byte, 4)
		binary.LittleEndian.PutUint32(pk, uint32(i))
		v := randUnit(rng, dim)
		g.PutVec(pk, v)
		if err := Insert(g, pk, v); err != nil {
			t.Fatal(err)
		}
		cands = append(cands, Candidate{PK: pk, Vec: v})
	}
	var r10, r100 float64
	const queries = 20
	for qn := 0; qn < queries; qn++ {
		q := randUnit(rng, dim)
		truth, err := FlatSearch(q, MetricCosine, cands, 100, 1)
		if err != nil {
			t.Fatal(err)
		}
		approx, err := Search(g, q, 100, 100)
		if err != nil {
			t.Fatal(err)
		}
		r10 += RecallAt(truth, approx, 10)
		r100 += RecallAt(truth, approx, 100)
	}
	r10 /= queries
	r100 /= queries
	t.Logf("HNSW recall@10=%.3f recall@100=%.3f n=%d dim=%d", r10, r100, n, dim)
	if r10 < 0.85 {
		t.Fatalf("recall@10 too low: %.3f", r10)
	}
	if r100 < 0.80 {
		t.Fatalf("recall@100 too low: %.3f", r100)
	}
}

func TestPersistLoadMem(t *testing.T) {
	src := NewMem(4, MetricL2)
	for i, v := range [][]float32{{1, 0, 0, 0}, {0, 1, 0, 0}, {0, 0, 1, 0}} {
		pk := []byte{byte(i)}
		src.PutVec(pk, v)
		if err := Insert(src, pk, v); err != nil {
			t.Fatal(err)
		}
	}
	dst := NewMem(4, MetricL2)
	for k, v := range src.Vecs {
		dst.PutVec([]byte(k), v)
	}
	if err := Persist(dst, src); err != nil {
		t.Fatal(err)
	}
	got, err := LoadMem(dst)
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta.Count != 3 {
		t.Fatalf("count %d", got.Meta.Count)
	}
	q := []float32{1, 0, 0, 0}
	a, err := Search(src, q, 2, 8)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Search(got, q, 2, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) == 0 || len(b) == 0 || string(a[0].PK) != string(b[0].PK) {
		t.Fatalf("search %+v vs %+v", a, b)
	}
}

func TestHNSWDelete(t *testing.T) {
	g := NewMem(2, MetricL2)
	for i, v := range [][]float32{{0, 0}, {1, 0}, {0, 1}} {
		pk := []byte{byte(i)}
		g.PutVec(pk, v)
		if err := Insert(g, pk, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := Delete(g, []byte{0}); err != nil {
		t.Fatal(err)
	}
	hits, err := Search(g, []float32{0, 0}, 2, 8)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if string(h.PK) == "\x00" {
			t.Fatal("deleted node returned")
		}
	}
	st, err := InspectTombstones(g)
	if err != nil {
		t.Fatal(err)
	}
	if st.Total != 3 || st.Live != 2 || st.Deleted != 1 {
		t.Fatalf("tombstone stats %+v", st)
	}
	if ShouldRebuildTombstones(st) {
		t.Fatal("tiny graph should not trigger rebuild")
	}
	if !ShouldRebuildTombstones(TombstoneStats{Total: 5000, Live: 3900, Deleted: 1100}) {
		t.Fatal("20% tombstone policy did not trigger")
	}
}

func TestInspectTombstonesRejectsMetadataMismatch(t *testing.T) {
	g := NewMem(2, MetricL2)
	g.Nodes["orphan"] = Node{}
	if _, err := InspectTombstones(g); !nerr.HasCode(err, nerr.Corruption) {
		t.Fatalf("metadata mismatch: %v", err)
	}
}

func TestDimensionMismatch(t *testing.T) {
	if err := Check([]float32{1, 2}, 3); err == nil {
		t.Fatal("expected dim mismatch")
	}
	if !nerr.HasCode(errOf(Check([]float32{1, 2}, 3)), nerr.InvalidArgument) {
		t.Fatal("code")
	}
}

func TestRowVectorRefRoundTrip(t *testing.T) {
	vt, err := types.VectorF32(3)
	if err != nil {
		t.Fatal(err)
	}
	inline := types.VectorValue([]float32{1, 2, 3}, vt)
	raw, err := types.EncodeRow([]types.Value{inline})
	if err != nil {
		t.Fatal(err)
	}
	got, err := types.DecodeRow(raw, []types.Type{vt})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].VecRef || len(got[0].Vec) != 3 || got[0].Vec[1] != 2 {
		t.Fatalf("%+v", got[0])
	}
	ref := types.VectorRef(vt)
	raw, err = types.EncodeRow([]types.Value{ref})
	if err != nil {
		t.Fatal(err)
	}
	got, err = types.DecodeRow(raw, []types.Type{vt})
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].VecRef || got[0].Vec != nil {
		t.Fatalf("ref %+v", got[0])
	}
}

func TestRichVectorAlgebra(t *testing.T) {
	a := []float32{3, 4}
	norm, err := Norm(a)
	if err != nil || norm != 5 {
		t.Fatalf("norm=%f err=%v", norm, err)
	}
	unit, err := Normalize(a)
	if err != nil || math.Abs(float64(unit[0])-.6) > 1e-6 || math.Abs(float64(unit[1])-.8) > 1e-6 {
		t.Fatalf("normalize=%v err=%v", unit, err)
	}
	if _, err := Normalize([]float32{0, 0}); err == nil {
		t.Fatal("expected zero-normalization error")
	}
	sum, err := Add(a, []float32{1, 2})
	if err != nil || sum[0] != 4 || sum[1] != 6 {
		t.Fatalf("add=%v err=%v", sum, err)
	}
	diff, err := Sub(a, []float32{1, 2})
	if err != nil || diff[0] != 2 || diff[1] != 2 {
		t.Fatalf("sub=%v err=%v", diff, err)
	}
	scaled, err := Scale(a, .5)
	if err != nil || scaled[0] != 1.5 || scaled[1] != 2 {
		t.Fatalf("scale=%v err=%v", scaled, err)
	}
	l1, err := L1(a, []float32{1, 1})
	if err != nil || l1 != 5 {
		t.Fatalf("l1=%f err=%v", l1, err)
	}
	if _, err := Add(a, []float32{1}); err == nil {
		t.Fatal("expected dimension mismatch")
	}
	if _, err := Scale(a, math.Inf(1)); err == nil {
		t.Fatal("expected non-finite scale error")
	}
}

func errOf(err error) error { return err }

func equalVec(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func randUnit(rng *rand.Rand, dim int) []float32 {
	v := make([]float32, dim)
	var n float64
	for i := range v {
		v[i] = float32(rng.NormFloat64())
		n += float64(v[i]) * float64(v[i])
	}
	s := float32(math.Sqrt(n))
	if s == 0 {
		v[0] = 1
		return v
	}
	for i := range v {
		v[i] /= s
	}
	return v
}

func pks(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = string(h.PK)
	}
	return out
}
