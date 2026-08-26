package vector

import (
	"encoding/binary"
	"math/rand"
	"testing"
)

func BenchmarkDistances(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	a := randUnit(rng, 1536)
	c := randUnit(rng, 1536)
	b.Run("cosine", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = CosineSimilarity(a, c)
		}
	})
	b.Run("l2", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = L2(a, c)
		}
	})
	b.Run("ip", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = InnerProduct(a, c)
		}
	})
}

func BenchmarkHNSWSearch(b *testing.B) {
	const dim = 32
	rng := rand.New(rand.NewSource(7))
	g := NewMem(dim, MetricCosine)
	var cands []Candidate
	for i := 0; i < 2000; i++ {
		pk := make([]byte, 4)
		binary.LittleEndian.PutUint32(pk, uint32(i))
		v := randUnit(rng, dim)
		g.PutVec(pk, v)
		if err := Insert(g, pk, v); err != nil {
			b.Fatal(err)
		}
		cands = append(cands, Candidate{PK: pk, Vec: v})
	}
	q := randUnit(rng, dim)
	truth, err := FlatSearch(q, MetricCosine, cands, 10, 1)
	if err != nil {
		b.Fatal(err)
	}
	approx, err := Search(g, q, 10, 64)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(RecallAt(truth, approx, 10), "recall@10")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Search(g, q, 10, 64); err != nil {
			b.Fatal(err)
		}
	}
}
