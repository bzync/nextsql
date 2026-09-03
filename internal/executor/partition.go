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
	// Tag with the base table name (system.locks has no partition column).
	tr.SetName(table)
	return tr, nil
}

// partitionVecFor resolves the partition-local vector-payload store that owns
// row. Partitioned tables keep vector payloads per partition (not in the shared
// tab.VecMeta store) so each partition is self-contained for ATTACH/DETACH and
// partition-local HNSW.
func (s *Session) partitionVecFor(tab *catalog.Table, row []types.Value) (*btree.Tree, error) {
	part, err := s.partitionForRow(tab, row)
	if err != nil {
		return nil, err
	}
	return s.partitionVec(tab, part.ID)
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
	tr.SetName(table)
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
	// Tag with the base table name (not the index name), matching db.index.
	tr.SetName(table)
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
	case catalog.PartitionLegacyTenant:
		return pruneValuePartitions(tab, where)
	default:
		return all
	}
}

// pruneRange prunes RANGE partitions from a predicate. RANGE partition bounds
// are lexicographically ordered tuples covering the half-open interval
// [lower, upper) (lower inclusive, upper exclusive; nil is unbounded). The
// predicate is reduced to a query lower/upper bound prefix over the ordered
// partition-key columns: successive equality constraints extend both prefixes,
// and the first non-equality constraint contributes its own lower/upper literal
// and terminates the walk. A partition survives only when the query bound
// interval can intersect its [lower, upper) tuple interval, so a predicate that
// also pins trailing partition-key columns prunes partitions that merely share a
// leading value.
//
// This mirrors optimizer.pruneRangeForExplain; the executor helper is currently
// unused but is kept in sync with the authoritative optimizer path.
func pruneRange(tab *catalog.Table, where ast.Expr) []catalog.Partition {
	p := tab.Partitioning
	if p == nil || len(p.Columns) == 0 {
		return p.Partitions
	}
	var qlo, qhi []types.Value
	qloInc, qhiInc := true, true
	for _, ord := range p.Columns {
		colType := tab.Columns[ord].Type
		eq, lower, upper, lowerInc, upperInc := extractRangeConstraints(where, tab.Columns[ord].Name)
		if eq != nil {
			cv, err := types.Coerce(*eq, colType)
			if err != nil {
				break
			}
			qlo = append(qlo, cv)
			qhi = append(qhi, cv)
			continue
		}
		if lower != nil {
			if cv, err := types.Coerce(*lower, colType); err == nil {
				qlo = append(qlo, cv)
				qloInc = lowerInc
			}
		}
		if upper != nil {
			if cv, err := types.Coerce(*upper, colType); err == nil {
				qhi = append(qhi, cv)
				qhiInc = upperInc
			}
		}
		break
	}
	if len(qlo) == 0 && len(qhi) == 0 {
		return p.Partitions
	}
	qloRest := -1
	if !qloInc {
		qloRest = 1
	}
	qhiRest := 1
	if !qhiInc {
		qhiRest = -1
	}
	var out []catalog.Partition
	for _, part := range p.Partitions {
		lo, hi := part.Values[0], part.Values[1]
		prune := false
		if lo != nil && len(qhi) > 0 {
			if c, ok := cmpPartitionBoundTuple(qhi, qhiRest, lo); ok {
				if c < 0 || (c == 0 && !qhiInc) {
					prune = true
				}
			}
		}
		if !prune && hi != nil && len(qlo) > 0 {
			if c, ok := cmpPartitionBoundTuple(qlo, qloRest, hi); ok && c >= 0 {
				prune = true
			}
		}
		if !prune {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return []catalog.Partition{}
	}
	return out
}

// cmpPartitionBoundTuple compares a query bound prefix q against a full partition
// bound tuple pt using the order-preserving key encoding. When q is a strict
// prefix of pt, restInf decides the result (-1 == -infinity suffix, +1 == +infinity).
// ok is false only when a value pair cannot be compared.
func cmpPartitionBoundTuple(q []types.Value, restInf int, pt []types.Value) (int, bool) {
	m := len(q)
	if len(pt) < m {
		m = len(pt)
	}
	for i := 0; i < m; i++ {
		c, err := compareValues([]types.Value{q[i]}, []types.Value{pt[i]})
		if err != nil {
			return 0, false
		}
		if c != 0 {
			return c, true
		}
	}
	if len(q) >= len(pt) {
		return 0, true
	}
	return restInf, true
}

func pruneValuePartitions(tab *catalog.Table, where ast.Expr) []catalog.Partition {
	if tab.Partitioning == nil || len(tab.Partitioning.Columns) == 0 {
		return tab.Partitioning.Partitions
	}
	if len(tab.Partitioning.Columns) > 1 {
		return pruneMultiColumnListPartitions(tab, where)
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

// pruneMultiColumnListPartitions prunes multi-column LIST partitions only when
// every partition column is pinned to a single equality value; the resulting
// tuple then matches at most one partition's membership set. Any looser
// predicate keeps every partition (conservative).
func pruneMultiColumnListPartitions(tab *catalog.Table, where ast.Expr) []catalog.Partition {
	tuple := make([]types.Value, len(tab.Partitioning.Columns))
	for i, ord := range tab.Partitioning.Columns {
		vals, ok := extractEqualValues(where, tab.Columns[ord].Name)
		if !ok || len(vals) != 1 {
			return tab.Partitioning.Partitions
		}
		coerced, err := types.Coerce(vals[0], tab.Columns[ord].Type)
		if err != nil {
			return tab.Partitioning.Partitions
		}
		tuple[i] = coerced
	}
	key, err := types.EncodeKey(tuple)
	if err != nil {
		return tab.Partitioning.Partitions
	}
	for _, part := range tab.Partitioning.Partitions {
		for _, v := range part.Values {
			ek, err := types.EncodeKey(v)
			if err != nil {
				return tab.Partitioning.Partitions
			}
			if bytes.Equal(key, ek) {
				return []catalog.Partition{part}
			}
		}
	}
	return []catalog.Partition{}
}

func pruneHash(tab *catalog.Table, where ast.Expr) []catalog.Partition {
	if tab == nil || tab.Partitioning == nil {
		return nil
	}
	if len(tab.Partitioning.Columns) == 0 || len(tab.Partitioning.Partitions) == 0 {
		return tab.Partitioning.Partitions
	}
	modulus := tab.Partitioning.Partitions[0].Modulus
	byRemainder := make(map[uint32]catalog.Partition, len(tab.Partitioning.Partitions))
	for _, part := range tab.Partitioning.Partitions {
		byRemainder[part.Remainder] = part
	}
	// Multi-column HASH prunes only when every partition column is pinned to a
	// single equality value; the tuple then hashes to exactly one partition.
	if len(tab.Partitioning.Columns) > 1 {
		tuple := make([]types.Value, len(tab.Partitioning.Columns))
		for i, ord := range tab.Partitioning.Columns {
			cvals, cok := extractEqualValues(where, tab.Columns[ord].Name)
			if !cok || len(cvals) != 1 {
				return tab.Partitioning.Partitions
			}
			coerced, err := types.Coerce(cvals[0], tab.Columns[ord].Type)
			if err != nil {
				return tab.Partitioning.Partitions
			}
			tuple[i] = coerced
		}
		remainder, err := catalog.HashPartitionRemainder(tuple, modulus)
		if err != nil {
			return tab.Partitioning.Partitions
		}
		if part, ok := byRemainder[remainder]; ok {
			return []catalog.Partition{part}
		}
		return tab.Partitioning.Partitions
	}
	colOrd := tab.Partitioning.Columns[0]
	vals, ok := extractEqualValues(where, tab.Columns[colOrd].Name)
	if !ok || len(vals) == 0 {
		return tab.Partitioning.Partitions
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

// crossPartitionUniqueIndexes returns the logical secondary UNIQUE indexes of a
// partitioned table whose uniqueness spans every partition. Partial, expression,
// JSON-path, spatial, full-text, and vector indexes are excluded (the binder
// rejects UNIQUE for those on partitioned tables).
func crossPartitionUniqueIndexes(tab *catalog.Table) []catalog.Index {
	if tab == nil || tab.Partitioning == nil {
		return nil
	}
	var out []catalog.Index
	for _, idx := range tab.Indexes {
		if !idx.Unique || idx.Fulltext || idx.Vector || idx.Spatial || idx.Predicate != nil || idx.HasExpr() || len(idx.Path) > 0 {
			continue
		}
		out = append(out, idx)
	}
	return out
}

// crossPartitionUniqueConflict reports whether key already exists in a
// partition-local root of idx other than skipID. It is a pure probe: callers
// that are about to write must first take an exclusive lock on key (via
// Txn.LockExclusive) so concurrent writers to sibling partitions are
// serialized on the shared key-lock namespace.
func (s *Session) crossPartitionUniqueConflict(tab *catalog.Table, idx catalog.Index, skipID uint32, key []byte) (bool, error) {
	for _, part := range tab.Partitioning.Partitions {
		if part.ID == skipID {
			continue
		}
		ix, err := s.partitionIndex(tab, part.ID, idx)
		if err != nil {
			return false, err
		}
		if _, err := s.lookupConflict(s.x.use(ix), key); err == nil {
			return true, nil
		} else if !nerr.HasCode(err, nerr.NotFound) {
			return false, err
		}
	}
	return false, nil
}

// checkCrossPartitionUnique enforces a secondary UNIQUE index across partitions
// on the write path. It takes an exclusive lock on the encoded index key (via
// itx, whose lock namespace is engine-global) so concurrent inserts of the same
// value into sibling partitions serialize, then probes every other partition's
// local root. The row's own partition is skipped: an in-partition duplicate is
// still caught by the ordinary local-root Insert.
func (s *Session) checkCrossPartitionUnique(tab *catalog.Table, idx catalog.Index, itx *btree.Txn, row []types.Value) error {
	if tab.Partitioning == nil || row == nil {
		return nil
	}
	if !idx.Unique || idx.Fulltext || idx.Vector || idx.Spatial || idx.Predicate != nil || idx.HasExpr() || len(idx.Path) > 0 {
		return nil
	}
	part, err := s.partitionForRow(tab, row)
	if err != nil {
		return err
	}
	k, _, err := s.indexKV(tab, idx, row)
	if err != nil {
		return err
	}
	if err := itx.LockExclusive(k); err != nil {
		return err
	}
	dup, err := s.crossPartitionUniqueConflict(tab, idx, part.ID, k)
	if err != nil {
		return err
	}
	if dup {
		return nerr.New(nerr.AlreadyExists, "executor.maintainIndexes", "duplicate key across partitions for UNIQUE index")
	}
	return nil
}

// verifyCrossPartitionUnique fails closed if the same key appears in more than
// one partition-local root of a freshly (re)built UNIQUE index. Within-partition
// duplicates are already rejected when each local root is populated, so this
// only needs to compare each partition against the ones before it.
func (s *Session) verifyCrossPartitionUnique(tab *catalog.Table, idx catalog.Index) error {
	if tab == nil || tab.Partitioning == nil {
		return nil
	}
	parts := tab.Partitioning.Partitions
	for i := 1; i < len(parts); i++ {
		prev := make([]*btree.Txn, i)
		for j := 0; j < i; j++ {
			pix, err := s.partitionIndex(tab, parts[j].ID, idx)
			if err != nil {
				return err
			}
			prev[j] = s.x.use(pix)
		}
		cur, err := s.partitionIndex(tab, parts[i].ID, idx)
		if err != nil {
			return err
		}
		if err := s.x.use(cur).Range(nil, nil, func(key, _ []byte) error {
			if err := s.budget().Check(); err != nil {
				return err
			}
			for _, ptx := range prev {
				if _, err := ptx.Lookup(key); err == nil {
					return nerr.New(nerr.AlreadyExists, "executor.CreateIndex", "duplicate key across partitions for UNIQUE index")
				} else if !nerr.HasCode(err, nerr.NotFound) {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
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
