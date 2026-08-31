package catalog

import (
	"bytes"
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

// ValidatePartitioning rejects descriptors that could make routing ambiguous,
// allocate unbounded decoder state, or reference an unsupported key type.
func ValidatePartitioning(t *Table) error {
	if t == nil || t.Partitioning == nil {
		return nil
	}
	p := t.Partitioning
	if p.Kind < PartitionRange || p.Kind > PartitionLegacyTenant {
		return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "unknown partition kind")
	}
	if len(p.Columns) == 0 || len(p.Columns) > MaxPartitionColumns {
		return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "partition column count")
	}
	columnTypes := make([]types.Type, len(p.Columns))
	seenColumns := make(map[int]struct{}, len(p.Columns))
	for i, ord := range p.Columns {
		if ord < 0 || ord >= len(t.Columns) {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "partition column ordinal")
		}
		if _, exists := seenColumns[ord]; exists {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "duplicate partition column")
		}
		seenColumns[ord] = struct{}{}
		columnTypes[i] = t.Columns[ord].Type
		if columnTypes[i].Kind == types.KindVector {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "VECTOR partition key")
		}
	}
	if len(p.Partitions) == 0 || len(p.Partitions) > MaxPartitions {
		return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "partition count")
	}
	if p.Kind == PartitionLegacyTenant {
		tenantCol, ok := t.LegacyTenantCol()
		if !ok || len(p.Columns) != 1 || p.Columns[0] != tenantCol {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "TENANT partition key must be tenant_id")
		}
	}
	seenIDs := make(map[uint32]struct{}, len(p.Partitions))
	seenNames := make(map[string]struct{}, len(p.Partitions))
	seenRules := make(map[string]struct{})
	valueCount := 0
	var hashModulus uint32
	var maxID uint32
	hasVector := t.HasVector()
	for _, part := range p.Partitions {
		if part.ID == 0 {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "zero partition identity")
		}
		if _, exists := seenIDs[part.ID]; exists {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "duplicate partition identity")
		}
		seenIDs[part.ID] = struct{}{}
		if part.ID > maxID {
			maxID = part.ID
		}
		if strings.TrimSpace(part.Name) == "" || len(part.Name) > MaxPartitionNameLength {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "partition name")
		}
		if _, exists := seenNames[part.Name]; exists {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "duplicate partition name")
		}
		seenNames[part.Name] = struct{}{}
		if part.HeapMeta == 0 {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "zero partition heap metadata")
		}
		if (part.VecMeta != 0) != hasVector {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "partition vector metadata mismatch")
		}
		if err := validatePartitionIndexes(t, part); err != nil {
			return err
		}
		valueCount += len(part.Values)
		if valueCount > MaxPartitionValues {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "partition value limit")
		}
		switch p.Kind {
		case PartitionRange:
			if len(part.Values) != 2 || part.Modulus != 0 || part.Remainder != 0 {
				return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "invalid RANGE rule")
			}
			for _, tuple := range part.Values {
				if tuple != nil {
					if err := validatePartitionTuple(tuple, columnTypes); err != nil {
						return err
					}
				}
			}
			if part.Values[0] == nil && part.LowerInclusive {
				return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "inclusive unbounded RANGE lower edge")
			}
			if part.Values[1] == nil && part.UpperInclusive {
				return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "inclusive unbounded RANGE upper edge")
			}
		case PartitionHash:
			if len(part.Values) != 0 || part.Modulus == 0 || part.Modulus > MaxPartitions || part.Remainder >= part.Modulus {
				return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "invalid HASH rule")
			}
			if hashModulus == 0 {
				hashModulus = part.Modulus
			} else if hashModulus != part.Modulus {
				return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "mixed HASH modulus")
			}
			rule := string([]byte{byte(part.Remainder >> 24), byte(part.Remainder >> 16), byte(part.Remainder >> 8), byte(part.Remainder)})
			if _, exists := seenRules[rule]; exists {
				return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "duplicate HASH remainder")
			}
			seenRules[rule] = struct{}{}
		case PartitionList, PartitionLegacyTenant:
			if len(part.Values) == 0 || part.Modulus != 0 || part.Remainder != 0 {
				return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "invalid value partition rule")
			}
			if p.Kind == PartitionLegacyTenant && len(part.Values) != 1 {
				return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "TENANT partition requires one value")
			}
			for _, tuple := range part.Values {
				if err := validatePartitionTuple(tuple, columnTypes); err != nil {
					return err
				}
				raw, err := types.EncodeKey(tuple)
				if err != nil {
					return err
				}
				rule := string(raw)
				if _, exists := seenRules[rule]; exists {
					return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "overlapping partition value")
				}
				seenRules[rule] = struct{}{}
			}
		}
	}
	nextID := p.NextID
	if nextID == 0 && maxID != ^uint32(0) {
		nextID = maxID + 1
	}
	if nextID == 0 || nextID == ^uint32(0) || nextID <= maxID {
		return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "invalid next partition identity")
	}
	if p.Kind == PartitionHash && (int(hashModulus) != len(p.Partitions) || len(seenRules) != len(p.Partitions)) {
		return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "incomplete HASH remainder set")
	}
	if p.Kind == PartitionRange {
		for i, part := range p.Partitions {
			lower, upper := part.Values[0], part.Values[1]
			if lower != nil && upper != nil {
				cmp, err := comparePartitionTuple(lower, upper)
				if err != nil {
					return err
				}
				if cmp >= 0 {
					return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "empty or reversed RANGE partition")
				}
			}
			if i == 0 {
				continue
			}
			prev := p.Partitions[i-1]
			prevUpper := prev.Values[1]
			if prevUpper == nil || lower == nil {
				return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "overlapping unbounded RANGE partitions")
			}
			cmp, err := comparePartitionTuple(prevUpper, lower)
			if err != nil {
				return err
			}
			if cmp > 0 || (cmp == 0 && prev.UpperInclusive && part.LowerInclusive) {
				return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "overlapping RANGE partitions")
			}
		}
	}
	return nil
}

func validatePartitionTuple(tuple []types.Value, want []types.Type) error {
	if len(tuple) != len(want) {
		return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "partition tuple width")
	}
	for i, value := range tuple {
		if value.Null {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "NULL partition value")
		}
		if !value.Typ.Equals(want[i]) {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "partition value type")
		}
	}
	return nil
}

func validatePartitionIndexes(t *Table, part Partition) error {
	want := make(map[string]struct{}, len(t.Indexes))
	for _, idx := range t.Indexes {
		want[idx.Name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(part.Indexes))
	for _, idx := range part.Indexes {
		if _, ok := want[idx.Name]; !ok || idx.Meta == 0 {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "invalid partition index metadata")
		}
		if _, exists := seen[idx.Name]; exists {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "duplicate partition index metadata")
		}
		seen[idx.Name] = struct{}{}
	}
	if len(seen) != len(want) {
		return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "missing partition index metadata")
	}
	return nil
}

func partitionTupleTypes(t *Table) []types.Type {
	if t == nil || t.Partitioning == nil {
		return nil
	}
	out := make([]types.Type, len(t.Partitioning.Columns))
	for i, ord := range t.Partitioning.Columns {
		if ord >= 0 && ord < len(t.Columns) {
			out[i] = t.Columns[ord].Type
		}
	}
	return out
}

func comparePartitionTuple(a, b []types.Value) (int, error) {
	ak, err := types.EncodeKey(a)
	if err != nil {
		return 0, err
	}
	bk, err := types.EncodeKey(b)
	if err != nil {
		return 0, err
	}
	return bytes.Compare(ak, bk), nil
}

// PartitionJoinPair pairs one left-table partition with the right-table
// partition that can hold its join partners under a partition-aligned join.
type PartitionJoinPair struct {
	Left  uint32
	Right uint32
}

// AlignedPartitionJoin reports whether an equi-join between left and right,
// where left column leftKeys[i] is equated to right column rightKeys[i], is
// partition-aligned: both tables use an identical physical partition scheme and
// every partition-key column of each side is equated to the positionally
// corresponding partition-key column of the other side. When it returns true a
// row on either side can only match rows in the single paired partition on the
// other side, so the join may execute as one independent join per returned
// pair. RANGE partitions pair by identical bound tuples, HASH by a shared
// modulus and remainder, LIST by identical value groupings. Legacy TENANT and
// unpartitioned tables are never aligned.
func AlignedPartitionJoin(left, right *Table, leftKeys, rightKeys []int) ([]PartitionJoinPair, bool) {
	if left == nil || right == nil || left.Partitioning == nil || right.Partitioning == nil {
		return nil, false
	}
	lp, rp := left.Partitioning, right.Partitioning
	if lp.Kind != rp.Kind {
		return nil, false
	}
	switch lp.Kind {
	case PartitionRange, PartitionHash, PartitionList:
	default:
		return nil, false
	}
	if len(lp.Columns) == 0 || len(lp.Columns) != len(rp.Columns) {
		return nil, false
	}
	if len(lp.Partitions) == 0 || len(lp.Partitions) != len(rp.Partitions) {
		return nil, false
	}
	if len(leftKeys) != len(rightKeys) {
		return nil, false
	}
	lt, rt := left.Types(), right.Types()
	for i := range lp.Columns {
		lc, rc := lp.Columns[i], rp.Columns[i]
		if lc < 0 || lc >= len(lt) || rc < 0 || rc >= len(rt) {
			return nil, false
		}
		if lt[lc] != rt[rc] {
			return nil, false
		}
		aligned := false
		for j := range leftKeys {
			if leftKeys[j] == lc && rightKeys[j] == rc {
				aligned = true
				break
			}
		}
		if !aligned {
			return nil, false
		}
	}
	switch lp.Kind {
	case PartitionRange:
		pairs := make([]PartitionJoinPair, len(lp.Partitions))
		for i := range lp.Partitions {
			if !sameRangeBounds(lp.Partitions[i], rp.Partitions[i]) {
				return nil, false
			}
			pairs[i] = PartitionJoinPair{lp.Partitions[i].ID, rp.Partitions[i].ID}
		}
		return pairs, true
	case PartitionHash:
		byRemainder := make(map[uint32]uint32, len(rp.Partitions))
		var rmod uint32
		for _, p := range rp.Partitions {
			byRemainder[p.Remainder] = p.ID
			rmod = p.Modulus
		}
		pairs := make([]PartitionJoinPair, 0, len(lp.Partitions))
		for _, p := range lp.Partitions {
			if p.Modulus == 0 || p.Modulus != rmod {
				return nil, false
			}
			rid, ok := byRemainder[p.Remainder]
			if !ok {
				return nil, false
			}
			pairs = append(pairs, PartitionJoinPair{p.ID, rid})
		}
		return pairs, true
	case PartitionList:
		used := make([]bool, len(rp.Partitions))
		pairs := make([]PartitionJoinPair, 0, len(lp.Partitions))
		for _, lpart := range lp.Partitions {
			match := -1
			for k := range rp.Partitions {
				if used[k] {
					continue
				}
				if sameValueTupleSet(lpart.Values, rp.Partitions[k].Values) {
					match = k
					break
				}
			}
			if match < 0 {
				return nil, false
			}
			used[match] = true
			pairs = append(pairs, PartitionJoinPair{lpart.ID, rp.Partitions[match].ID})
		}
		return pairs, true
	}
	return nil, false
}

func sameRangeBounds(a, b Partition) bool {
	if a.LowerInclusive != b.LowerInclusive || a.UpperInclusive != b.UpperInclusive {
		return false
	}
	if len(a.Values) != 2 || len(b.Values) != 2 {
		return false
	}
	return sameTuple(a.Values[0], b.Values[0]) && sameTuple(a.Values[1], b.Values[1])
}

func sameTuple(a, b []types.Value) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	ak, err := types.EncodeKey(a)
	if err != nil {
		return false
	}
	bk, err := types.EncodeKey(b)
	if err != nil {
		return false
	}
	return bytes.Equal(ak, bk)
}

func sameValueTupleSet(a, b [][]types.Value) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, v := range a {
		k, err := types.EncodeKey(v)
		if err != nil {
			return false
		}
		counts[string(k)]++
	}
	for _, v := range b {
		k, err := types.EncodeKey(v)
		if err != nil {
			return false
		}
		counts[string(k)]--
		if counts[string(k)] < 0 {
			return false
		}
	}
	for _, c := range counts {
		if c != 0 {
			return false
		}
	}
	return true
}
