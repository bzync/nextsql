package catalog

import (
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/format"
)

func partitionTestTable() *Table {
	return &Table{
		ID: 1, Name: "events", HeapMeta: 7,
		Columns: []Column{
			{Name: "tenant_id", Type: types.String(), NotNull: true, Primary: true},
			{Name: "id", Type: types.String(), NotNull: true, Primary: true},
		},
		PK: []int{0, 1},
	}
}

func TestPartitionCatalogRoundTripAllKinds(t *testing.T) {
	tests := []struct {
		name  string
		kind  PartitionKind
		cols  []int
		parts []Partition
	}{
		{
			name: "range", kind: PartitionRange, cols: []int{1},
			parts: []Partition{
				{ID: 1, Name: "early", HeapMeta: 11, Values: [][]types.Value{nil, {types.StringValue("m")}}},
				{ID: 2, Name: "late", HeapMeta: 12, LowerInclusive: true, Values: [][]types.Value{{types.StringValue("m")}, nil}},
			},
		},
		{
			name: "hash", kind: PartitionHash, cols: []int{1},
			parts: []Partition{
				{ID: 1, Name: "h0", HeapMeta: 21, Modulus: 2, Remainder: 0},
				{ID: 2, Name: "h1", HeapMeta: 22, Modulus: 2, Remainder: 1},
			},
		},
		{
			name: "list", kind: PartitionList, cols: []int{1},
			parts: []Partition{
				{ID: 1, Name: "ab", HeapMeta: 31, Values: [][]types.Value{{types.StringValue("a")}, {types.StringValue("b")}}},
				{ID: 2, Name: "c", HeapMeta: 32, Values: [][]types.Value{{types.StringValue("c")}}},
			},
		},
		{
			name: "tenant", kind: PartitionTenant, cols: []int{0},
			parts: []Partition{
				{ID: 1, Name: "tenant_a", HeapMeta: 41, Values: [][]types.Value{{types.StringValue("a")}}},
				{ID: 2, Name: "tenant_b", HeapMeta: 42, Values: [][]types.Value{{types.StringValue("b")}}},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tab := partitionTestTable()
			tab.Partitioning = &Partitioning{Kind: tc.kind, Columns: tc.cols, Partitions: tc.parts}
			raw, err := EncodeTable(tab)
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodeTable(raw)
			if err != nil {
				t.Fatal(err)
			}
			if got.Partitioning == nil || got.Partitioning.Kind != tc.kind || len(got.Partitioning.Partitions) != len(tc.parts) {
				t.Fatalf("partitioning=%+v", got.Partitioning)
			}
			clone := got.Clone()
			clone.Partitioning.Partitions[0].Name = "changed"
			if got.Partitioning.Partitions[0].Name == "changed" {
				t.Fatal("clone aliases partition metadata")
			}
		})
	}
}

func TestPartitionCatalogIndexesRoundTrip(t *testing.T) {
	tab := partitionTestTable()
	vectorType, err := types.VectorF32(3)
	if err != nil {
		t.Fatal(err)
	}
	tab.Columns = append(tab.Columns, Column{Name: "embedding", Type: vectorType})
	tab.Indexes = []Index{{Name: "by_id", Columns: []int{1}, Meta: 50}}
	tab.Partitioning = &Partitioning{Kind: PartitionHash, Columns: []int{1}, Partitions: []Partition{
		{ID: 1, Name: "h0", HeapMeta: 51, VecMeta: 70, Modulus: 2, Remainder: 0, Indexes: []PartitionIndex{{Name: "by_id", Meta: 61}}},
		{ID: 2, Name: "h1", HeapMeta: 52, VecMeta: 71, Modulus: 2, Remainder: 1, Indexes: []PartitionIndex{{Name: "by_id", Meta: 62}}},
	}}
	raw, err := EncodeTable(tab)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeTable(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Partitioning.Partitions[1].VecMeta != 71 || got.Partitioning.Partitions[1].Indexes[0].Meta != 62 {
		t.Fatalf("partition=%+v", got.Partitioning.Partitions[1])
	}
}

func TestHashPartitionRemainderStable(t *testing.T) {
	got, err := HashPartitionRemainder([]types.Value{types.StringValue("alpha")}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got != 3 {
		t.Fatalf("remainder=%d want 3", got)
	}
	if _, err := HashPartitionRemainder([]types.Value{types.StringValue("alpha")}, 0); err == nil {
		t.Fatal("zero modulus accepted")
	}

	tab := partitionTestTable()
	tab.Partitioning = &Partitioning{
		Kind:    PartitionHash,
		Columns: []int{1},
		Partitions: []Partition{
			{ID: 1, Name: "h0", HeapMeta: 50, Modulus: 4, Remainder: 0},
			{ID: 2, Name: "h1", HeapMeta: 51, Modulus: 4, Remainder: 1},
			{ID: 3, Name: "h2", HeapMeta: 52, Modulus: 4, Remainder: 2},
			{ID: 4, Name: "h3", HeapMeta: 53, Modulus: 4, Remainder: 3},
		},
	}
	part, err := tab.PartitionForRow([]types.Value{types.StringValue("id"), types.StringValue("alpha")})
	if err != nil {
		t.Fatal(err)
	}
	if part == nil || part.Name != "h3" {
		t.Fatalf("partition=%+v want h3", part)
	}
}

func TestPartitionCatalogRejectsAmbiguousOrUnboundedMetadata(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Table)
	}{
		{"duplicate list value", func(tab *Table) {
			tab.Partitioning = &Partitioning{Kind: PartitionList, Columns: []int{1}, Partitions: []Partition{
				{ID: 1, Name: "a", HeapMeta: 11, Values: [][]types.Value{{types.StringValue("x")}}},
				{ID: 2, Name: "b", HeapMeta: 12, Values: [][]types.Value{{types.StringValue("x")}}},
			}}
		}},
		{"overlapping range", func(tab *Table) {
			tab.Partitioning = &Partitioning{Kind: PartitionRange, Columns: []int{1}, Partitions: []Partition{
				{ID: 1, Name: "a", HeapMeta: 11, UpperInclusive: true, Values: [][]types.Value{nil, {types.StringValue("m")}}},
				{ID: 2, Name: "b", HeapMeta: 12, LowerInclusive: true, Values: [][]types.Value{{types.StringValue("m")}, nil}},
			}}
		}},
		{"incomplete hash", func(tab *Table) {
			tab.Partitioning = &Partitioning{Kind: PartitionHash, Columns: []int{1}, Partitions: []Partition{
				{ID: 1, Name: "a", HeapMeta: 11, Modulus: 3, Remainder: 0},
				{ID: 2, Name: "b", HeapMeta: 12, Modulus: 3, Remainder: 1},
			}}
		}},
		{"tenant wrong column", func(tab *Table) {
			tab.Partitioning = &Partitioning{Kind: PartitionTenant, Columns: []int{1}, Partitions: []Partition{
				{ID: 1, Name: "a", HeapMeta: 11, Values: [][]types.Value{{types.StringValue("a")}}},
			}}
		}},
		{"missing local index", func(tab *Table) {
			tab.Indexes = []Index{{Name: "by_id", Columns: []int{1}, Meta: 50}}
			tab.Partitioning = &Partitioning{Kind: PartitionHash, Columns: []int{1}, Partitions: []Partition{
				{ID: 1, Name: "a", HeapMeta: 11, Modulus: 1, Remainder: 0},
			}}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tab := partitionTestTable()
			tc.edit(tab)
			if _, err := EncodeTable(tab); err == nil || !nerr.HasCode(err, nerr.InvalidArgument) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestPartitionCatalogDecoderRejectsTruncation(t *testing.T) {
	tab := partitionTestTable()
	tab.Partitioning = &Partitioning{Kind: PartitionHash, Columns: []int{1}, Partitions: []Partition{
		{ID: 1, Name: "only", HeapMeta: format.PageID(11), Modulus: 1, Remainder: 0},
	}}
	raw, err := EncodeTable(tab)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < len(raw); n++ {
		if _, err := DecodeTable(raw[:n]); err == nil {
			t.Fatalf("accepted truncation at %d/%d", n, len(raw))
		}
	}
}
