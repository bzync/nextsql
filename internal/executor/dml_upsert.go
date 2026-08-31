package executor

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/binder"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/btree"
	"github.com/bzync/nextsql/internal/txn"
)

func (s *Session) execUpsert(p planner.Upsert) (*Result, error) {
	tab, ok := s.lookup(p.Table.Name)
	if !ok {
		return nil, nerr.New(nerr.NotFound, "executor.Upsert", "unknown table")
	}
	heap, err := s.heapOf(tab)
	if err != nil {
		return nil, err
	}
	htx := s.x.use(heap)
	empty := make([]types.Value, len(tab.Columns))
	for i := range empty {
		empty[i] = types.Null(tab.Columns[i].Type)
	}
	var (
		n   int64
		out [][]types.Value
	)
	for _, exprs := range p.Rows {
		row := append([]types.Value(nil), empty...)
		for j, ex := range exprs {
			v, err := s.evalInsertValue(ex, tab, p.Columns[j], row)
			if err != nil {
				return nil, err
			}
			v, err = types.Coerce(v, tab.Columns[p.Columns[j]].Type)
			if err != nil {
				return nil, err
			}
			row[p.Columns[j]] = v
		}
		for i := range row {
			nv, err := s.applyDefault(tab, i, row[i])
			if err != nil {
				return nil, err
			}
			if nv.Null && tab.Columns[i].NotNull {
				return nil, nerr.New(nerr.InvalidArgument, "executor.Upsert", "NULL in NOT NULL column")
			}
			row[i] = nv
		}
		if err := s.checkLegacyTenantRow(tab, row); err != nil {
			return nil, err
		}
		final, err := s.upsertRow(tab, htx, p, row)
		if err != nil {
			return nil, err
		}
		if err := s.collectReturning(&out, p.Returning, tab, final, row); err != nil {
			return nil, err
		}
		n++
	}
	if err := s.maybeAutoAnalyze(tab, n); err != nil {
		return nil, err
	}
	s.recordAutomaticMaintenance(tab, n)
	return returningResult(p.Returning, out, n), nil
}

func (s *Session) upsertRow(tab *catalog.Table, htx *btree.Txn, p planner.Upsert, proposed []types.Value) ([]types.Value, error) {
	for attempt := 0; attempt < 2; attempt++ {
		old, found, err := s.findConflict(tab, htx, p, proposed)
		if err != nil {
			return nil, err
		}
		if found {
			neu, err := s.applyUpsertUpdate(tab, p, old, proposed)
			if err != nil {
				return nil, err
			}
			s.conflictWrite = true
			err = s.replaceRow(tab, htx, old, neu)
			s.conflictWrite = false
			if err != nil {
				return nil, err
			}
			return neu, nil
		}
		err = s.writeRow(tab, htx, proposed, true)
		if err == nil {
			return proposed, nil
		}
		if attempt == 0 && (nerr.HasCode(err, nerr.AlreadyExists) || nerr.HasCode(err, nerr.Serialization)) {
			if _, own, lerr := s.lookupHeapPK(tab, htx, proposed); lerr != nil {
				return nil, lerr
			} else if own && !p.UniquePK {
				return nil, err
			}
			continue
		}
		return nil, err
	}
	return nil, nerr.New(nerr.AlreadyExists, "executor.Upsert", "duplicate key")
}

func (s *Session) applyUpsertUpdate(tab *catalog.Table, p planner.Upsert, old, proposed []types.Value) ([]types.Value, error) {
	if !p.DefaultSet {
		evalTab := excludedEvalTable(tab)
		evalRow := append(append([]types.Value(nil), old...), proposed...)
		neu := append([]types.Value(nil), old...)
		for _, set := range p.Sets {
			v, err := s.eval(set.Expr, evalTab, evalRow)
			if err != nil {
				return nil, err
			}
			v, err = types.Coerce(v, tab.Columns[set.Col].Type)
			if err != nil {
				return nil, err
			}
			if v.Null && tab.Columns[set.Col].NotNull {
				return nil, nerr.New(nerr.InvalidArgument, "executor.Upsert", "NULL in NOT NULL column")
			}
			neu[set.Col] = v
		}
		if err := s.checkLegacyTenantRow(tab, old); err != nil {
			return nil, err
		}
		if err := s.checkLegacyTenantRow(tab, neu); err != nil {
			return nil, err
		}
		return neu, nil
	}
	keep := make(map[int]struct{}, len(p.UniqueCols)+len(tab.PK))
	for _, c := range p.UniqueCols {
		keep[c] = struct{}{}
	}
	for _, c := range tab.PK {
		keep[c] = struct{}{}
	}
	neu := append([]types.Value(nil), old...)
	for _, col := range p.Columns {
		if _, skip := keep[col]; skip {
			continue
		}
		neu[col] = proposed[col]
	}
	if err := s.checkLegacyTenantRow(tab, old); err != nil {
		return nil, err
	}
	if err := s.checkLegacyTenantRow(tab, neu); err != nil {
		return nil, err
	}
	return neu, nil
}

func (s *Session) findConflict(tab *catalog.Table, htx *btree.Txn, p planner.Upsert, proposed []types.Value) ([]types.Value, bool, error) {
	if tab.Partitioning != nil {
		return s.findConflictPartitioned(tab, p, proposed)
	}
	keyVals := make([]types.Value, len(p.UniqueCols))
	for i, ord := range p.UniqueCols {
		if ord < 0 || ord >= len(proposed) {
			return nil, false, nerr.New(nerr.Internal, "executor.Upsert", "unique column out of range")
		}
		keyVals[i] = proposed[ord]
	}
	k, err := types.EncodeKey(keyVals)
	if err != nil {
		return nil, false, err
	}
	var (
		probe *btree.Txn
		pk    []byte
	)
	if p.UniquePK {
		probe = htx
		if err := probe.LockExclusive(k); err != nil {
			return nil, false, err
		}
		raw, err := s.lookupConflict(probe, k)
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				return nil, false, nil
			}
			return nil, false, err
		}
		row, err := s.decodeHeapRow(tab, raw)
		if err != nil {
			return nil, false, err
		}
		return row, true, nil
	}
	ix, err := s.indexOf(tab, indexByName(tab, p.UniqueIdx))
	if err != nil {
		return nil, false, err
	}
	probe = s.x.use(ix)
	if err := probe.LockExclusive(k); err != nil {
		return nil, false, err
	}
	pk, err = s.lookupConflict(probe, k)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	heapKey, err := indexPKKey(tab, pk)
	if err != nil {
		return nil, false, err
	}
	raw, err := s.lookupConflict(htx, heapKey)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	row, err := s.decodeHeapRow(tab, raw)
	if err != nil {
		return nil, false, err
	}
	return row, true, nil
}

// findConflictPartitioned resolves an existing UPSERT-conflicting row for a
// partitioned table. Every primary key includes every partition column, so a
// PK-target UPSERT's proposed row routes to exactly one partition and any PK
// conflict must live in that same partition-local heap. A secondary UNIQUE
// target is enforced across partitions, so the conflicting key may live in any
// partition: one exclusive lock on the encoded key (the engine key-lock
// namespace is global, so concurrent UPSERTs of the same value serialize) then
// probe every partition-local root.
func (s *Session) findConflictPartitioned(tab *catalog.Table, p planner.Upsert, proposed []types.Value) ([]types.Value, bool, error) {
	keyVals := make([]types.Value, len(p.UniqueCols))
	for i, ord := range p.UniqueCols {
		if ord < 0 || ord >= len(proposed) {
			return nil, false, nerr.New(nerr.Internal, "executor.Upsert", "unique column out of range")
		}
		keyVals[i] = proposed[ord]
	}
	k, err := types.EncodeKey(keyVals)
	if err != nil {
		return nil, false, err
	}
	if p.UniquePK {
		part, err := s.partitionForRow(tab, proposed)
		if err != nil {
			return nil, false, err
		}
		ph, err := s.partitionHeap(tab, part.ID)
		if err != nil {
			return nil, false, err
		}
		probe := s.x.use(ph)
		if err := probe.LockExclusive(k); err != nil {
			return nil, false, err
		}
		raw, err := s.lookupConflict(probe, k)
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				return nil, false, nil
			}
			return nil, false, err
		}
		row, err := s.decodeHeapRow(tab, raw)
		if err != nil {
			return nil, false, err
		}
		return row, true, nil
	}
	idx := indexByName(tab, p.UniqueIdx)
	parts := tab.Partitioning.Partitions
	if len(parts) == 0 {
		return nil, false, nil
	}
	lockIx, err := s.partitionIndex(tab, parts[0].ID, idx)
	if err != nil {
		return nil, false, err
	}
	if err := s.x.use(lockIx).LockExclusive(k); err != nil {
		return nil, false, err
	}
	for _, part := range parts {
		ix, err := s.partitionIndex(tab, part.ID, idx)
		if err != nil {
			return nil, false, err
		}
		pk, err := s.lookupConflict(s.x.use(ix), k)
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				continue
			}
			return nil, false, err
		}
		heapKey, err := indexPKKey(tab, pk)
		if err != nil {
			return nil, false, err
		}
		ph, err := s.partitionHeap(tab, part.ID)
		if err != nil {
			return nil, false, err
		}
		raw, err := s.lookupConflict(s.x.use(ph), heapKey)
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				continue
			}
			return nil, false, err
		}
		row, err := s.decodeHeapRow(tab, raw)
		if err != nil {
			return nil, false, err
		}
		return row, true, nil
	}
	return nil, false, nil
}

func (s *Session) lookupHeapPK(tab *catalog.Table, htx *btree.Txn, row []types.Value) ([]types.Value, bool, error) {
	if tab.Partitioning != nil {
		part, err := s.partitionForRow(tab, row)
		if err != nil {
			return nil, false, err
		}
		ph, err := s.partitionHeap(tab, part.ID)
		if err != nil {
			return nil, false, err
		}
		htx = s.x.use(ph)
	}
	pk, err := types.EncodeKey(tab.PKValues(row))
	if err != nil {
		return nil, false, err
	}
	raw, err := s.lookupConflict(htx, pk)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	decoded, err := s.decodeHeapRow(tab, raw)
	if err != nil {
		return nil, false, err
	}
	return decoded, true, nil
}

func (s *Session) lookupConflict(tx *btree.Txn, key []byte) ([]byte, error) {
	if snap, ok := s.latestSnap(); ok {
		return tx.LookupAt(key, snap)
	}
	return tx.Lookup(key)
}

func (s *Session) latestSnap() (txn.Snapshot, bool) {
	h, tm, err := s.fkTM()
	if err != nil {
		return txn.Snapshot{}, false
	}
	return tm.Capture(h.ID), true
}

func (s *Session) collectReturning(dst *[][]types.Value, ret binder.Returning, tab *catalog.Table, row, proposed []types.Value) error {
	if len(ret.Exprs) == 0 {
		return nil
	}
	if len(*dst)+1 > s.budget().ResultRows() {
		return nerr.New(nerr.Exhausted, "executor.Returning", "result exceeds row limit")
	}
	evalTab := ret.Eval
	evalRow := row
	if evalTab != nil && proposed != nil && len(evalTab.Columns) == 2*len(tab.Columns) {
		evalRow = append(append([]types.Value(nil), row...), proposed...)
	} else if evalTab == nil {
		evalTab = tab
	}
	out := make([]types.Value, len(ret.Exprs))
	for i, ex := range ret.Exprs {
		v, err := s.eval(ex, evalTab, evalRow)
		if err != nil {
			return err
		}
		out[i] = v
	}
	*dst = append(*dst, out)
	return nil
}

func returningResult(ret binder.Returning, rows [][]types.Value, affected int64) *Result {
	if len(ret.Names) == 0 {
		return &Result{Affected: affected}
	}
	return &Result{Columns: append([]string(nil), ret.Names...), Rows: rows, Affected: affected}
}

func excludedEvalTable(tab *catalog.Table) *catalog.Table {
	out := tab.Clone()
	for _, c := range tab.Columns {
		out.Columns = append(out.Columns, catalog.Column{
			Name:    "excluded." + c.Name,
			Type:    c.Type,
			NotNull: c.NotNull,
		})
	}
	return out
}
