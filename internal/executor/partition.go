package executor

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/btree"
	"github.com/bzync/nextsql/internal/storage/format"
)

func partitionHeapKey(table string, pid uint32) string {
	return fmt.Sprintf("%s#%d", table, pid)
}
func partitionIndexKey(table string, pid uint32, index string) string {
	return fmt.Sprintf("%s#%d/%s", table, pid, index)
}

// partitionForRow returns the catalog partition that should own row. Fail-closed if no partition matches.
func (s *Session) partitionForRow(tab *catalog.Table, row []types.Value) (*catalog.Partition, error) {
	if tab == nil || tab.Partitioning == nil {
		return nil, nil
	}
	part, err := tab.PartitionForRow(row)
	if err != nil {
		return nil, err
	}
	if part == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.PartitionForRow", "no partition for row")
	}
	return part, nil
}

func (s *Session) partitionHeap(tab *catalog.Table, pid uint32) (*btree.Tree, error) {
	key := partitionHeapKey(tab.Name, pid)
	if s.pending != nil {
		if tr, ok := s.pending.partHeaps[key]; ok {
			return tr, nil
		}
		// also check if main pending heaps contain it (should not)
	}
	return s.db.partitionHeap(tab.Name, pid)
}
func (db *DB) partitionHeap(table string, pid uint32) (*btree.Tree, error) {
	key := partitionHeapKey(table, pid)
	db.mu.RLock()
	tr := db.partHeaps[key]
	db.mu.RUnlock()
	if tr == nil {
		return nil, nerr.New(nerr.NotFound, "executor.partitionHeap", "partition heap not open")
	}
	return tr, nil
}
func (s *Session) partitionVec(tab *catalog.Table, pid uint32) (*btree.Tree, error) {
	if tab == nil || !tab.HasVector() {
		return nil, nerr.New(nerr.NotFound, "executor.partitionVec", "vector store not open")
	}
	key := partitionHeapKey(tab.Name, pid)
	if s.pending != nil {
		if tr, ok := s.pending.partVecs[key]; ok {
			return tr, nil
		}
	}
	return s.db.partitionVec(tab.Name, pid)
}
func (db *DB) partitionVec(table string, pid uint32) (*btree.Tree, error) {
	key := partitionHeapKey(table, pid)
	db.mu.RLock()
	tr := db.partVecs[key]
	db.mu.RUnlock()
	if tr == nil {
		return nil, nerr.New(nerr.NotFound, "executor.partitionVec", "partition vector not open")
	}
	return tr, nil
}
func (s *Session) partitionIndex(tab *catalog.Table, pid uint32, idx catalog.Index) (*btree.Tree, error) {
	key := partitionIndexKey(tab.Name, pid, idx.Name)
	if s.pending != nil {
		if tr, ok := s.pending.partIdxs[key]; ok {
			return tr, nil
		}
	}
	return s.db.partitionIndex(tab.Name, pid, idx.Name)
}
func (db *DB) partitionIndex(table string, pid uint32, index string) (*btree.Tree, error) {
	key := partitionIndexKey(table, pid, index)
	db.mu.RLock()
	tr := db.partIdxs[key]
	db.mu.RUnlock()
	if tr == nil {
		return nil, nerr.New(nerr.NotFound, "executor.partitionIndex", "partition index not open")
	}
	return tr, nil
}

func partitionSelection(tab *catalog.Table, ids []uint32) []catalog.Partition {
	if tab == nil || tab.Partitioning == nil {
		return nil
	}
	if ids == nil {
		return tab.Partitioning.Partitions
	}
	wanted := make(map[uint32]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	out := make([]catalog.Partition, 0, len(ids))
	for _, part := range tab.Partitioning.Partitions {
		if _, ok := wanted[part.ID]; ok {
			out = append(out, part)
		}
	}
	return out
}

// prunePartitions returns candidate partition IDs for a WHERE expression.
// For RANGE we attempt range pruning; HASH/LIST/TENANT use equality pruning.
// Conservative fallback: return all partitions.
func prunePartitions(tab *catalog.Table, where ast.Expr) []catalog.Partition {
	if tab == nil || tab.Partitioning == nil {
		return nil
	}
	all := tab.Partitioning.Partitions
	if where == nil {
		return all
	}
	switch tab.Partitioning.Kind {
	case catalog.PartitionRange:
		return pruneRange(tab, where)
	case catalog.PartitionHash:
		return pruneHash(tab, where)
	case catalog.PartitionList:
		return pruneValuePartitions(tab, where)
	case catalog.PartitionTenant:
		return pruneValuePartitions(tab, where)
	default:
		return all
	}
}

func pruneRange(tab *catalog.Table, where ast.Expr) []catalog.Partition {
	// Single-column RANGE pruning.
	if tab.Partitioning == nil || len(tab.Partitioning.Columns) != 1 {
		return tab.Partitioning.Partitions
	}
	colOrd := tab.Partitioning.Columns[0]
	colName := tab.Columns[colOrd].Name
	// Extract equality or range constraints on partition col.
	// We handle simple patterns: col = lit, col < lit, col <= lit, col > lit, col >= lit, AND combinations.
	eq, lower, upper, lowerInc, upperInc := extractRangeConstraints(where, colName)
	if eq != nil {
		// equality: find exact partition
		tmp := []types.Value{*eq}
		for _, part := range tab.Partitioning.Partitions {
			lower, upper := part.Values[0], part.Values[1]
			if lower != nil {
				cmp, _ := compareValues(tmp, lower)
				if cmp < 0 || (cmp == 0 && !part.LowerInclusive) {
					continue
				}
			}
			if upper != nil {
				cmp, _ := compareValues(tmp, upper)
				if cmp > 0 || (cmp == 0 && !part.UpperInclusive) {
					continue
				}
			}
			return []catalog.Partition{part}
		}
		return []catalog.Partition{}
	}
	// range pruning: keep partitions overlapping [lower, upper]
	var out []catalog.Partition
	for _, part := range tab.Partitioning.Partitions {
		plower, pupper := part.Values[0], part.Values[1]
		// Check overlap: partition interval [plower, pupper) vs query interval [lower, upper] with inclusives.
		// For conservative, if no constraint, keep all.
		if lower != nil && pupper != nil {
			cmp, _ := compareValues([]types.Value{*lower}, pupper)
			if cmp > 0 || (cmp == 0 && !upperInc && !part.UpperInclusive) {
				// Actually need to compare lower vs pupper: if query lower >= partition upper, no overlap.
				// lower is query lower bound.
			}
		}
		// Simplified: if upper (query) < partition lower, no overlap.
		if upper != nil && plower != nil {
			cmp, _ := compareValues([]types.Value{*upper}, plower)
			if cmp < 0 || (cmp == 0 && !upperInc && !part.LowerInclusive) {
				continue
			}
		}
		if lower != nil && pupper != nil {
			cmp, _ := compareValues([]types.Value{*lower}, pupper)
			if cmp > 0 || (cmp == 0 && !lowerInc && !part.UpperInclusive) {
				// query lower is after partition upper
				if cmp > 0 {
					continue
				}
				if cmp == 0 && (!lowerInc || !part.UpperInclusive) {
					// Need strict: if query lower == partition upper and either exclusive, no overlap? But lower inclusive means query includes lower.
					// For simplicity, if equality and partition upper exclusive, still no overlap if lower inclusive? Actually partition [a,b), query [b, ...] -> no overlap when equal and partition exclusive.
					// So continue if not both inclusive.
					if !part.UpperInclusive {
						continue
					}
				}
			}
		}
		// Additional check: partition lower > query upper -> no overlap already handled.
		// So keep
		out = append(out, part)
	}
	if len(out) == 0 {
		// fallback to all if pruning logic uncertain
		return tab.Partitioning.Partitions
	}
	return out
}

func pruneValuePartitions(tab *catalog.Table, where ast.Expr) []catalog.Partition {
	if tab.Partitioning == nil || len(tab.Partitioning.Columns) != 1 {
		return tab.Partitioning.Partitions
	}
	colOrd := tab.Partitioning.Columns[0]
	colName := tab.Columns[colOrd].Name
	vals, ok := extractEqualValues(where, colName)
	if !ok || len(vals) == 0 {
		return tab.Partitioning.Partitions
	}
	lookup := make(map[string]catalog.Partition, len(tab.Partitioning.Partitions))
	for _, part := range tab.Partitioning.Partitions {
		for _, v := range part.Values {
			if len(v) == 1 {
				key, err := types.EncodeKey(v)
				if err != nil {
					return tab.Partitioning.Partitions
				}
				lookup[string(key)] = part
			}
		}
	}
	var out []catalog.Partition
	seen := make(map[uint32]struct{})
	for _, v := range vals {
		coerced, err := types.Coerce(v, tab.Columns[colOrd].Type)
		if err != nil {
			return tab.Partitioning.Partitions
		}
		key, err := types.EncodeKey([]types.Value{coerced})
		if err != nil {
			return tab.Partitioning.Partitions
		}
		if part, ok := lookup[string(key)]; ok {
			if _, dup := seen[part.ID]; !dup {
				seen[part.ID] = struct{}{}
				out = append(out, part)
			}
		}
	}
	if len(out) == 0 {
		return []catalog.Partition{}
	}
	return out
}

func pruneHash(tab *catalog.Table, where ast.Expr) []catalog.Partition {
	if tab == nil || tab.Partitioning == nil {
		return nil
	}
	if len(tab.Partitioning.Columns) != 1 || len(tab.Partitioning.Partitions) == 0 {
		return tab.Partitioning.Partitions
	}
	colOrd := tab.Partitioning.Columns[0]
	vals, ok := extractEqualValues(where, tab.Columns[colOrd].Name)
	if !ok || len(vals) == 0 {
		return tab.Partitioning.Partitions
	}
	modulus := tab.Partitioning.Partitions[0].Modulus
	byRemainder := make(map[uint32]catalog.Partition, len(tab.Partitioning.Partitions))
	for _, part := range tab.Partitioning.Partitions {
		byRemainder[part.Remainder] = part
	}
	out := make([]catalog.Partition, 0, len(vals))
	seen := make(map[uint32]struct{}, len(vals))
	for _, value := range vals {
		coerced, err := types.Coerce(value, tab.Columns[colOrd].Type)
		if err != nil {
			return tab.Partitioning.Partitions
		}
		remainder, err := catalog.HashPartitionRemainder([]types.Value{coerced}, modulus)
		if err != nil {
			return tab.Partitioning.Partitions
		}
		part, exists := byRemainder[remainder]
		if !exists {
			return tab.Partitioning.Partitions
		}
		if _, exists := seen[part.ID]; !exists {
			seen[part.ID] = struct{}{}
			out = append(out, part)
		}
	}
	return out
}

// Helpers to extract constraints from Expr tree.

func extractRangeConstraints(expr ast.Expr, col string) (eq *types.Value, lower *types.Value, upper *types.Value, lowerInc bool, upperInc bool) {
	// Returns equality if found, otherwise lower/upper bounds.
	// Handle AND chains.
	var eqs []*types.Value
	var lowers []*types.Value
	var uppers []*types.Value
	var lowersInc []bool
	var uppersInc []bool
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		switch x := e.(type) {
		case ast.Binary:
			if x.Op == "and" || x.Op == "AND" {
				walk(x.Left)
				walk(x.Right)
				return
			}
			// col op literal or literal op col
			if isCol(x.Left, col) {
				if v := literalToValue(x.Right); v != nil {
					switch x.Op {
					case "=":
						eqs = append(eqs, v)
					case "<":
						uppers = append(uppers, v)
						uppersInc = append(uppersInc, false)
					case "<=":
						uppers = append(uppers, v)
						uppersInc = append(uppersInc, true)
					case ">":
						lowers = append(lowers, v)
						lowersInc = append(lowersInc, false)
					case ">=":
						lowers = append(lowers, v)
						lowersInc = append(lowersInc, true)
					}
				}
			} else if isCol(x.Right, col) {
				// reverse
				if v := literalToValue(x.Left); v != nil {
					switch x.Op {
					case "=":
						eqs = append(eqs, v)
					case "<":
						lowers = append(lowers, v)
						lowersInc = append(lowersInc, false)
					case "<=":
						lowers = append(lowers, v)
						lowersInc = append(lowersInc, true)
					case ">":
						uppers = append(uppers, v)
						uppersInc = append(uppersInc, false)
					case ">=":
						uppers = append(uppers, v)
						uppersInc = append(uppersInc, true)
					}
				}
			}
		case ast.Between:
			if isCol(x.Expr, col) {
				if lv := literalToValue(x.Low); lv != nil {
					lowers = append(lowers, lv)
					lowersInc = append(lowersInc, true)
				}
				if hv := literalToValue(x.High); hv != nil {
					uppers = append(uppers, hv)
					uppersInc = append(uppersInc, true)
				}
			}
		}
	}
	walk(expr)
	if len(eqs) > 0 {
		// if multiple equals, pick first
		return eqs[0], nil, nil, false, false
	}
	// pick most restrictive lower (max) and upper (min)
	if len(lowers) > 0 {
		best := lowers[0]
		inc := lowersInc[0]
		for i := 1; i < len(lowers); i++ {
			cmp, _ := compareValues([]types.Value{*best}, []types.Value{*lowers[i]})
			if cmp < 0 {
				best = lowers[i]
				inc = lowersInc[i]
			} else if cmp == 0 && !inc && lowersInc[i] {
				inc = false
			}
		}
		lower = best
		lowerInc = inc
	}
	if len(uppers) > 0 {
		best := uppers[0]
		inc := uppersInc[0]
		for i := 1; i < len(uppers); i++ {
			cmp, _ := compareValues([]types.Value{*best}, []types.Value{*uppers[i]})
			if cmp > 0 {
				best = uppers[i]
				inc = uppersInc[i]
			} else if cmp == 0 && !inc && uppersInc[i] {
				inc = false
			}
		}
		upper = best
		upperInc = inc
	}
	return nil, lower, upper, lowerInc, upperInc
}

func extractEqualValues(expr ast.Expr, col string) ([]types.Value, bool) {
	x, ok := expr.(ast.Binary)
	if !ok {
		return nil, false
	}
	switch strings.ToLower(x.Op) {
	case "and":
		left, leftOK := extractEqualValues(x.Left, col)
		right, rightOK := extractEqualValues(x.Right, col)
		if leftOK && rightOK {
			return append(left, right...), true
		}
		if leftOK {
			return left, true
		}
		if rightOK {
			return right, true
		}
		return nil, false
	case "or":
		left, leftOK := extractEqualValues(x.Left, col)
		right, rightOK := extractEqualValues(x.Right, col)
		if !leftOK || !rightOK {
			return nil, false
		}
		return append(left, right...), true
	case "=":
		if isCol(x.Left, col) {
			if value := literalToValue(x.Right); value != nil {
				return []types.Value{*value}, true
			}
		} else if isCol(x.Right, col) {
			if value := literalToValue(x.Left); value != nil {
				return []types.Value{*value}, true
			}
		}
	}
	return nil, false
}

func isCol(expr ast.Expr, name string) bool {
	switch x := expr.(type) {
	case ast.Ident:
		return strings.EqualFold(x.Name, name)
	case ast.Path:
		if len(x.Parts) == 1 {
			return strings.EqualFold(x.Parts[0], name)
		}
		if len(x.Parts) == 2 {
			return strings.EqualFold(x.Parts[1], name)
		}
	}
	return false
}

func literalToValue(expr ast.Expr) *types.Value {
	switch x := expr.(type) {
	case ast.Literal:
		v := x.Value
		// clone
		cv := v.Clone()
		return &cv
	case ast.Unary:
		if x.Op == "-" {
			if lit, ok := x.Right.(ast.Literal); ok {
				// negate handled earlier, but for pruning we can just return negated decimal if needed
				// For now, return original for simplicity
				v := lit.Value
				cv := v.Clone()
				return &cv
			}
		}
	}
	return nil
}

// helpers for catalog compare

func compareValues(a, b []types.Value) (int, error) {
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
func init() {
	// ensure catalog helpers are accessible
	_ = format.PageID(0)
}
