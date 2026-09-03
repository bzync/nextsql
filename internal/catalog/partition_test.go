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

func TestAlignedPartitionJoin(t *testing.T) {
	mk := func(kind PartitionKind, parts []Partition) *Table {
		return &Table{
			ID: 1, Name: "t",
			Columns: []Column{
				{Name: "g", Type: types.String(), NotNull: true, Primary: true},
				{Name: "id", Type: types.String(), NotNull: true, Primary: true},
			},
			PK:           []int{0, 1},
			Partitioning: &Partitioning{Kind: kind, NextID: 99, Columns: []int{0}, Partitions: parts},
		}
	}
	rangeParts := func() []Partition {
		return []Partition{
			{ID: 1, Name: "p0", HeapMeta: 11, Values: [][]types.Value{nil, {types.StringValue("m")}}},
			{ID: 2, Name: "p1", HeapMeta: 12, LowerInclusive: true, Values: [][]types.Value{{types.StringValue("m")}, nil}},
		}
	}

	// Aligned RANGE on the partition column.
	l, r := mk(PartitionRange, rangeParts()), mk(PartitionRange, rangeParts())
	r.Partitioning.Partitions[0].ID, r.Partitioning.Partitions[1].ID = 5, 6
	pairs, ok := AlignedPartitionJoin(l, r, []int{0, 1}, []int{0, 1})
	if !ok || len(pairs) != 2 || pairs[0] != (PartitionJoinPair{1, 5}) || pairs[1] != (PartitionJoinPair{2, 6}) {
		t.Fatalf("aligned RANGE join pairs=%+v ok=%v", pairs, ok)
	}

	// Join key does not cover the partition column.
	if _, ok := AlignedPartitionJoin(l, r, []int{1}, []int{1}); ok {
		t.Fatal("join without the partition key was treated as aligned")
	}

	// Incompatible RANGE bounds.
	skew := mk(PartitionRange, []Partition{
		{ID: 1, Name: "q0", HeapMeta: 11, Values: [][]types.Value{nil, {types.StringValue("k")}}},
		{ID: 2, Name: "q1", HeapMeta: 12, LowerInclusive: true, Values: [][]types.Value{{types.StringValue("k")}, nil}},
	})
	if _, ok := AlignedPartitionJoin(l, skew, []int{0, 1}, []int{0, 1}); ok {
		t.Fatal("mismatched RANGE bounds treated as aligned")
	}

	// HASH pairs by remainder even when partition order differs.
	hl := mk(PartitionHash, []Partition{
		{ID: 1, Name: "h0", HeapMeta: 21, Modulus: 2, Remainder: 0},
		{ID: 2, Name: "h1", HeapMeta: 22, Modulus: 2, Remainder: 1},
	})
	hr := mk(PartitionHash, []Partition{
		{ID: 8, Name: "h1", HeapMeta: 22, Modulus: 2, Remainder: 1},
		{ID: 9, Name: "h0", HeapMeta: 21, Modulus: 2, Remainder: 0},
	})
	pairs, ok = AlignedPartitionJoin(hl, hr, []int{0}, []int{0})
	if !ok || len(pairs) != 2 || pairs[0] != (PartitionJoinPair{1, 9}) || pairs[1] != (PartitionJoinPair{2, 8}) {
		t.Fatalf("aligned HASH join pairs=%+v ok=%v", pairs, ok)
	}
	// Different modulus.
	hr.Partitioning.Partitions[0].Modulus, hr.Partitioning.Partitions[1].Modulus = 4, 4
	if _, ok := AlignedPartitionJoin(hl, hr, []int{0}, []int{0}); ok {
		t.Fatal("different HASH modulus treated as aligned")
	}

	// LIST pairs by identical value grouping regardless of order.
	ll := mk(PartitionList, []Partition{
		{ID: 1, Name: "ab", HeapMeta: 31, Values: [][]types.Value{{types.StringValue("a")}, {types.StringValue("b")}}},
		{ID: 2, Name: "c", HeapMeta: 32, Values: [][]types.Value{{types.StringValue("c")}}},
	})
	lr := mk(PartitionList, []Partition{
		{ID: 7, Name: "c", HeapMeta: 32, Values: [][]types.Value{{types.StringValue("c")}}},
		{ID: 8, Name: "ba", HeapMeta: 31, Values: [][]types.Value{{types.StringValue("b")}, {types.StringValue("a")}}},
	})
	pairs, ok = AlignedPartitionJoin(ll, lr, []int{0}, []int{0})
	if !ok || len(pairs) != 2 || pairs[0] != (PartitionJoinPair{1, 8}) || pairs[1] != (PartitionJoinPair{2, 7}) {
		t.Fatalf("aligned LIST join pairs=%+v ok=%v", pairs, ok)
	}
	// Different grouping: {a,b}|{c} vs {a}|{b,c}.
	lr.Partitioning.Partitions[0].Values = [][]types.Value{{types.StringValue("b")}, {types.StringValue("c")}}
	lr.Partitioning.Partitions[1].Values = [][]types.Value{{types.StringValue("a")}}
	if _, ok := AlignedPartitionJoin(ll, lr, []int{0}, []int{0}); ok {
		t.Fatal("different LIST grouping treated as aligned")
	}

	// Different kinds, legacy tenant, and nil tables are never aligned.
	if _, ok := AlignedPartitionJoin(l, hl, []int{0}, []int{0}); ok {
		t.Fatal("RANGE/HASH mix treated as aligned")
	}
	tenant := mk(PartitionLegacyTenant, []Partition{{ID: 1, Name: "a", HeapMeta: 41, Values: [][]types.Value{{types.StringValue("a")}}}})
	if _, ok := AlignedPartitionJoin(tenant, tenant, []int{0}, []int{0}); ok {
		t.Fatal("legacy TENANT treated as aligned")
	}
	if _, ok := AlignedPartitionJoin(nil, r, []int{0}, []int{0}); ok {
		t.Fatal("nil table treated as aligned")
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
			name: "legacy_tenant", kind: PartitionLegacyTenant, cols: []int{0},
			parts: []Partition{
				{ID: 1, Name: "tenant_a", HeapMeta: 41, Values: [][]types.Value{{types.StringValue("a")}}},
				{ID: 2, Name: "tenant_b", HeapMeta: 42, Values: [][]types.Value{{types.StringValue("b")}}},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tab := partitionTestTable()
			tab.Partitioning = &Partitioning{Kind: tc.kind, NextID: 3, Columns: tc.cols, Partitions: tc.parts}
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
			if got.Partitioning.NextID != 3 {
				t.Fatalf("next partition id=%d", got.Partitioning.NextID)
			}
			clone := got.Clone()
			clone.Partitioning.Partitions[0].Name = "changed"
			if got.Partitioning.Partitions[0].Name == "changed" {
				t.Fatal("clone aliases partition metadata")
			}
		})
	}
}

func TestPartitionCatalogV5ReadsNextID(t *testing.T) {
	tab := partitionTestTable()
	tab.Partitioning = &Partitioning{Kind: PartitionList, NextID: 9, Columns: []int{1}, Partitions: []Partition{
		{ID: 2, Name: "a", HeapMeta: 31, Values: [][]types.Value{{types.StringValue("a")}}},
		{ID: 7, Name: "b", HeapMeta: 32, Values: [][]types.Value{{types.StringValue("b")}}},
	}}
	raw, err := EncodeTable(tab)
	if err != nil {
		t.Fatal(err)
	}
	// Zero indexes: v6+ trailers are empty, so a v5 version byte on a current
	// body must still consume NextID (not wait for tableVersion == current).
	// Strip v10's one-byte-per-column flag and v11's two-byte-per-column
	// ENUM label count.
	v5 := append([]byte(nil), raw[:len(raw)-len(tab.Columns)*3]...)
	v5[4], v5[5] = byte(tableVersionV5), 0
	got, err := DecodeTable(v5)
	if err != nil {
		t.Fatal(err)
	}
	if got.Partitioning.NextID != 9 {
		t.Fatalf("v5 next partition id=%d want 9", got.Partitioning.NextID)
	}
}

func TestPartitionCatalogV4DerivesNextIdentity(t *testing.T) {
	tab := partitionTestTable()
	tab.Partitioning = &Partitioning{Kind: PartitionList, NextID: 9, Columns: []int{1}, Partitions: []Partition{
		{ID: 2, Name: "a", HeapMeta: 31, Values: [][]types.Value{{types.StringValue("a")}}},
		{ID: 7, Name: "b", HeapMeta: 32, Values: [][]types.Value{{types.StringValue("b")}}},
	}}
	raw, err := EncodeTable(tab)
	if err != nil {
		t.Fatal(err)
	}
	// NSCT v4 ended immediately after the partition list; v5 appends NextID.
	// Strip v10's one-byte-per-column flag and v11's two-byte-per-column
	// ENUM label count (3 bytes/column total) in addition to NextID's 4 bytes.
	v4 := append([]byte(nil), raw[:len(raw)-len(tab.Columns)*3-4]...)
	v4[4], v4[5] = byte(tableVersionV4), 0
	got, err := DecodeTable(v4)
	if err != nil {
		t.Fatal(err)
	}
	if got.Partitioning.NextID != 8 {
		t.Fatalf("derived next partition id=%d want 8", got.Partitioning.NextID)
	}
	zeroNext := append([]byte(nil), raw...)
	for i := len(zeroNext) - len(tab.Columns)*3 - 4; i < len(zeroNext)-len(tab.Columns)*3; i++ {
		zeroNext[i] = 0
	}
	if _, err := DecodeTable(zeroNext); err == nil {
		t.Fatal("v5 zero next partition identity accepted")
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

func TestMultiColumnHashPartitionRoundTripAndRouting(t *testing.T) {
	tab := partitionTestTable()
	// tenant_id + id are both partition columns and both in the PK.
	tab.Partitioning = &Partitioning{Kind: PartitionHash, NextID: 3, Columns: []int{0, 1}, Partitions: []Partition{
		{ID: 1, Name: "h0", HeapMeta: 11, Modulus: 2, Remainder: 0},
		{ID: 2, Name: "h1", HeapMeta: 12, Modulus: 2, Remainder: 1},
	}}
	if err := ValidatePartitioning(tab); err != nil {
		t.Fatalf("validate: %v", err)
	}
	raw, err := EncodeTable(tab)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeTable(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Partitioning.Columns) != 2 || got.Partitioning.Columns[0] != 0 || got.Partitioning.Columns[1] != 1 {
		t.Fatalf("columns=%v", got.Partitioning.Columns)
	}

	row := []types.Value{types.StringValue("acme"), types.StringValue("s-1")}
	tuple := []types.Value{row[0], row[1]}
	want, err := HashPartitionRemainder(tuple, 2)
	if err != nil {
		t.Fatal(err)
	}
	part, err := got.PartitionForRow(row)
	if err != nil {
		t.Fatal(err)
	}
	if part == nil || part.Remainder != want {
		t.Fatalf("routed to %+v want remainder %d", part, want)
	}
	// Swapping the two column values must be able to change the routed partition
	// (tuple order matters), so routing is not order-insensitive.
	swapped := []types.Value{types.StringValue("s-1"), types.StringValue("acme")}
	if _, err := got.PartitionForRow(swapped); err != nil {
		t.Fatal(err)
	}
}

func TestMultiColumnRangeAndListPartitionRoundTripAndRouting(t *testing.T) {
	t.Run("range", func(t *testing.T) {
		tab := partitionTestTable()
		// (tenant_id, id) RANGE key with lexicographically ordered tuple bounds.
		tab.Partitioning = &Partitioning{Kind: PartitionRange, NextID: 3, Columns: []int{0, 1}, Partitions: []Partition{
			{ID: 1, Name: "lo", HeapMeta: 11, Values: [][]types.Value{nil, {types.StringValue("m"), types.StringValue("")}}},
			{ID: 2, Name: "hi", HeapMeta: 12, LowerInclusive: true, Values: [][]types.Value{{types.StringValue("m"), types.StringValue("")}, nil}},
		}}
		if err := ValidatePartitioning(tab); err != nil {
			t.Fatalf("validate: %v", err)
		}
		raw, err := EncodeTable(tab)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeTable(raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			row  []types.Value
			want string
		}{
			{[]types.Value{types.StringValue("acme"), types.StringValue("z")}, "lo"},
			{[]types.Value{types.StringValue("m"), types.StringValue("a")}, "hi"},
			{[]types.Value{types.StringValue("zzz"), types.StringValue("a")}, "hi"},
		} {
			part, err := got.PartitionForRow(tc.row)
			if err != nil {
				t.Fatalf("route %v: %v", tc.row, err)
			}
			if part == nil || part.Name != tc.want {
				t.Fatalf("row %v routed to %+v want %s", tc.row, part, tc.want)
			}
		}
	})
	t.Run("list", func(t *testing.T) {
		tab := partitionTestTable()
		tab.Partitioning = &Partitioning{Kind: PartitionList, NextID: 3, Columns: []int{0, 1}, Partitions: []Partition{
			{ID: 1, Name: "a", HeapMeta: 11, Values: [][]types.Value{
				{types.StringValue("us"), types.StringValue("gold")},
				{types.StringValue("eu"), types.StringValue("gold")},
			}},
			{ID: 2, Name: "b", HeapMeta: 12, Values: [][]types.Value{
				{types.StringValue("us"), types.StringValue("bronze")},
			}},
		}}
		if err := ValidatePartitioning(tab); err != nil {
			t.Fatalf("validate: %v", err)
		}
		raw, err := EncodeTable(tab)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeTable(raw)
		if err != nil {
			t.Fatal(err)
		}
		part, err := got.PartitionForRow([]types.Value{types.StringValue("eu"), types.StringValue("gold")})
		if err != nil || part == nil || part.Name != "a" {
			t.Fatalf("routed to %+v err=%v", part, err)
		}
		if _, err := got.PartitionForRow([]types.Value{types.StringValue("eu"), types.StringValue("bronze")}); err == nil {
			t.Fatal("tuple outside every LIST partition must fail closed")
		}
	})
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
			tab.Partitioning = &Partitioning{Kind: PartitionLegacyTenant, Columns: []int{1}, Partitions: []Partition{
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
