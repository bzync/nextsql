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
	f.Add([]byte("NSCT"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = DecodeTable(raw)
	})
}
