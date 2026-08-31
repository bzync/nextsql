package executor

import (
	"time"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/optimizer"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/btree"
)

func applyLimitOffset(rows [][]types.Value, n, offset int64) [][]types.Value {
	if offset < 0 {
		offset = 0
	}
	if offset > 0 {
		if offset >= int64(len(rows)) {
			if rows == nil {
				return nil
			}
			return rows[:0]
		}
		rows = rows[offset:]
	}
	if n >= 0 && int64(len(rows)) > n {
		rows = rows[:n]
	}
	return rows
}

func (s *Session) forEachRow(p planner.Logical, fn func([]types.Value) error) error {
	if p == nil {
		return nil
	}
	start := time.Now()
	err := s.forEachRowInner(p, fn)
	if s.trace != nil {
		op := accessOp(p)
		if n := optimizer.Find(s.trace, op); n != nil && n.TimeNS == 0 {
			n.TimeNS = time.Since(start).Nanoseconds()
			n.CPUTimeNS = n.TimeNS
			n.Workers = 1
		}
	}
	return err
}

func accessOp(p planner.Logical) string {
	switch p.(type) {
	case planner.IndexScan:
		return "IndexScan"
	case planner.Search:
		return "Search"
	case planner.Nearest:
		return "Nearest"
	case planner.Candidates:
		return "Candidates"
	case planner.Rerank:
		return "Rerank"
	case planner.SeqScan, planner.Scan:
		return "SeqScan"
	case planner.Filter:
		return "Filter"
	case planner.Limit:
		return "Limit"
	default:
		return "Scan"
	}
}

func (s *Session) forEachRowInner(p planner.Logical, fn func([]types.Value) error) error {
	switch n := p.(type) {
	case planner.Empty:
		return nil
	case planner.With:
		rows, err := s.collectPlan(n)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := fn(row); err != nil {
				return err
			}
		}
		return nil
	case planner.CTEScan:
		rows, err := s.scanCTE(n)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := fn(row); err != nil {
				return err
			}
		}
		return nil
	case planner.SetOperation:
		rows, err := s.collectPlan(n)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := fn(row); err != nil {
				return err
			}
		}
		return nil
	case planner.Limit:
		var skipped, taken int64
		err := s.forEachRowInner(n.Input, func(row []types.Value) error {
			if skipped < n.Offset {
				skipped++
				return nil
			}
			if n.N >= 0 && taken >= n.N {
				return errStop
			}
			if err := fn(row); err != nil {
				return err
			}
			taken++
			if n.N >= 0 && taken >= n.N {
				return errStop
			}
			return nil
		})
		if err == errStop {
			return nil
		}
		return err
	case planner.Filter:
		tab := tableOf(n.Input)
		return s.forEachRowInner(n.Input, func(row []types.Value) error {
			ok, err := s.match(n.Pred, tab, row)
			if err != nil || !ok {
				return err
			}
			if s.trace != nil {
				if node := optimizer.Find(s.trace, "Filter"); node != nil {
					node.ActRows++
				}
			}
			return fn(row)
		})
	case planner.Scan:
		return s.scanHeap(n.Table, nil, nil, true, true, fn)
	case planner.SeqScan:
		if len(n.Segments) == 0 {
			return s.scanHeapPartitions(n.Table, n.Partitions, nil, nil, true, true, fn)
		}
		for _, seg := range n.Segments {
			if err := s.scanHeapPartitions(n.Table, n.Partitions, seg.Low, seg.High, seg.LowIncl, seg.HighIncl, fn); err != nil {
				return err
			}
		}
		return nil
	case planner.IndexScan:
		return s.scanIndex(n, fn)
	case planner.Search:
		rows, err := s.searchFulltext(n)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := fn(row); err != nil {
				return err
			}
		}
		return nil
	case planner.Nearest:
		rows, err := s.searchNearest(n)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := fn(row); err != nil {
				return err
			}
		}
		return nil
	case planner.Candidates:
		rows, err := s.execCandidates(n)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := fn(row); err != nil {
				return err
			}
		}
		return nil
	case planner.Rerank:
		rows, err := s.execRerank(n)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := fn(row); err != nil {
				return err
			}
		}
		return nil
	case planner.Project:
		return s.forEachRowInner(n.Input, fn)
	case planner.Window, planner.Sort, planner.Aggregate, planner.Facet:
		rows, err := s.collectPlan(n)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := fn(row); err != nil {
				return err
			}
		}
		return nil
	default:
		return nerr.New(nerr.Internal, "executor.forEachRow", "unexpected plan node")
	}
}

func (s *Session) scanHeap(tab *catalog.Table, low, high []types.Value, lowIncl, highIncl bool, fn func([]types.Value) error) error {
	return s.scanHeapPartitions(tab, nil, low, high, lowIncl, highIncl, fn)
}

func (s *Session) scanHeapPartitions(tab *catalog.Table, partitions []uint32, low, high []types.Value, lowIncl, highIncl bool, fn func([]types.Value) error) error {
	if tab != nil && tab.Partitioning != nil {
		start, end, err := encodeBounds(low, high, lowIncl, highIncl)
		if err != nil {
			return err
		}
		for _, part := range partitionSelection(tab, partitions) {
			heap, err := s.partitionHeap(tab, part.ID)
			if err != nil {
				return err
			}
			htx := s.x.use(heap)
			if err := htx.Range(start, end, func(_, val []byte) error {
				row, err := s.decodeHeapRow(tab, val)
				if err != nil {
					return err
				}
				if s.trace != nil {
					if n := optimizer.Find(s.trace, "SeqScan"); n != nil {
						n.ActRows++
					}
				}
				return fn(row)
			}); err != nil {
				return err
			}
		}
		return nil
	}
	heap, err := s.heapOf(tab)
	if err != nil {
		return err
	}
	start, end, err := encodeBounds(low, high, lowIncl, highIncl)
	if err != nil {
		return err
	}
	htx := s.x.use(heap)
	return htx.Range(start, end, func(_, val []byte) error {
		row, err := s.decodeHeapRow(tab, val)
		if err != nil {
			return err
		}
		if s.trace != nil {
			if n := optimizer.Find(s.trace, "SeqScan"); n != nil {
				n.ActRows++
			}
		}
		return fn(row)
	})
}

func (s *Session) scanIndex(n planner.IndexScan, fn func([]types.Value) error) error {
	tab := n.Table
	if tab != nil && tab.Partitioning != nil {
		if n.PK {
			return s.scanPartitionedPK(n, fn)
		}
		return s.scanPartitionedIndex(n, fn)
	}
	heap, err := s.heapOf(tab)
	if err != nil {
		return err
	}
	htx := s.x.use(heap)
	var start, end []byte
	if n.Spatial {
		start, end = n.GeoStart, n.GeoEnd
	} else {
		var err error
		start, end, err = encodeBounds(n.Low, n.High, n.LowIncl, n.HighIncl)
		if err != nil {
			return err
		}
	}
	emit := func(row []types.Value) error {
		ok, err := s.match(n.Residual, tab, row)
		if err != nil || !ok {
			return err
		}
		if s.trace != nil {
			if node := optimizer.Find(s.trace, "IndexScan"); node != nil {
				node.ActRows++
			}
		}
		return fn(row)
	}
	if n.PK {
		if point, key := uniquePoint(n); point {
			val, err := htx.Lookup(key)
			if err != nil {
				if nerr.HasCode(err, nerr.NotFound) {
					return nil
				}
				return err
			}
			row, err := s.decodeHeapRow(tab, val)
			if err != nil {
				return err
			}
			return emit(row)
		}
		return htx.Range(start, end, func(_, val []byte) error {
			row, err := s.decodeHeapRow(tab, val)
			if err != nil {
				return err
			}
			return emit(row)
		})
	}
	idx := indexByName(tab, n.IndexName)
	ix, err := s.indexOf(tab, idx)
	if err != nil {
		return err
	}
	itx := s.x.use(ix)
	pkTypes := pkTypeList(tab)
	emitIndex := func(key, val []byte) error {
		if n.IndexOnly {
			row, err := rowFromIndex(tab, idx, key, val)
			if err != nil {
				return err
			}
			return emit(row)
		}
		return s.fetchPK(htx, tab, val, pkTypes, emit)
	}
	if point, key := uniquePoint(n); point {
		val, err := itx.Lookup(key)
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				return nil
			}
			return err
		}
		return emitIndex(key, val)
	}
	return itx.Range(start, end, emitIndex)
}

func (s *Session) scanPartitionedIndex(n planner.IndexScan, fn func([]types.Value) error) error {
	tab := n.Table
	idx := indexByName(tab, n.IndexName)
	var start, end []byte
	if n.Spatial {
		start, end = n.GeoStart, n.GeoEnd
	} else {
		var err error
		start, end, err = encodeBounds(n.Low, n.High, n.LowIncl, n.HighIncl)
		if err != nil {
			return err
		}
	}
	emit := func(row []types.Value) error {
		ok, err := s.match(n.Residual, tab, row)
		if err != nil || !ok {
			return err
		}
		if s.trace != nil {
			if node := optimizer.Find(s.trace, "IndexScan"); node != nil {
				node.ActRows++
			}
		}
		return fn(row)
	}
	pkTypes := pkTypeList(tab)
	for _, part := range partitionSelection(tab, n.Partitions) {
		heap, err := s.partitionHeap(tab, part.ID)
		if err != nil {
			return err
		}
		local, err := s.partitionIndex(tab, part.ID, idx)
		if err != nil {
			return err
		}
		htx := s.x.use(heap)
		itx := s.x.use(local)
		emitIndex := func(key, val []byte) error {
			if n.IndexOnly {
				row, err := rowFromIndex(tab, idx, key, val)
				if err != nil {
					return err
				}
				return emit(row)
			}
			return s.fetchPK(htx, tab, val, pkTypes, emit)
		}
		if point, key := uniquePoint(n); point {
			val, err := itx.Lookup(key)
			if err != nil {
				if nerr.HasCode(err, nerr.NotFound) {
					continue
				}
				return err
			}
			if err := emitIndex(key, val); err != nil {
				return err
			}
			continue
		}
		if err := itx.Range(start, end, emitIndex); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) scanPartitionedPK(n planner.IndexScan, fn func([]types.Value) error) error {
	tab := n.Table
	start, end, err := encodeBounds(n.Low, n.High, n.LowIncl, n.HighIncl)
	if err != nil {
		return err
	}
	emit := func(val []byte) error {
		row, err := s.decodeHeapRow(tab, val)
		if err != nil {
			return err
		}
		if n.Residual != nil {
			ok, err := s.match(n.Residual, tab, row)
			if err != nil || !ok {
				return err
			}
		}
		return fn(row)
	}
	for _, part := range partitionSelection(tab, n.Partitions) {
		heap, err := s.partitionHeap(tab, part.ID)
		if err != nil {
			return err
		}
		htx := s.x.use(heap)
		if point, key := uniquePoint(n); point {
			val, err := htx.Lookup(key)
			if err != nil {
				if nerr.HasCode(err, nerr.NotFound) {
					continue
				}
				return err
			}
			if err := emit(val); err != nil {
				return err
			}
			continue
		}
		if err := htx.Range(start, end, func(_, val []byte) error { return emit(val) }); err != nil {
			return err
		}
	}
	return nil
}

func impliedIndexConsts(tab *catalog.Table, idx catalog.Index) map[int]types.Value {
	out := map[int]types.Value{}
	if tab == nil || idx.Predicate == nil {
		return out
	}
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		if e == nil {
			return
		}
		b, ok := e.(ast.Binary)
		if !ok {
			return
		}
		if b.Op == "AND" {
			walk(b.Left)
			walk(b.Right)
			return
		}
		if b.Op != "=" {
			return
		}
		id, ok := b.Left.(ast.Ident)
		lit, isLit := b.Right.(ast.Literal)
		if !ok {
			id, ok = b.Right.(ast.Ident)
			lit, isLit = b.Left.(ast.Literal)
		}
		if !ok || !isLit {
			return
		}
		if ord, found := tab.ColIndex(id.Name); found {
			out[ord] = lit.Value
		}
	}
	walk(idx.Predicate)
	return out
}

func rowFromIndex(tab *catalog.Table, idx catalog.Index, key, val []byte) ([]types.Value, error) {
	row := make([]types.Value, len(tab.Columns))
	for i := range tab.Columns {
		row[i] = types.Null(tab.Columns[i].Type)
	}
	payload, err := types.DecodeKey(val, indexPayloadTypes(tab, idx))
	if err != nil {
		return nil, err
	}
	for i, ord := range tab.PK {
		if i < len(payload) && ord >= 0 && ord < len(row) {
			row[ord] = payload[i]
		}
	}
	for i, ord := range idx.Include {
		off := len(tab.PK) + i
		if off < len(payload) && ord >= 0 && ord < len(row) {
			row[ord] = payload[off]
		}
	}
	for ord, v := range impliedIndexConsts(tab, idx) {
		if ord >= 0 && ord < len(row) && row[ord].Null {
			row[ord] = v
		}
	}
	keyTypes := make([]types.Type, 0, len(idx.Columns)+len(tab.PK))
	for i := range idx.Columns {
		keyTypes = append(keyTypes, idx.KeyType(tab, i))
	}
	if !idx.Unique {
		keyTypes = append(keyTypes, pkTypeList(tab)...)
	}
	if len(keyTypes) == 0 || len(key) == 0 {
		return row, nil
	}
	keyVals, err := types.DecodeKey(key, keyTypes)
	if err != nil {
		return nil, err
	}
	for i, ord := range idx.Columns {
		if i >= len(keyVals) || ord < 0 || ord >= len(row) {
			continue
		}
		if idx.KeyIsExpr(i) || (i == 0 && len(idx.Path) > 0) || idx.Spatial {
			continue
		}
		row[ord] = keyVals[i]
	}
	return row, nil
}

func (s *Session) fetchPK(htx *btree.Txn, tab *catalog.Table, idxVal []byte, pkTypes []types.Type, fn func([]types.Value) error) error {
	if len(idxVal) == 0 {
		return nil
	}
	pk, err := types.DecodeKey(idxVal, pkTypes)
	if err != nil {
		return err
	}
	k, err := types.EncodeKey(pk)
	if err != nil {
		return err
	}
	payload, err := htx.Lookup(k)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return nil
		}
		return err
	}
	row, err := s.decodeHeapRow(tab, payload)
	if err != nil {
		return err
	}
	return fn(row)
}

func uniquePoint(n planner.IndexScan) (bool, []byte) {
	if !n.Unique && !n.PK {
		return false, nil
	}
	if !n.LowIncl || !n.HighIncl {
		return false, nil
	}
	if len(n.Low) == 0 || len(n.Low) != len(n.High) || len(n.Low) != len(n.Columns) {
		return false, nil
	}
	for i := range n.Low {
		c, err := n.Low[i].Cmp(n.High[i])
		if err != nil || c != 0 {
			return false, nil
		}
	}
	k, err := types.EncodeKey(n.Low)
	if err != nil {
		return false, nil
	}
	return true, k
}

func encodeBounds(low, high []types.Value, lowIncl, highIncl bool) ([]byte, []byte, error) {
	var start, end []byte
	var err error
	if len(low) > 0 {
		start, err = types.EncodeKey(low)
		if err != nil {
			return nil, nil, err
		}
		if !lowIncl {
			start = types.PrefixEnd(start)
		}
	}
	if len(high) > 0 {
		end, err = types.EncodeKey(high)
		if err != nil {
			return nil, nil, err
		}
		if highIncl {
			end = types.PrefixEnd(end)
		}
	}
	return start, end, nil
}

func indexByName(tab *catalog.Table, name string) catalog.Index {
	for _, idx := range tab.Indexes {
		if idx.Name == name {
			return idx
		}
	}
	return catalog.Index{Name: name}
}

func pkTypeList(tab *catalog.Table) []types.Type {
	out := make([]types.Type, len(tab.PK))
	for i, ord := range tab.PK {
		out[i] = tab.Columns[ord].Type
	}
	return out
}
