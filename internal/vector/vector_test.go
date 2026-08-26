package vector

import (
	"encoding/binary"
	"math"
	"math/rand"
	"testing"

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

func errOf(err error) error { return err }

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
