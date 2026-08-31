package vector

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

// adcOnlyStore hides the payloads so SearchIVFPQ must fall back to ADC ranking.
type adcOnlyStore struct{ *IVFPQMem }

func (adcOnlyStore) LoadVec(pk []byte) ([]float32, error) {
	return nil, nerr.New(nerr.NotFound, "test.adcOnlyStore", "no payloads")
}

// buildIVFPQ trains an IVF-PQ over n random unit vectors and adds them all.
func buildIVFPQ(t *testing.T, dim, n, nlist, nprobe, m int, metric Metric) (*IVFPQMem, []Candidate, *rand.Rand) {
	t.Helper()
	rng := rand.New(rand.NewSource(7))
	samples := make([][]float32, n)
	cands := make([]Candidate, n)
	for i := 0; i < n; i++ {
		v := randUnit(rng, dim)
		samples[i] = v
		cands[i] = Candidate{PK: pk32(i), Vec: v}
	}
	meta := DefaultIVFPQMeta(uint16(dim), metric, uint32(nlist), uint16(m))
	meta.NProbe = uint32(nprobe)
	idx, err := TrainIVFPQ(meta, samples)
	if err != nil {
		t.Fatalf("TrainIVFPQ: %v", err)
	}
	for i := 0; i < n; i++ {
		idx.PutVec(pk32(i), samples[i])
		if err := AddIVFPQ(idx, pk32(i), samples[i]); err != nil {
			t.Fatalf("AddIVFPQ: %v", err)
		}
	}
	if idx.Meta.Count != uint64(n) {
		t.Fatalf("Count = %d, want %d", idx.Meta.Count, n)
	}
	return idx, cands, rng
}

func TestIVFPQMetaRoundTrip(t *testing.T) {
	m := DefaultIVFPQMeta(128, MetricL2, 64, 16)
	m.Count = 4096
	m.Trained = true
	raw, err := EncodeIVFPQMeta(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != ivfpqMetaLen {
		t.Fatalf("meta len = %d, want %d", len(raw), ivfpqMetaLen)
	}
	got, err := DecodeIVFPQMeta(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != m {
		t.Fatalf("round trip: got %+v want %+v", got, m)
	}
	for _, bad := range [][]byte{
		nil,
		[]byte("NSPQ"),
		append([]byte("XXXX"), raw[4:]...),
		raw[:len(raw)-1],
	} {
		if _, err := DecodeIVFPQMeta(bad); err == nil {
			t.Fatalf("decoded bad meta %q", bad)
		}
	}
	// M must divide Dim.
	bad := DefaultIVFPQMeta(100, MetricL2, 8, 7)
	if _, err := EncodeIVFPQMeta(bad); err == nil {
		t.Fatal("encoded M that does not divide Dim")
	}
	// INNER_PRODUCT rejected.
	bad = DefaultIVFPQMeta(64, MetricIP, 8, 8)
	if _, err := EncodeIVFPQMeta(bad); err == nil {
		t.Fatal("encoded IVF-PQ with INNER_PRODUCT metric")
	}
	// NProbe > NList rejected.
	bad = DefaultIVFPQMeta(64, MetricL2, 8, 8)
	bad.NProbe = 9
	if _, err := EncodeIVFPQMeta(bad); err == nil {
		t.Fatal("encoded NProbe > NList")
	}
}

func TestPQCodebookRoundTrip(t *testing.T) {
	cb := &PQCodebook{
		M: 2, SubDim: 3, Ksub: 2,
		Sub: [][][]float32{
			{{1, 2, 3}, {-1, -2, -3}},
			{{0, 0, 1}, {4, 5, 6}},
		},
	}
	raw, err := EncodePQCodebook(cb)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodePQCodebook(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.M != cb.M || got.SubDim != cb.SubDim || got.Ksub != cb.Ksub {
		t.Fatalf("shape: got %+v", got)
	}
	for m := range cb.Sub {
		for j := range cb.Sub[m] {
			if !bytes.Equal(f32Bytes(got.Sub[m][j]), f32Bytes(cb.Sub[m][j])) {
				t.Fatalf("codebook[%d][%d] mismatch", m, j)
			}
		}
	}
	if _, err := DecodePQCodebook(raw[:len(raw)-1]); err == nil {
		t.Fatal("decoded truncated codebook")
	}
	// Ragged sub-centroid rejected.
	bad := &PQCodebook{M: 1, SubDim: 3, Ksub: 2, Sub: [][][]float32{{{1, 2, 3}, {1, 2}}}}
	if _, err := EncodePQCodebook(bad); err == nil {
		t.Fatal("encoded ragged codebook")
	}
}

func TestPQListRoundTrip(t *testing.T) {
	entries := []PQEntry{
		{PK: []byte("row-00000010"), Code: []byte{1, 2, 3, 4}},
		{PK: []byte("row-00000002"), Code: []byte{5, 6, 7, 8}},
		{PK: []byte("row-00000002"), Code: []byte{9, 9, 9, 9}}, // dup PK, dropped
		{PK: []byte("row-00000001"), Code: []byte{0, 0, 0, 0}},
		{PK: []byte("row-00000100"), Code: []byte{255, 128, 64, 32}},
	}
	raw, err := EncodePQList(entries, 4)
	if err != nil {
		t.Fatal(err)
	}
	flat := 0
	for _, e := range entries {
		flat += len(e.PK) + 2 + len(e.Code)
	}
	if len(raw) >= flat {
		t.Fatalf("front-coded list %d not smaller than flat %d", len(raw), flat)
	}
	got, err := DecodePQList(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []PQEntry{
		{PK: []byte("row-00000001"), Code: []byte{0, 0, 0, 0}},
		{PK: []byte("row-00000002"), Code: []byte{5, 6, 7, 8}},
		{PK: []byte("row-00000010"), Code: []byte{1, 2, 3, 4}},
		{PK: []byte("row-00000100"), Code: []byte{255, 128, 64, 32}},
	}
	if len(got) != len(want) {
		t.Fatalf("list len %d want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i].PK, want[i].PK) || !bytes.Equal(got[i].Code, want[i].Code) {
			t.Fatalf("entry[%d] = %q/%v want %q/%v", i, got[i].PK, got[i].Code, want[i].PK, want[i].Code)
		}
	}
	// Empty list round trips.
	raw, err = EncodePQList(nil, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodePQList(raw); err != nil || len(got) != 0 {
		t.Fatalf("empty list: %v %v", got, err)
	}
	// Wrong code length rejected on encode.
	if _, err := EncodePQList([]PQEntry{{PK: []byte("x"), Code: []byte{1, 2}}}, 4); err == nil {
		t.Fatal("encoded entry with wrong code length")
	}
	// Fail closed on an impossible shared prefix (M=4, count=1, shared=5).
	if _, err := DecodePQList([]byte{'N', 'S', 'P', 'L', 1, 0x04, 0x01, 0x05, 0x01, 'x', 0, 0, 0, 0}); err == nil {
		t.Fatal("decoded impossible shared prefix")
	}
}

func TestIVFPQSearchRecall(t *testing.T) {
	const (
		dim   = 32
		n     = 700
		nlist = 16
		m     = 8
	)
	idx, cands, rng := buildIVFPQ(t, dim, n, nlist, nlist, m, MetricCosine)

	const queries = 24
	var rerankRecall, adcRecall float64
	adc := adcOnlyStore{idx}
	for qn := 0; qn < queries; qn++ {
		q := randUnit(rng, dim)
		truth, err := FlatSearch(q, MetricCosine, cands, 10, 1)
		if err != nil {
			t.Fatal(err)
		}
		withRerank, err := SearchIVFPQ(idx, q, 10, nlist, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		rerankRecall += RecallAt(truth, withRerank, 10)

		adcOnly, err := SearchIVFPQ(adc, q, 10, nlist, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		adcRecall += RecallAt(truth, adcOnly, 10)
	}
	rerankRecall /= queries
	adcRecall /= queries
	t.Logf("IVF-PQ recall@10: probe-all+rerank %.3f, ADC-only %.3f (dim=%d n=%d nlist=%d M=%d)",
		rerankRecall, adcRecall, dim, n, nlist, m)
	if rerankRecall < 0.95 {
		t.Fatalf("probe-all + exact re-rank recall@10 too low: %.3f", rerankRecall)
	}
	if adcRecall < 0.30 {
		t.Fatalf("ADC-only recall@10 unexpectedly low: %.3f", adcRecall)
	}
}

func TestIVFPQAddRemove(t *testing.T) {
	idx, _, _ := buildIVFPQ(t, 16, 160, 8, 8, 4, MetricL2)
	removed := map[string]bool{}
	for i := 0; i < 160; i += 2 {
		ok, err := RemoveIVFPQ(idx, pk32(i))
		if err != nil || !ok {
			t.Fatalf("RemoveIVFPQ(%d): ok=%v err=%v", i, ok, err)
		}
		removed[string(pk32(i))] = true
	}
	if idx.Meta.Count != 80 {
		t.Fatalf("Count after removals = %d, want 80", idx.Meta.Count)
	}
	if ok, err := RemoveIVFPQ(idx, pk32(0)); err != nil || ok {
		t.Fatalf("second RemoveIVFPQ should be a no-op: ok=%v err=%v", ok, err)
	}
	q := randUnit(rand.New(rand.NewSource(5)), 16)
	hits, err := SearchIVFPQ(idx, q, 20, 8, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if removed[string(h.PK)] {
			t.Fatalf("search returned removed pk %x", h.PK)
		}
	}
}

func TestIVFPQPersistLoad(t *testing.T) {
	src, cands, rng := buildIVFPQ(t, 24, 360, 16, 16, 8, MetricCosine)

	dst := &IVFPQMem{Lists: make([][]PQEntry, src.Meta.NList)}
	if err := PersistIVFPQ(dst, src); err != nil {
		t.Fatal(err)
	}
	// Round trip the on-disk encodings.
	rawMeta, err := EncodeIVFPQMeta(dst.Meta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeIVFPQMeta(rawMeta); err != nil {
		t.Fatal(err)
	}
	rawCb, err := EncodePQCodebook(dst.Codebook)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePQCodebook(rawCb); err != nil {
		t.Fatal(err)
	}
	for l := range dst.Lists {
		raw, err := EncodePQList(dst.Lists[l], dst.Codebook.M)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodePQList(raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(dst.Lists[l]) {
			t.Fatalf("list %d len %d want %d", l, len(got), len(dst.Lists[l]))
		}
	}

	for i := range cands {
		dst.PutVec(cands[i].PK, cands[i].Vec)
	}
	loaded, err := LoadIVFPQMem(dst)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Meta.Count != src.Meta.Count {
		t.Fatalf("loaded Count %d want %d", loaded.Meta.Count, src.Meta.Count)
	}
	q := randUnit(rng, 24)
	a, err := SearchIVFPQ(src, q, 10, 16, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := SearchIVFPQ(loaded, q, 10, 16, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if RecallAt(a, b, 10) != 1.0 {
		t.Fatalf("loaded index search differs from source: %v vs %v", a, b)
	}
}

func TestTrainIVFPQDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	samples := make([][]float32, 240)
	for i := range samples {
		samples[i] = randUnit(rng, 24)
	}
	meta := DefaultIVFPQMeta(24, MetricCosine, 16, 6)
	a, err := TrainIVFPQ(meta, samples)
	if err != nil {
		t.Fatal(err)
	}
	b, err := TrainIVFPQ(meta, samples)
	if err != nil {
		t.Fatal(err)
	}
	for i := range a.Centroids {
		if !bytes.Equal(f32Bytes(a.Centroids[i]), f32Bytes(b.Centroids[i])) {
			t.Fatalf("centroid %d not deterministic", i)
		}
	}
	for m := range a.Codebook.Sub {
		for j := range a.Codebook.Sub[m] {
			if !bytes.Equal(f32Bytes(a.Codebook.Sub[m][j]), f32Bytes(b.Codebook.Sub[m][j])) {
				t.Fatalf("codebook[%d][%d] not deterministic", m, j)
			}
		}
	}
}
