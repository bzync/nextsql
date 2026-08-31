package executor

import (
	"sort"
	"strconv"

	"github.com/bzync/nextsql/internal/fulltext"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
)

type facetBucket struct {
	value types.Value
	count int64
}

func (s *Session) execFacet(f *planner.Facet, input planner.Logical) ([][]types.Value, error) {
	rows, err := s.collectPlan(input)
	if err != nil {
		return nil, err
	}
	return s.facetRows(f, rows)
}

func (s *Session) facetRows(f *planner.Facet, rows [][]types.Value) ([][]types.Value, error) {
	if f == nil || len(f.Columns) == 0 {
		return nil, nerr.New(nerr.Internal, "executor.facet", "missing FACET columns")
	}
	if f.Limit == 0 {
		return nil, nil
	}
	if len(f.Names) != len(f.Columns) {
		return nil, nerr.New(nerr.Internal, "executor.facet", "FACET names must match columns")
	}
	buckets := make([]map[string]facetBucket, len(f.Columns))
	for i := range buckets {
		buckets[i] = make(map[string]facetBucket)
	}
	for _, row := range rows {
		if err := s.budget().Check(); err != nil {
			return nil, err
		}
		for i, col := range f.Columns {
			if col < 0 || col >= len(row) {
				return nil, nerr.New(nerr.Internal, "executor.facet", "FACET column out of range")
			}
			v := row[col]
			if v.Null {
				continue
			}
			key := v.String()
			b, ok := buckets[i][key]
			if !ok {
				if len(buckets[i]) >= fulltext.MaxFacetValues {
					return nil, nerr.New(nerr.Exhausted, "executor.facet", "FACET exceeds the distinct-value limit")
				}
				b.value = v.Clone()
			}
			b.count++
			buckets[i][key] = b
		}
	}
	var out [][]types.Value
	for i, m := range buckets {
		list := make([]facetBucket, 0, len(m))
		for _, b := range m {
			list = append(list, b)
		}
		sort.Slice(list, func(a, b int) bool {
			if list[a].count != list[b].count {
				return list[a].count > list[b].count
			}
			cmp, err := list[a].value.Cmp(list[b].value)
			if err != nil {
				return list[a].value.String() < list[b].value.String()
			}
			return cmp < 0
		})
		if f.Limit > 0 && int64(len(list)) > f.Limit {
			list = list[:f.Limit]
		}
		name := types.StringValue(f.Names[i])
		for _, b := range list {
			count, err := facetCountValue(b.count)
			if err != nil {
				return nil, err
			}
			out = append(out, []types.Value{name, types.StringValue(b.value.String()), count})
		}
	}
	return out, nil
}

func facetCountValue(n int64) (types.Value, error) {
	d, err := types.ParseDecimal(strconv.FormatInt(n, 10))
	if err != nil {
		return types.Value{}, err
	}
	return types.DecimalValue(d, types.Type{Kind: types.KindDecimal}), nil
}
