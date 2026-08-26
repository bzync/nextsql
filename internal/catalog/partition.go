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
	if p.Kind < PartitionRange || p.Kind > PartitionTenant {
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
	if p.Kind == PartitionTenant {
		tenantCol, ok := t.TenantCol()
		if !ok || len(p.Columns) != 1 || p.Columns[0] != tenantCol {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "TENANT partition key must be tenant_id")
		}
	}
	seenIDs := make(map[uint32]struct{}, len(p.Partitions))
	seenNames := make(map[string]struct{}, len(p.Partitions))
	seenRules := make(map[string]struct{})
	valueCount := 0
	var hashModulus uint32
	hasVector := t.HasVector()
	for _, part := range p.Partitions {
		if part.ID == 0 {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "zero partition identity")
		}
		if _, exists := seenIDs[part.ID]; exists {
			return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "duplicate partition identity")
		}
		seenIDs[part.ID] = struct{}{}
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
		case PartitionList, PartitionTenant:
			if len(part.Values) == 0 || part.Modulus != 0 || part.Remainder != 0 {
				return nerr.New(nerr.InvalidArgument, "catalog.ValidatePartitioning", "invalid value partition rule")
			}
			if p.Kind == PartitionTenant && len(part.Values) != 1 {
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
