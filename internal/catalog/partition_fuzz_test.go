package catalog

import (
	"testing"

	"github.com/bzync/nextsql/internal/sql/types"
)

func FuzzDecodePartitionedTable(f *testing.F) {
	tab := partitionTestTable()
	tab.Partitioning = &Partitioning{Kind: PartitionList, Columns: []int{1}, Partitions: []Partition{
		{ID: 1, Name: "p", HeapMeta: 11, Values: [][]types.Value{{types.StringValue("seed")}}},
	}}
	if raw, err := EncodeTable(tab); err == nil {
		f.Add(raw)
	}
	// Multi-column RANGE and LIST descriptors carry wider bound/value tuples.
	mcr := partitionTestTable()
	mcr.Partitioning = &Partitioning{Kind: PartitionRange, NextID: 3, Columns: []int{0, 1}, Partitions: []Partition{
		{ID: 1, Name: "lo", HeapMeta: 11, Values: [][]types.Value{nil, {types.StringValue("m"), types.StringValue("z")}}},
		{ID: 2, Name: "hi", HeapMeta: 12, LowerInclusive: true, Values: [][]types.Value{{types.StringValue("m"), types.StringValue("z")}, nil}},
	}}
	if raw, err := EncodeTable(mcr); err == nil {
		f.Add(raw)
	}
	mcl := partitionTestTable()
	mcl.Partitioning = &Partitioning{Kind: PartitionList, NextID: 2, Columns: []int{0, 1}, Partitions: []Partition{
		{ID: 1, Name: "p", HeapMeta: 11, Values: [][]types.Value{
			{types.StringValue("us"), types.StringValue("gold")},
			{types.StringValue("eu"), types.StringValue("gold")},
		}},
	}}
	if raw, err := EncodeTable(mcl); err == nil {
		f.Add(raw)
	}
	// v7: a vector column with an IVF index carries the method + list/probe
	// counts in the trailing per-index block.
	ivf := partitionTestTable()
	if vt, err := types.VectorF32(4); err == nil {
		ivf.Columns = append(ivf.Columns, Column{Name: "emb", Type: vt})
		ivf.Indexes = []Index{{
			Name: "ix_emb", Vector: true, Columns: []int{2}, Meta: 40,
			VecMethod: VecMethodIVF, IVFLists: 32, IVFProbes: 4,
		}}
		if raw, err := EncodeTable(ivf); err == nil {
			f.Add(raw)
		}
	}
	// v8: an IVF-PQ index additionally carries the product-quantisation subspace
	// count in the trailing per-index block.
	ivfpq := partitionTestTable()
	if vt, err := types.VectorF32(8); err == nil {
		ivfpq.Columns = append(ivfpq.Columns, Column{Name: "emb", Type: vt})
		ivfpq.Indexes = []Index{{
			Name: "ix_emb", Vector: true, Columns: []int{2}, Meta: 40,
			VecMethod: VecMethodIVFPQ, IVFLists: 32, IVFProbes: 4, IVFSubspaces: 4,
		}}
		if raw, err := EncodeTable(ivfpq); err == nil {
			f.Add(raw)
		}
	}
	// v9: a FULLTEXT index carries the analyzer id + revision.
	ft := partitionTestTable()
	ft.Columns = append(ft.Columns, Column{Name: "body", Type: types.Text()})
	ft.Indexes = []Index{{
		Name: "ix_body", Fulltext: true, Columns: []int{2}, Meta: 40,
		FTAnalyzer: FTAnalyzerEnglish, FTVersion: FTAnalyzerEnglishV1,
	}}
	if raw, err := EncodeTable(ft); err == nil {
		f.Add(raw)
	}
	ft.Indexes[0].FTVersion = FTAnalyzerEnglishV2
	if raw, err := EncodeTable(ft); err == nil {
		f.Add(raw)
	}
	ft.Indexes[0].FTVersion = FTAnalyzerEnglishV3
	if raw, err := EncodeTable(ft); err == nil {
		f.Add(raw)
	}
	ft.Indexes[0].FTAnalyzer = FTAnalyzerFrench
	ft.Indexes[0].FTVersion = FTAnalyzerFrenchV1
	if raw, err := EncodeTable(ft); err == nil {
		f.Add(raw)
	}
	ft.Indexes[0].FTAnalyzer = FTAnalyzerGerman
	ft.Indexes[0].FTVersion = FTAnalyzerGermanV1
	if raw, err := EncodeTable(ft); err == nil {
		f.Add(raw)
	}
	ft.Indexes[0].FTAnalyzer = FTAnalyzerSpanish
	ft.Indexes[0].FTVersion = FTAnalyzerSpanishV1
	if raw, err := EncodeTable(ft); err == nil {
		f.Add(raw)
	}
	// SPARSE method on a SPARSEVECTOR column (catalog v8 method whitelist).
	sp := partitionTestTable()
	if vt, err := types.VectorSparse(64); err == nil {
		sp.Columns = append(sp.Columns, Column{Name: "emb", Type: vt})
		sp.Indexes = []Index{{
			Name: "ix_emb", Vector: true, Columns: []int{2}, Meta: 40,
			VecMethod: VecMethodSPARSE,
		}}
		if raw, err := EncodeTable(sp); err == nil {
			f.Add(raw)
		}
	}
	f.Add([]byte("NSCT"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = DecodeTable(raw)
	})
}
