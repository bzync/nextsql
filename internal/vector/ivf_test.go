package vector

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand"
	"testing"
)

func pk32(i int) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, uint32(i))
	return b
}

// buildIVF trains an IVF over n random unit vectors and adds them all.
func buildIVF(t *testing.T, dim, n, nlist, nprobe int, metric Metric) (*IVFMem, []Candidate, *rand.Rand) {
	t.Helper()
	rng := rand.New(rand.NewSource(9))
	samples := make([][]float32, n)
	cands := make([]Candidate, n)
	for i := 0; i < n; i++ {
		v := randUnit(rng, dim)
		samples[i] = v
		cands[i] = Candidate{PK: pk32(i), Vec: v}
	}
	meta := DefaultIVFMeta(uint16(dim), metric, uint32(nlist))
	meta.NProbe = uint32(nprobe)
	m, err := TrainIVF(meta, samples)
	if err != nil {
		t.Fatalf("TrainIVF: %v", err)
	}
	for i := 0; i < n; i++ {
		m.PutVec(pk32(i), samples[i])
		if err := AddIVF(m, pk32(i), samples[i]); err != nil {
			t.Fatalf("AddIVF: %v", err)
		}
	}
	if m.Meta.Count != uint64(n) {
		t.Fatalf("Count = %d, want %d", m.Meta.Count, n)
	}
	return m, cands, rng
}

func TestIVFMetaRoundTrip(t *testing.T) {
	m := DefaultIVFMeta(128, MetricCosine, 64)
	m.Count = 4096
	m.Trained = true
	raw, err := EncodeIVFMeta(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != ivfMetaLen {
		t.Fatalf("meta len = %d, want %d", len(raw), ivfMetaLen)
	}
	got, err := DecodeIVFMeta(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != m {
		t.Fatalf("round trip: got %+v want %+v", got, m)
	}
	// Fail closed.
	for _, bad := range [][]byte{
		nil,
		[]byte("NSIV"),
		append([]byte("XXXX"), raw[4:]...),
		raw[:len(raw)-1],
	} {
		if _, err := DecodeIVFMeta(bad); err == nil {
			t.Fatalf("decoded bad meta %q", bad)
		}
	}
	// NProbe > NList rejected.
	bad := m
	bad.NProbe = bad.NList + 1
	if _, err := EncodeIVFMeta(bad); err == nil {
		t.Fatal("encoded NProbe > NList")
	}
	// Hamming metric rejected (IVF is real-valued).
	bad = m
	bad.Metric = MetricHamming
	if _, err := EncodeIVFMeta(bad); err == nil {
		t.Fatal("encoded IVF with HAMMING metric")
	}
}

func TestIVFCentroidRoundTrip(t *testing.T) {
	cent := [][]float32{{1, 2, 3, 4}, {-1, 0.5, 2, -3}, {0, 0, 0, 1}}
	raw, err := EncodeCentroids(cent, 4)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeCentroids(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(cent) {
		t.Fatalf("centroid count %d want %d", len(got), len(cent))
	}
	for i := range cent {
		for j := range cent[i] {
			if got[i][j] != cent[i][j] {
				t.Fatalf("centroid[%d][%d] = %v want %v", i, j, got[i][j], cent[i][j])
			}
		}
	}
	if _, err := DecodeCentroids(raw[:len(raw)-1]); err == nil {
		t.Fatal("decoded truncated centroids")
	}
	if _, err := EncodeCentroids([][]float32{{1, 2}, {3}}, 2); err == nil {
		t.Fatal("encoded ragged centroids")
	}
}

func TestIVFListRoundTrip(t *testing.T) {
	pks := [][]byte{
		[]byte("row-00000010"),
		[]byte("row-00000002"),
		[]byte("row-00000002"), // dup
		[]byte("row-00000001"),
		[]byte("row-00000100"),
	}
	raw, err := EncodeIVFList(pks)
	if err != nil {
		t.Fatal(err)
	}
	// Front coding + dedup should be well under the flat size.
	flat := 0
	for _, p := range pks {
		flat += len(p) + 2
	}
	if len(raw) >= flat {
		t.Fatalf("front-coded list %d not smaller than flat %d", len(raw), flat)
	}
	got, err := DecodeIVFList(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]byte{
		[]byte("row-00000001"),
		[]byte("row-00000002"),
		[]byte("row-00000010"),
		[]byte("row-00000100"),
	}
	if len(got) != len(want) {
		t.Fatalf("list len %d want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("list[%d] = %q want %q", i, got[i], want[i])
		}
	}
	// Empty list round trips.
	raw, err = EncodeIVFList(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeIVFList(raw); err != nil || len(got) != 0 {
		t.Fatalf("empty list: %v %v", got, err)
	}
	// Fail closed on an impossible shared prefix.
	if _, err := DecodeIVFList([]byte{'N', 'S', 'I', 'L', 1, 0x01, 0x05, 0x01, 'x'}); err == nil {
		t.Fatal("decoded impossible shared prefix")
	}
}

func TestIVFSearchRecall(t *testing.T) {
	const (
		dim   = 24
		n     = 800
		nlist = 32
	)
	m, cands, rng := buildIVF(t, dim, n, nlist, nlist, MetricCosine) // probe every list -> exact
	var exact float64
	const queries = 24
	for qn := 0; qn < queries; qn++ {
		q := randUnit(rng, dim)
		truth, err := FlatSearch(q, MetricCosine, cands, 10, 1)
		if err != nil {
			t.Fatal(err)
		}
		approx, err := SearchIVF(m, q, 10, nlist, 1)
		if err != nil {
			t.Fatal(err)
		}
		exact += RecallAt(truth, approx, 10)
	}
	exact /= queries
	if exact < 0.999 {
		t.Fatalf("probing every list must be exact, recall@10 = %.3f", exact)
	}

	// A modest probe count keeps most of the recall.
	var partial float64
	for qn := 0; qn < queries; qn++ {
		q := randUnit(rng, dim)
		truth, err := FlatSearch(q, MetricCosine, cands, 10, 1)
		if err != nil {
			t.Fatal(err)
		}
		approx, err := SearchIVF(m, q, 10, 8, 4)
		if err != nil {
			t.Fatal(err)
		}
		partial += RecallAt(truth, approx, 10)
	}
	partial /= queries
	t.Logf("IVF recall@10: nprobe=all %.3f, nprobe=8 %.3f (nlist=%d n=%d)", exact, partial, nlist, n)
	if partial < 0.80 {
		t.Fatalf("recall@10 with nprobe=8 too low: %.3f", partial)
	}
}

func TestIVFAddRemove(t *testing.T) {
	m, _, _ := buildIVF(t, 12, 120, 8, 8, MetricL2)
	// Remove half; Count tracks; search never returns a removed pk.
	removed := map[string]bool{}
	for i := 0; i < 120; i += 2 {
		ok, err := RemoveIVF(m, pk32(i))
		if err != nil || !ok {
			t.Fatalf("RemoveIVF(%d): ok=%v err=%v", i, ok, err)
		}
		removed[string(pk32(i))] = true
	}
	if m.Meta.Count != 60 {
		t.Fatalf("Count after removals = %d, want 60", m.Meta.Count)
	}
	if ok, err := RemoveIVF(m, pk32(0)); err != nil || ok {
		t.Fatalf("second RemoveIVF should be no-op: ok=%v err=%v", ok, err)
	}
	q := randUnit(rand.New(rand.NewSource(3)), 12)
	hits, err := SearchIVF(m, q, 20, 8, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if removed[string(h.PK)] {
			t.Fatalf("search returned removed pk %x", h.PK)
		}
	}
}

func TestIVFPersistLoad(t *testing.T) {
	src, cands, rng := buildIVF(t, 16, 300, 16, 16, MetricCosine)

	dst := &IVFMem{Lists: make([][][]byte, src.Meta.NList)}
	if err := PersistIVF(dst, src); err != nil {
		t.Fatal(err)
	}
	// Round trip meta/centroids/lists through the on-disk encodings too.
	rawMeta, err := EncodeIVFMeta(dst.Meta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeIVFMeta(rawMeta); err != nil {
		t.Fatal(err)
	}
	rawCent, err := EncodeCentroids(dst.Centroids, 16)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCentroids(rawCent); err != nil {
		t.Fatal(err)
	}
	for l := range dst.Lists {
		raw, err := EncodeIVFList(dst.Lists[l])
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeIVFList(raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(dst.Lists[l]) {
			t.Fatalf("list %d len %d want %d", l, len(got), len(dst.Lists[l]))
		}
	}

	// Payloads must be re-supplied to the loaded copy (mirrors the executor,
	// where LoadVec reads the column payload store).
	for i := range cands {
		dst.PutVec(cands[i].PK, cands[i].Vec)
	}
	loaded, err := LoadIVFMem(dst)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Meta.Count != src.Meta.Count {
		t.Fatalf("loaded Count %d want %d", loaded.Meta.Count, src.Meta.Count)
	}
	q := randUnit(rng, 16)
	a, err := SearchIVF(src, q, 10, 16, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := SearchIVF(loaded, q, 10, 16, 1)
	if err != nil {
		t.Fatal(err)
	}
	if RecallAt(a, b, 10) != 1.0 {
		t.Fatalf("loaded index search differs from source: %v vs %v", a, b)
	}
}

func TestTrainIVFDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	samples := make([][]float32, 200)
	for i := range samples {
		samples[i] = randUnit(rng, 20)
	}
	meta := DefaultIVFMeta(20, MetricCosine, 16)
	a, err := TrainIVF(meta, samples)
	if err != nil {
		t.Fatal(err)
	}
	b, err := TrainIVF(meta, samples)
	if err != nil {
		t.Fatal(err)
	}
	for i := range a.Centroids {
		if !bytes.Equal(f32Bytes(a.Centroids[i]), f32Bytes(b.Centroids[i])) {
			t.Fatalf("centroid %d not deterministic", i)
		}
	}
}

func f32Bytes(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return b
}
